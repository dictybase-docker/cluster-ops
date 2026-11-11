# dcr-mcp Server Deploy

The `dcr-mcp/` Pulumi project deploys **dcr-mcp** (`dictybase/dcr-mcp`), the dictyBase Stock Center Model Context Protocol (MCP) server, into one namespace. The project creates the Deployment `dcr-mcp-api-server` and the NodePort Service `dcr-mcp-api` on port `8080`. The server reads the dictyBase GraphQL server over HTTP, and it needs no database, no Secret, and no volume.

**The project has two target clusters, and both have a registry entry in `config/clusters/`.** `dcr-kube1` (stack `dcr-kube1`, namespace `prod`) sends external traffic through the central Cilium Gateway ([`gateway-deploy.md`](gateway-deploy.md)). `dcr-experiments` (stack `experiments`, namespace `dev`) is the legacy `nginx` + `kcert` cluster, and the developer CI workflow of `dictyBase/dcr-mcp` deploys to it on each push to `develop`.

**On a first setup, no image and no release tag exist.** The first `dictybase/dcr-mcp` image comes from the developer CI workflow, and that workflow clones this repository at branch `develop` to apply the stack. So you push `dcr-mcp/Pulumi.<stack>.yaml` ([Section 6](#6-push-the-stack-file-before-the-first-ci-build)) **before** you start the first build, and you pin the tag that the build publishes after it ([Section 7](#7-build-and-deploy-through-developer-ci-or-pin-an-existing-image-tag)).

**The per-app TLS Ingress is optional, and it is off by default.** With the default, the stack creates the Deployment and the Service only. [Section 5](#5-configure-legacy-ingress-mode-optional) gives the procedure to turn the Ingress on for the legacy cluster.

**Status**: Production procedure. Run `just gcp-pulumi scaffold-dcr-mcp-stack` to create a new cluster stack file from the registry and template, or `just gcp-pulumi ensure-stack --folder dcr-mcp` when the stack file already exists.

![dcr-mcp Server Deploy Workflow](reference/dcr-mcp/dcr-mcp-deploy-workflow.png)

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Register the Cluster and Check the Prerequisites](#1-register-the-cluster-and-check-the-prerequisites)
- [2. Scaffold or Select the Stack](#2-scaffold-or-select-the-stack)
- [3. Override the GraphQL Endpoint (Optional)](#3-override-the-graphql-endpoint-optional)
- [4. Configure Server Options (Optional)](#4-configure-server-options-optional)
- [5. Configure Legacy Ingress Mode (Optional)](#5-configure-legacy-ingress-mode-optional)
- [6. Push the Stack File Before the First CI Build](#6-push-the-stack-file-before-the-first-ci-build)
- [7. Build and Deploy Through Developer CI, or Pin an Existing Image Tag](#7-build-and-deploy-through-developer-ci-or-pin-an-existing-image-tag)
- [8. Expose Through the Production Gateway](#8-expose-through-the-production-gateway)
- [9. Verify the Rollout](#9-verify-the-rollout)
- [10. Enable Production Tag Deploys](#10-enable-production-tag-deploys)
- [11. Troubleshooting](#11-troubleshooting)
- [12. Related Documents](#12-related-documents)

---

## Quick Reference

For experienced users. Full details in the sections below.

```bash
# Enter the cluster environment first
just cluster-env --env <env> --cluster <cluster>

# 1. Ensure the cluster registry entry exists, then check the prerequisites
just gcp-cluster register-cluster
just dcr-mcp check

# 2. Scaffold the stack on a new cluster, or select an existing stack
just gcp-pulumi scaffold-dcr-mcp-stack
just gcp-pulumi ensure-stack --folder dcr-mcp

# 3. Optional. Read a GraphQL server outside the cluster. The default is in-cluster.
just gcp-pulumi set-config --folder dcr-mcp --key properties.graphqlEndpoint --value https://<graphql-host>/graphql

# 4. Optional. Change the server options. The defaults are shown.
just gcp-pulumi set-config --folder dcr-mcp --key properties.stateless --value true
just gcp-pulumi set-config --folder dcr-mcp --key properties.rateLimit --value 120
just gcp-pulumi set-config --folder dcr-mcp --key properties.allowedOrigins.0 --value "*"

# 5. Optional, legacy 'experiments' cluster only. The default is false (no Ingress).
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.enabled --value false

# 6. Push the stack file BEFORE the first CI build. The tag is still 'bootstrap'.
git add "config/clusters/${CLUSTER_NAME}.yaml" "dcr-mcp/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: scaffold ${PULUMI_STACK} stack" && git push

# 7a. First build on 'experiments': developer CI builds the image and applies the
#     stack. Record the published tag afterwards.
gh run list --repo dictyBase/dcr-mcp --workflow ci.yml --branch develop --limit 1
TAG="develop-$(gh api repos/dictyBase/dcr-mcp/commits/develop --jq '.sha[0:7]')"
just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value "${TAG}"
git add "dcr-mcp/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: pin ${TAG}" && git push

# 7b. The tag already exists on Docker Hub: pin it and apply the stack here
just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value <immutable-tag>
just gcp-pulumi preview --folder dcr-mcp
just gcp-pulumi create-resource --folder dcr-mcp
git add "dcr-mcp/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: pin image tag" && git push

# 8. Gateway clusters. Set the dedicated hostname, then deploy the route.
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value true
just gateway deploy
just gateway show-ip
git add "gateway/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: gateway route" && git push

# 9. Verify the rollout and the Gateway route
just dcr-mcp verify
just gateway verify

# 10. Optional, one time per cluster. Let release tags deploy.
just ci create-deploy-key
just ci render-composite-deploy
just ci set-deploy-secret --repos dictyBase/dcr-mcp
just ci sync-deploy-vars --repos dictyBase/dcr-mcp
just ci render-tag-deploy --app dcr-mcp --project dcr-mcp --out bin/ci-render/dcr-mcp-tag-build.yaml
```

---

**All of the commands below run in the cluster-env sub-shell.**

- Enter the sub-shell with `just cluster-env --env <env> --cluster <cluster>`. The sub-shell reads `.env.<env>.<cluster>` and exports `CLUSTER_NAME`, `CLUSTER_ENV`, `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Each recipe defaults `--stack` to `$PULUMI_STACK`. No command below needs the flag.
- Outside the sub-shell the recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Register the Cluster and Check the Prerequisites

`register-cluster` writes `config/clusters/<cluster>.yaml` when the entry is absent, keeps an existing entry without overwriting it, and validates the entry with `registry-show`. `check` is read-only: it matches the registry entry to this stack, and it checks the namespace, Service `graphql-api`, and the rollout of `graphql-api-server`.
→ [Cluster registration](reference/dcr-mcp/deployment.md#cluster-registration-one-time-per-cluster) · [Prerequisites](reference/dcr-mcp/deployment.md#prerequisites)

```bash
just gcp-cluster register-cluster
just dcr-mcp check
```

---

## 2. Scaffold or Select the Stack

`scaffold-dcr-mcp-stack` initializes the stack in the Pulumi backend with the cluster KMS key, and it writes `dcr-mcp/Pulumi.${PULUMI_STACK}.yaml` with the placeholder `image.tag: bootstrap`. The CI workflow needs that stack in the backend, so run this step before the first build. Run `ensure-stack` when the stack file already exists.
→ [Create the stack](reference/dcr-mcp/deployment.md#create-the-stack) · [Bootstrap order](reference/dcr-mcp/deployment.md#bootstrap-order-on-a-new-cluster)

```bash
# New cluster (creates the stack and writes dcr-mcp/Pulumi.${PULUMI_STACK}.yaml)
just gcp-pulumi scaffold-dcr-mcp-stack

# Existing stack file
just gcp-pulumi ensure-stack --folder dcr-mcp
```

---

## 3. Override the GraphQL Endpoint (Optional)

**Optional. The default is in-cluster. Skip this section to read `graphql-api` inside the cluster.**

Set `properties.graphqlEndpoint` only when the server must read a GraphQL URL outside the cluster. The value replaces the in-cluster address.
→ [GraphQL endpoint resolution](reference/dcr-mcp/deployment.md#graphql-endpoint-resolution)

```bash
just gcp-pulumi set-config --folder dcr-mcp --key properties.graphqlEndpoint --value https://<graphql-host>/graphql
```

---

## 4. Configure Server Options (Optional)

**Optional. The template writes the production defaults. Skip this section to keep them.**

`properties.stateless` keeps each request independent, so any replica answers it. `properties.rateLimit` is the maximum number of `/mcp` requests for each client IP address in one minute, and `properties.allowedOrigins` is the CORS list for browser clients.
→ [Server options and container flags](reference/dcr-mcp/deployment.md#server-options-and-container-flags)

```bash
just gcp-pulumi set-config --folder dcr-mcp --key properties.stateless --value true
just gcp-pulumi set-config --folder dcr-mcp --key properties.rateLimit --value 120
just gcp-pulumi set-config --folder dcr-mcp --key properties.allowedOrigins.0 --value "*"
```

---

## 5. Configure Legacy Ingress Mode (Optional)

**Optional. The default is off. Keep it off on a Gateway cluster such as `dcr-kube1`.**

On the legacy `dcr-experiments` cluster, the stack owns the external route: set `hosts`, `path`, `tlsSecret`, and `label` first, then set `properties.ingress.enabled` to `true` to create the `nginx` Ingress `dcr-mcp-ingress`. The host is dedicated to `dcr-mcp`, and the path is `/`.
→ [Legacy Ingress mode](reference/dcr-mcp/deployment.md#legacy-ingress-mode-dcr-experiments)

```bash
# Gateway cluster: keep the Ingress off (default). You can also omit the key.
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.enabled --value false

# Legacy 'experiments' stack: set the five keys first, then enable the Ingress.
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.path --value /
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.tlsSecret --value <mcp-tls-secret>
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.label.name --value kcert.dev/ingress
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.label.value --value managed
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.enabled --value true
```

---

## 6. Push the Stack File Before the First CI Build

**CAUTION: Push this commit before the first build in `dictyBase/dcr-mcp`.**

The build clones cluster-ops at branch `develop` and applies the `dcr-mcp/Pulumi.<stack>.yaml` that the clone holds, so a stack file that stays local makes the deploy step fail. The tag in this commit is still `bootstrap`, and that is correct: CI replaces the tag in its own clone.
→ [Bootstrap order](reference/dcr-mcp/deployment.md#bootstrap-order-on-a-new-cluster) · [CI deployment](reference/dcr-mcp/deployment.md#ci-deployment)

```bash
git add "config/clusters/${CLUSTER_NAME}.yaml" "dcr-mcp/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: scaffold ${PULUMI_STACK} stack" && git push
```

---

## 7. Build and Deploy Through Developer CI, or Pin an Existing Image Tag

Path A is the first setup of the `experiments` stack: CI builds `dictybase/dcr-mcp:develop-<sha>`, applies the stack, and you record that tag in Git afterwards. Path B is each cluster where the tag already exists on Docker Hub: you pin the tag and apply the stack yourself. `just ci latest-tag` works only after the first release tag exists in `dictyBase/dcr-mcp`.
→ [Image tag](reference/dcr-mcp/deployment.md#image-tag) · [Deploy behavior](reference/dcr-mcp/deployment.md#deploy-behavior)

```bash
# Path A. First build of the 'experiments' stack. Start the run with a push or a
# merge into develop in dictyBase/dcr-mcp, then watch it.
gh run list --repo dictyBase/dcr-mcp --workflow ci.yml --branch develop --limit 1
gh run watch --repo dictyBase/dcr-mcp <run-id>

# Path A. CI applied the stack already. Record the published tag, then push it.
TAG="develop-$(gh api repos/dictyBase/dcr-mcp/commits/develop --jq '.sha[0:7]')"
just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value "${TAG}"
git add "dcr-mcp/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: pin ${TAG}" && git push

# Path B. Production cluster or manual deploy, with a tag that Docker Hub holds.
just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value <immutable-tag>
just gcp-pulumi preview --folder dcr-mcp
just gcp-pulumi create-resource --folder dcr-mcp
git add "dcr-mcp/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: pin image tag" && git push
```

---

## 8. Expose Through the Production Gateway

**Gateway clusters only. Skip this section on the legacy `experiments` stack, which uses [Section 5](#5-configure-legacy-ingress-mode-optional).**

The Gateway route needs Service `dcr-mcp-api`, so apply the `dcr-mcp` stack first. Point the DNS `A` record for `<mcp-host>` at the address that `show-ip` prints.
→ [Production Gateway route](reference/dcr-mcp/deployment.md#production-gateway-route-recommended) · [Gateway MCP route](reference/gateway/deployment.md#mcp-route)

```bash
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value true
just gateway deploy
just gateway show-ip
git add "gateway/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "dcr-mcp: gateway route" && git push
```

---

## 9. Verify the Rollout

The recipes are read-only. `dcr-mcp verify` waits for the rollout, compares the running image with the tag in the stack file, and checks Service `dcr-mcp-api`. `gateway verify` passes after the DNS record resolves and the TLS Secret exists.
→ [Verification](reference/dcr-mcp/deployment.md#verification)

```bash
just dcr-mcp verify
just gateway verify
```

---

## 10. Enable Production Tag Deploys

**Optional. Do this one time for each cluster. Skip it while production deploys stay manual.**

`dcr-mcp` is absent from `config/services.yaml`, so the bulk backend recipes do not cover it. These five recipes mint the deployer key, publish the credential and the `<CI_ENV>_*` variables, and render a tag workflow file that does nothing until a pull request adds it to `dictyBase/dcr-mcp`.
→ [Tag deploy setup](reference/dcr-mcp/deployment.md#tag-deploy-setup)

```bash
just ci create-deploy-key
just ci render-composite-deploy
just ci set-deploy-secret --repos dictyBase/dcr-mcp
just ci sync-deploy-vars --repos dictyBase/dcr-mcp
just ci render-tag-deploy --app dcr-mcp --project dcr-mcp --out bin/ci-render/dcr-mcp-tag-build.yaml
```

---

## 11. Troubleshooting

→ [Troubleshooting table](reference/dcr-mcp/deployment.md#troubleshooting)

---

## 12. Related Documents

**Reference details for this guide:**
- [dcr-mcp server deployment details](reference/dcr-mcp/deployment.md)

**Other documents:**
- Gateway routing (`gateway/`): [`gateway-deploy.md`](gateway-deploy.md)
- Upstream GraphQL server (`graphql_server/`): [`graphql-deploy.md`](graphql-deploy.md)
- Cluster bootstrap and the `cluster-env` sub-shell: [`pulumi-setup.md`](pulumi-setup.md)
- Cluster registry (`config/clusters/<cluster>.yaml`): [`reference/backend/cluster-registry.md`](reference/backend/cluster-registry.md)
- CI credentials and variables: [`reference/backend/ci-credentials.md`](reference/backend/ci-credentials.md), [`reference/backend/ci-variables.md`](reference/backend/ci-variables.md)
- Shared namespaces: [`reference/pulumi/namespaces.md`](reference/pulumi/namespaces.md)
