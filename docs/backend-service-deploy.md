# Backend Service Deploy

Production procedure for a modware gRPC service on any cluster in this repo.
Inside a `cluster-env` shell of the target cluster; all recipes default their
identity from it (`CLUSTER_NAME`, `CLUSTER_ENV`, `PULUMI_STACK`).

The flow is two composite recipes. The individual recipes behind them stay
available for single-service runs and diagnostics — [section 4](#4-individual-recipes).

## Table of Contents
- [Quick Reference](#quick-reference)
- [1. Create the deployer key](#1-create-the-deployer-key)
- [2. Deploy all backend services](#2-deploy-all-backend-services)
- [3. Push the stack files](#3-push-the-stack-files)
- [4. Individual recipes](#4-individual-recipes)
  - [4.1 Verify the cluster registry](#41-verify-the-cluster-registry)
  - [4.2 Scaffold the stack config](#42-scaffold-the-stack-config)
  - [4.3 Check cluster prerequisites](#43-check-cluster-prerequisites)
  - [4.4 First deploy](#44-first-deploy)
  - [4.5 Publish CI credentials and variables](#45-publish-ci-credentials-and-variables)
  - [4.6 Render the tag workflow](#46-render-the-tag-workflow)
- [5. Verify](#5-verify)
- [6. Troubleshooting](#6-troubleshooting)
- [7. Related Documents](#7-related-documents)

## Quick Reference

```bash
# once per cluster:
just ci create-deploy-key                       # 1. SA + key (idempotent)
# every service run — scaffold → gate → deploy → vars → render:
just ci deploy-backend-services                 # 2. all services in config/services.yaml
git add modware-*/Pulumi.dcr-kube1.yaml && git commit && git push   # 3. stack files BEFORE any tag
# then: PR bin/ci-render/<folder>-tag-build.yaml into each service repo, merge, cut tags
```

Single-service or step-by-step runs — [section 4](#4-individual-recipes):

```bash
just ci deploy-backend-services --services order   # one service only
```

## 1. Create the deployer key

Mints the deployer service account and its key at
`credentials/<cluster>/deployer.json`. Reuses an existing key file — never
re-mints.
→ [Deploy credential details](reference/backend/ci-credentials.md)

```bash
just ci create-deploy-key
```

## 2. Deploy all backend services

**Push cluster-ops `develop` before any service tag** — CI deploys clone
`develop`, so an unpushed stack file deploys nothing.

Runs the full chain per service from `config/services.yaml`. Idempotent —
re-running converges.
→ [Aggregate deploy details](reference/backend/deploy-services.md)

```bash
just ci deploy-backend-services
```

## 3. Push the stack files

Records every scaffolded stack config on cluster-ops `develop` — CI deploys
read them from there at deploy time.
→ [First deploy details](reference/backend/bootstrap-service.md)

```bash
git add modware-*/Pulumi.dcr-kube1.yaml && git commit -m "stacks for <cluster>" && git push
```

## 4. Individual recipes

Fallback and diagnostic surface — each recipe also runs standalone.

### 4.1 Verify the cluster registry

Every later step reads the registry entry; `register-cluster` creates entries from bootstrap artifacts.
→ [Registry details](reference/backend/cluster-registry.md)

```bash
just gcp-cluster registry-show
```

### 4.2 Scaffold the stack config

Creates `modware-order/Pulumi.<stack>.yaml` from the template and initializes the stack in the Pulumi backend. Fails when the file already exists.
→ [Scaffold details](reference/backend/scaffold-backend-stack.md)

```bash
just gcp-pulumi scaffold-backend-stack --folder modware-order
```

### 4.3 Check cluster prerequisites

One composite read-only probe: namespace, ArangoDB credentials Secret, and the application database — through the Service the app connects to. It exits non-zero listing every missing prerequisite.
→ [Prerequisites details](reference/backend/prerequisites.md)

```bash
just gcp-pulumi check-backend-prereqs --folder modware-order
```

### 4.4 First deploy

**Preflight-gated composite:** prereqs → ensure-stack → preview → update → rollout + image check. The stack file commit and push stay manual — CI deploys clone cluster-ops `develop`, so an unpushed stack file deploys nothing. Idempotent: verify-only when the tag already runs.
→ [First deploy details](reference/backend/bootstrap-service.md)

```bash
just gcp-pulumi bootstrap-service --folder modware-order
```

### 4.5 Publish CI credentials and variables

Key preflight, org-secret publish, then the `<PROD>_*` repo variables.
→ [CI credentials details](reference/backend/ci-credentials.md)
→ [CI variables details](reference/backend/ci-variables.md)

```bash
just ci check-deploy-credentials --sa-key credentials/dcr-kube1/deployer.json
```

### 4.6 Render the tag workflow

Renders the complete `tag-build.yaml` — test, lint, then the composite deploy — plus the deletion of the dead `staging-build.yaml`.
→ [Workflow render details](reference/backend/render-deploy-workflows.md)

```bash
just ci render-tag-deploy --app order --project modware-order --out tag-build.yaml
```

## 5. Verify

After the first tag deploy: running image matches the tag, and the service answers inside the cluster.
→ [Verification details](reference/backend/bootstrap-service.md#verification)

```bash
kubectl -n prod get deploy order-api-server -o jsonpath='{.spec.template.spec.containers[0].image}'
```

## 6. Troubleshooting

→ [Troubleshooting details](reference/backend/troubleshooting.md)

## 7. Related Documents

- [Aggregate deploy details](reference/backend/deploy-services.md)
- [Upstream deploy refactor plan](plans/upstream-deploy-refactor.md)
- [Modware service plan](plans/modware-service-any-cluster.md)
- [ArangoDB deploy guide](arangodb-deploy.md)
- [kOps cluster setup](kops-setup.md)