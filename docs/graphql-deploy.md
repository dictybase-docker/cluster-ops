# GraphQL Server Deploy

Deployment guide for **graphql-server** (`dictybase/graphql-server`) via the `graphql_server/` Pulumi project: Deployment `graphql-api-server`, NodePort Service `graphql-api`, and public TLS Ingress `graphql-ingress`, fed by Redis, MinIO, and five in-cluster gRPC Services.

**Status**: Production procedure. The program reads `graphql_server/Pulumi.$PULUMI_STACK.yaml` — the repository ships only the lab `Pulumi.experiments.yaml` (namespace `dev`) and a retired `Pulumi.dev.yaml` the program no longer reads, so write the stack file for any other cluster before `ensure-stack`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Verify Upstream Services](#1-verify-upstream-services)
- [2. Configure the Stack](#2-configure-the-stack)
- [3. Select the Authentication Mode](#3-select-the-authentication-mode)
- [4. Encrypt the MinIO Secret](#4-encrypt-the-minio-secret)
- [5. Preview and Deploy](#5-preview-and-deploy)
- [6. Push the Stack File](#6-push-the-stack-file)
- [7. Verify](#7-verify)
- [8. Wire Production Tag Deploys](#8-wire-production-tag-deploys)
- [9. Troubleshooting](#9-troubleshooting)
- [10. Related Documents](#10-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# Enter cluster environment first
just cluster-env --env <env> --cluster <cluster>

# 1. Verify the upstream datastores (the five gRPC Services must exist too)
just redis verify
just minio verify

# 2. Write graphql_server/Pulumi.<stack>.yaml, then select the stack
just gcp-pulumi ensure-stack --folder graphql_server

# 3. Pick the authentication mode (false is the default when the key is absent)
just gcp-pulumi set-config --folder graphql_server --key properties.authEnabled --value false

# 4. Encrypt the MinIO Secret pointer
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.minio.name

# 5. Read the plan, then apply
just gcp-pulumi preview --folder graphql_server
just gcp-pulumi create-resource --folder graphql_server

# 6. Push the stack file before any graphql-server merge or tag
git add "graphql_server/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "graphql_server: stack config" && git push

# 7. Verify the rollout (the cluster-env shell sets no default namespace)
kubectl -n <namespace> rollout status deployment/graphql-api-server

# 8. One-time: make release tags deploy (then PR the rendered file by hand)
just ci set-deploy-secret --repos dictyBase/graphql-server
just ci sync-deploy-vars --repos dictyBase/graphql-server
just ci render-tag-deploy --app graphql-server --project graphql_server --out bin/ci-render/graphql-server-tag-build.yaml
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env <env> --cluster <cluster>`. The sub-shell sources `.env.<env>.<cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Recipes default `--stack` to `$PULUMI_STACK`, so no recipe below needs the flag.
- Outside the sub-shell recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Verify Upstream Services

Redis, MinIO, and the five modware gRPC Services must already exist in the namespace — Kubernetes injects their `*_SERVICE_HOST` variables only into pods created after them, and the container exits when one is missing.
→ [Prerequisites](reference/graphql/deployment.md#prerequisites) · [service discovery](reference/graphql/deployment.md#in-cluster-service-discovery)

```bash
just redis verify
just minio verify
```

---

## 2. Configure the Stack

`ensure-stack` fails closed unless `graphql_server/Pulumi.${PULUMI_STACK}.yaml` exists — write it first from the `Pulumi.experiments.yaml` shape.
→ [Stack configuration](reference/graphql/deployment.md#stack-configuration)

```bash
just gcp-pulumi ensure-stack --folder graphql_server
```

---

## 3. Select the Authentication Mode

`properties.authEnabled` defaults to **`false`** when the key is absent — the container runs with `--auth-enabled=false`, no JWT middleware, and no Logto dependency. `true` additionally requires `properties.endpoints.auth` and all five `properties.secrets.auth.*` values; set them **before** flipping the flag, because `preview` then fails closed with `authEnabled=true but secrets.auth is missing ...`.
→ [Authentication modes](reference/graphql/deployment.md#authentication-modes)

```bash
# Without auth (default)
just gcp-pulumi set-config --folder graphql_server --key properties.authEnabled --value false

# With auth: endpoint, then the five Logto secrets at a hidden prompt, flag last
just gcp-pulumi set-config --folder graphql_server --key properties.endpoints.auth --value <logto-url>
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.appId
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.appSecret
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.jwksURI
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.jwtIssuer
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.jwtAudience
just gcp-pulumi set-config --folder graphql_server --key properties.authEnabled --value true
```

---

## 4. Encrypt the MinIO Secret

Store the pointer to the MinIO credentials Secret created by the `minio` stack; `userKey` and `passKey` are plain config. Omit `--value` to enter the value at a hidden prompt.
→ [Secrets and MinIO wiring](reference/graphql/deployment.md#secrets-and-minio-wiring)

```bash
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.minio.name
```

---

## 5. Preview and Deploy

Read the plan, then apply it — the apply is non-interactive and aborts by itself on a protected resource. Re-run both for upgrades after changing `properties.image.tag`.
→ [Deploy behavior](reference/graphql/deployment.md#deploy-behavior)

```bash
just gcp-pulumi preview --folder graphql_server
just gcp-pulumi create-resource --folder graphql_server
```

---

## 6. Push the Stack File

**WARNING: Push cluster-ops `develop` before merging or tagging `dictyBase/graphql-server`.** CI clones cluster-ops at `develop`, so an unpushed stack file deploys nothing.
→ [CI deployment details](reference/graphql/deployment.md#github-actions-ci-deployment)

```bash
git add "graphql_server/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "graphql_server: stack config" && git push
```

---

## 7. Verify

No recipe covers this project — check the rollout with `kubectl`. The sub-shell sets no default namespace, so `-n <namespace>` (the stack's `properties.namespace`) is required.
→ [Verification details](reference/graphql/deployment.md#verification)

```bash
kubectl -n <namespace> rollout status deployment/graphql-api-server
```

---

## 8. Wire Production Tag Deploys

**One-time per cluster. Skip while production deploys stay manual.**

Grants `PROD_DEPLOY_SA_KEY` visibility to `dictyBase/graphql-server`, publishes the `<CI_ENV>_*` repo variables, and renders `tag-build.yaml` for the repo — which stays inert until it is PR'd in as `.github/workflows/tag-build.yaml`.
→ [Tag deploy wiring](reference/graphql/deployment.md#wiring-production-tag-deploys)

```bash
just ci set-deploy-secret --repos dictyBase/graphql-server
just ci sync-deploy-vars --repos dictyBase/graphql-server
just ci render-tag-deploy --app graphql-server --project graphql_server --out bin/ci-render/graphql-server-tag-build.yaml
```

---

## 9. Troubleshooting

→ [Troubleshooting table](reference/graphql/deployment.md#troubleshooting)

---

## 10. Related Documents

**Reference details for this guide:**
- [GraphQL server deployment details](reference/graphql/deployment.md)

**Other documentation:**
- gRPC backends behind this server: [`backend-service-deploy.md`](backend-service-deploy.md)
- Redis: [`redis-deploy.md`](redis-deploy.md)
- MinIO and the asset bucket: [`minio-deploy.md`](minio-deploy.md)
- Logto (only when `authEnabled: true`): [`logto-deploy.md`](logto-deploy.md)
- Shared namespaces: [`reference/pulumi/namespaces.md`](reference/pulumi/namespaces.md)
