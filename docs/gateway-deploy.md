# Gateway Deploy

The `gateway/` Pulumi project deploys the shared **Cilium Gateway** (`dictycr-gateway`), the **cert-manager** ACME `Issuer` (`letsencrypt-issuer`), and the `HTTPRoute` resources for `frontend_application`, `graphql_server`, `minio`, `log-to`, and `dcr-mcp` into one namespace.

**Each service route has its own `enabled` toggle.** By default, the scaffold template enables `frontend`, `graphql`, and `minio`, and disables `logto` ([Section 7](#7-configure-the-logto-route-optional)) and `mcp` ([Section 8](#8-configure-the-mcp-route-optional)). Legacy `nginx` Ingress resources remain on `dcr-experiments` only.

**Status**: Production procedure. Run `just gcp-pulumi scaffold-gateway-stack` to create a new cluster stack file from the registry and template, or `just gcp-pulumi ensure-stack --folder gateway` when the stack file already exists.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Install Gateway API and Enable Cilium](#1-install-gateway-api-and-enable-cilium)
  - [1.1 New Cluster Setup](#11-new-cluster-setup)
  - [1.2 Existing Cluster Migration](#12-existing-cluster-migration)
- [2. Scaffold or Select the Stack](#2-scaffold-or-select-the-stack)
- [3. Configure the ACME Issuer](#3-configure-the-acme-issuer)
- [4. Configure the Frontend Route](#4-configure-the-frontend-route)
- [5. Configure the GraphQL Route](#5-configure-the-graphql-route)
- [6. Configure the MinIO Route](#6-configure-the-minio-route)
- [7. Configure the Logto Route (Optional)](#7-configure-the-logto-route-optional)
- [8. Configure the MCP Route (Optional)](#8-configure-the-mcp-route-optional)
- [9. Preview and Deploy](#9-preview-and-deploy)
- [10. Point DNS A Records to the Gateway IP](#10-point-dns-a-records-to-the-gateway-ip)
- [11. Push the Stack File](#11-push-the-stack-file)
- [12. Verify the Gateway](#12-verify-the-gateway)
- [13. Troubleshooting](#13-troubleshooting)
- [14. Related Documents](#14-related-documents)

---

## Quick Reference

For experienced users. Full details in the sections below.

```bash
# Enter the cluster environment first
just cluster-env --env <env> --cluster <cluster>

# 1. New cluster: create cluster, install CRDs, then restart Cilium
just gcp-cluster create-cluster
just gateway install-crds
just gateway restart-cilium

# 1. Existing cluster: install CRDs, apply cluster manifest, then restart Cilium
just gateway install-crds
just gcp-cluster apply-cluster
just gateway restart-cilium

# 2. Scaffold the stack file on a new cluster, or select an existing stack
just gcp-pulumi scaffold-gateway-stack
just gcp-pulumi ensure-stack --folder gateway

# 3. Set the Let's Encrypt ACME contact email
just gcp-pulumi set-config --folder gateway --key properties.issuer.email --value <acme-email>

# 4. Configure the frontend route (default: enabled, e.g. slime.dictycr.org)
just gcp-pulumi set-config --folder gateway --key properties.routes.frontend.hosts.0 --value <frontend-host>

# 5. Configure the GraphQL route (default: enabled, e.g. graphql.dictycr.org)
just gcp-pulumi set-config --folder gateway --key properties.routes.graphql.hosts.0 --value <graphql-host>

# 6. Configure the MinIO route (default: enabled on its own subdomain, e.g. slimestorage.dictycr.org)
just gcp-pulumi set-config --folder gateway --key properties.routes.minio.hosts.0 --value <minio-host>

# 7. Optional. Enable and configure the Logto route (default: false)
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.hosts.0 --value <logto-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.enabled --value true

# 8. Optional. Enable and configure the MCP route (default: false)
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value true

# 9. Deploy the Gateway and wait for its external LoadBalancer IP
just gateway deploy

# 10. Point DNS A records for enabled hostnames to this IP
just gateway show-ip

# 11. Commit and push the stack file
git add "gateway/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "gateway: stack config" && git push

# 12. Verify the Gateway, HTTPRoutes, and TLS Secrets after DNS resolves
just gateway verify
```

---

**All of the commands below run in the cluster-env sub-shell.**

- Enter the sub-shell with `just cluster-env --env <env> --cluster <cluster>`. The sub-shell reads `.env.<env>.<cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PROJECT_ID`, and `KUBECONFIG`.
- Each recipe defaults `--stack` to `$PULUMI_STACK` and `--namespace` to `prod`. No command below needs those flags.
- Outside the sub-shell the recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Install Gateway API and Enable Cilium

![Cilium Gateway API Workflow](reference/gateway/gateway-setup-workflow.png)

Cilium creates `GatewayClass` `cilium` only when the Gateway API CRDs exist in the API server and the Cilium operator runs with Gateway API enabled. Choose the sequence below that matches your cluster condition.
→ [Command sequences and dependencies](reference/gateway/deployment.md#command-sequences-and-dependencies) · [`just gateway install-crds`](reference/gateway/deployment.md#just-gateway-install-crds) · [`just gateway restart-cilium`](reference/gateway/deployment.md#just-gateway-restart-cilium)

### 1.1 New Cluster Setup

On a new cluster, enable `spec.networking.cilium.gatewayAPI.enabled: true` in `config/kops/<cluster>/cluster.yaml`. Create the cluster, install the CRDs, and restart Cilium so the operator and node agents bind the new CRDs.
→ [Prerequisites](reference/gateway/deployment.md#prerequisites)

```bash
just gcp-cluster create-cluster
just gateway install-crds
just gateway restart-cilium
```

### 1.2 Existing Cluster Migration

On an existing cluster, install the Gateway API CRDs into the API server first. Then reconcile the cluster and restart the Cilium operator to register `GatewayClass` `cilium`.
→ [Command sequences and dependencies](reference/gateway/deployment.md#command-sequences-and-dependencies)

```bash
# 1. Install CRDs first (API server must have CRDs before Cilium activates)
just gateway install-crds

# 2. Reconcile kOps manifest and update cilium-config
just gcp-cluster apply-cluster

# 3. Restart Cilium operator to bind CRDs and verify GatewayClass cilium
just gateway restart-cilium
```

---

## 2. Scaffold or Select the Stack

On a new cluster, run `scaffold-gateway-stack` to initialize the stack with the cluster KMS key and render `gateway/Pulumi.${PULUMI_STACK}.yaml` from `config/templates/gateway-stack.yaml.tmpl`. When the stack file already exists, run `ensure-stack` to select the stack.
→ [Create the stack](reference/gateway/deployment.md#create-the-stack)

```bash
# New cluster (creates the stack and writes gateway/Pulumi.${PULUMI_STACK}.yaml)
just gcp-pulumi scaffold-gateway-stack

# Existing stack file
just gcp-pulumi ensure-stack --folder gateway
```

---

## 3. Configure the ACME Issuer

Set the contact email for the Let's Encrypt ACME `Issuer` (`letsencrypt-issuer`). `just gateway check` rejects an empty value or the `<acme-email>` placeholder.
→ [Configure the ACME Issuer](reference/gateway/deployment.md#configure-the-acme-issuer)

```bash
just gcp-pulumi set-config --folder gateway --key properties.issuer.email --value <acme-email>
```

---

## 4. Configure the Frontend Route

The key `properties.routes.frontend.enabled` defaults to `true`. Set the public hostname for the four frontend Services (`frontpage-api`, `stockcenter-api`, `publication-api`, `genomepage-api`), or set `enabled` to `false` if `frontend_application` is not deployed yet.
→ [Frontend route](reference/gateway/deployment.md#frontend-route)

```bash
# Set the public hostname (route is enabled by default)
just gcp-pulumi set-config --folder gateway --key properties.routes.frontend.hosts.0 --value <frontend-host>

# Or disable the route until frontend_application is deployed
just gcp-pulumi set-config --folder gateway --key properties.routes.frontend.enabled --value false
```

---

## 5. Configure the GraphQL Route

The key `properties.routes.graphql.enabled` defaults to `true`, and `properties.routes.graphql.path` defaults to `/` so the route serves both the GraphiQL playground at `/` and queries at `/graphql`. Set `hosts.0` (`hosts[0]`) to the public GraphQL hostname (for example, `graphql.dictycr.org`), or set `enabled` to `false` if `graphql_server` is not deployed yet.
→ [GraphQL route](reference/gateway/deployment.md#graphql-route)

```bash
# Set the public hostname (path / serves both / and /graphql)
just gcp-pulumi set-config --folder gateway --key properties.routes.graphql.hosts.0 --value <graphql-host>

# Or disable the route until graphql_server is deployed
just gcp-pulumi set-config --folder gateway --key properties.routes.graphql.enabled --value false
```

---

## 6. Configure the MinIO Route

The key `properties.routes.minio.enabled` defaults to `true`. Set the public S3 hostname for Service `minio` (`:9000`), or set `enabled` to `false` to keep MinIO internal only.
→ [MinIO route](reference/gateway/deployment.md#minio-route)

```bash
# Set the public hostname (route is enabled by default)
just gcp-pulumi set-config --folder gateway --key properties.routes.minio.hosts.0 --value <minio-host>

# Or disable the route to keep MinIO cluster-internal
just gcp-pulumi set-config --folder gateway --key properties.routes.minio.enabled --value false
```

---

## 7. Configure the Logto Route (Optional)

**Optional. The default is off. Skip this section while Logto is not deployed.**

The key `properties.routes.logto.enabled` defaults to `false`. To expose `logto-api` (`:3001`) through the Gateway after you deploy Logto, set `hosts.0` (`hosts[0]`) first and then set `enabled` to `true`.
→ [Logto route](reference/gateway/deployment.md#logto-route)

```bash
# Keep the Logto route off (default)
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.enabled --value false

# Turn the Logto route on. Set the hostname first.
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.hosts.0 --value <logto-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.enabled --value true
```

---

## 8. Configure the MCP Route (Optional)

**Optional. The default is off. Skip this section while dcr-mcp is not deployed.**

The key `properties.routes.mcp.enabled` defaults to `false`. To expose `dcr-mcp-api` (`:8080`) on its own root hostname (`path: /`) through the Gateway after you deploy `dcr-mcp`, set `hosts.0` (`hosts[0]`) first and then set `enabled` to `true`.
→ [MCP route](reference/gateway/deployment.md#mcp-route)

```bash
# Keep the MCP route off (default)
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value false

# Turn the MCP route on. Set the hostname first.
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value true
```

---

## 9. Preview and Deploy

Run `just gcp-pulumi preview --folder gateway` to review the plan first, then run `just gateway deploy`. The recipe runs `check` → `ensure-stack` → `preview` → `create-resource`, waits for Cilium to assign the external LoadBalancer IP address to `dictycr-gateway`, and prints that IP address.
→ [`just gateway deploy`](reference/gateway/deployment.md#just-gateway-deploy)

```bash
just gcp-pulumi preview --folder gateway
just gateway deploy
```

---

## 10. Point DNS A Records to the Gateway IP

Print the external IP address of `dictycr-gateway` and create DNS `A` records pointing each enabled route hostname (`<frontend-host>`, `<graphql-host>`, `<minio-host>`, `<logto-host>`, `<mcp-host>`) to that IP address. After DNS resolves, `cert-manager` completes the Let's Encrypt HTTP-01 challenge on port `80` and writes each route's TLS Secret.
→ [`just gateway show-ip`](reference/gateway/deployment.md#just-gateway-show-ip) · [TLS certificate issuance](reference/gateway/deployment.md#tls-certificate-issuance)

```bash
just gateway show-ip
```

---

## 11. Push the Stack File

Commit and push `gateway/Pulumi.${PULUMI_STACK}.yaml` so Git keeps the canonical routing and hostname configuration for the cluster.
→ [Create the stack](reference/gateway/deployment.md#create-the-stack)

```bash
git add "gateway/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "gateway: stack config" && git push
```

---

## 12. Verify the Gateway

After your DNS `A` records resolve to the Gateway IP address, run `just gateway verify` to confirm that `Issuer` `letsencrypt-issuer` is `Ready`, `Gateway` `dictycr-gateway` is `Programmed`, and every enabled route has an `Accepted` `HTTPRoute` and a `kubernetes.io/tls` Secret.
→ [Verification](reference/gateway/deployment.md#verification)

```bash
just gateway verify
```

---

## 13. Troubleshooting

→ [Troubleshooting table](reference/gateway/deployment.md#troubleshooting)

---

## 14. Related Documents

**Reference details for this guide:**
- [Gateway deployment details](reference/gateway/deployment.md)

**Other documentation:**
- Cluster setup and day-2 operations: [`kops-setup.md`](kops-setup.md)
- Pulumi backend and namespaces: [`pulumi-setup.md`](pulumi-setup.md)
- GraphQL server deployment: [`graphql-deploy.md`](graphql-deploy.md)
- MCP server deployment: [`dcr-mcp-deploy.md`](dcr-mcp-deploy.md)
- MinIO deployment: [`minio-deploy.md`](minio-deploy.md)
- Logto deployment: [`logto-deploy.md`](logto-deploy.md)
