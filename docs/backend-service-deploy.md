# Backend Service Deploy

Production procedure for a modware gRPC service on any cluster in this repo.
Run the recipes in a `cluster-env` shell of the target cluster. The recipes
read the cluster identity from the shell (`CLUSTER_NAME`, `CLUSTER_ENV`,
`PULUMI_STACK`).

The flow is two composite recipes. The individual recipes behind them are
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

The recipe creates the deployer service account and its key at the standard
path `credentials/<cluster>/deployer.json`. An existing key file stays in use —
the recipe does not create a second key.
→ [Deploy credential details](reference/backend/ci-credentials.md)

```bash
just ci create-deploy-key
```

## 2. Deploy all backend services

**WARNING: Push cluster-ops `develop` before any service tag.** CI deploys
read cluster-ops from `develop`. An unpushed stack file deploys nothing.

The recipe runs the full chain per service. The services come from
`config/services.yaml`. The recipe is idempotent — a re-run completes the
missing steps only.
→ [Aggregate deploy details](reference/backend/deploy-services.md)

```bash
just ci deploy-backend-services
```

## 3. Push the stack files

The stack configs must live on cluster-ops `develop` — CI deploys read them
from there at deploy time.
→ [First deploy details](reference/backend/bootstrap-service.md)

```bash
git add modware-*/Pulumi.dcr-kube1.yaml && git commit -m "stacks for <cluster>" && git push
```

## 4. Individual recipes

Use these recipes for single-service runs and for diagnostics. Each recipe
also runs alone.

### 4.1 Verify the cluster registry

Later steps read the registry entry. The `register-cluster` recipe creates
entries from bootstrap artifacts.
→ [Registry details](reference/backend/cluster-registry.md)

```bash
just gcp-cluster registry-show
```

### 4.2 Scaffold the stack config

The recipe creates `<folder>/Pulumi.<stack>.yaml` from the template and
initializes the stack in the Pulumi backend. The recipe stops with an error
when the file already exists.
→ [Scaffold details](reference/backend/scaffold-backend-stack.md)

```bash
just gcp-pulumi scaffold-backend-stack --folder modware-order
```

### 4.3 Check cluster prerequisites

The recipe does a read-only probe of these items: the namespace, the ArangoDB
credentials Secret, and the application database. The database probe goes
through the Service that the app connects to. The recipe exits non-zero and
lists each missing prerequisite.
→ [Prerequisites details](reference/backend/prerequisites.md)

```bash
just gcp-pulumi check-backend-prereqs --folder modware-order
```

### 4.4 First deploy

The recipe runs these steps in a fixed order: prereq gate → ensure-stack →
preview → update → rollout wait + image check. Prerequisites come first — the
recipe touches nothing when a prerequisite fails. The recipe is idempotent —
when the deployment runs the wanted tag, it checks the rollout only.
→ [First deploy details](reference/backend/bootstrap-service.md)

```bash
just gcp-pulumi bootstrap-service --folder modware-order
```

### 4.5 Publish CI credentials and variables

The recipes do these steps: key preflight, org-secret publish, and the
`<PROD>_*` repo variables.
→ [CI credentials details](reference/backend/ci-credentials.md)
→ [CI variables details](reference/backend/ci-variables.md)

```bash
just ci check-deploy-credentials --sa-key credentials/dcr-kube1/deployer.json
```

### 4.6 Render the tag workflow

The recipe makes the complete `tag-build.yaml` — test, lint, then the
composite deploy — and removes the dead `staging-build.yaml` in the same PR.
→ [Workflow render details](reference/backend/render-deploy-workflows.md)

```bash
just ci render-tag-deploy --app order --project modware-order --out tag-build.yaml
```

## 5. Verify

After the first tag deploy, the running image must match the tag. The service
must answer inside the cluster.
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