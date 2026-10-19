# Backend Service Deploy

Production procedure for a modware gRPC service on any cluster in this repo.
Run the recipes in a `cluster-env` shell of the target cluster. The recipes
read the cluster identity from the shell (`CLUSTER_NAME`, `CLUSTER_ENV`,
`PULUMI_STACK`).

The flow is composite recipes. The individual recipes behind them are
available for single-service runs and diagnostics — [section 6](#6-individual-recipes).

## Table of Contents
- [Quick Reference](#quick-reference)
- [1. Create the deployer key](#1-create-the-deployer-key)
- [2. Prepare the composite deploy workflow](#2-prepare-the-composite-deploy-workflow)
- [3. Deploy all backend services](#3-deploy-all-backend-services)
- [4. Push the stack files](#4-push-the-stack-files)
- [5. Open workflow pull requests](#5-open-workflow-pull-requests)
- [6. Individual recipes](#6-individual-recipes)
  - [6.1 Verify the cluster registry](#61-verify-the-cluster-registry)
  - [6.2 Scaffold the stack config](#62-scaffold-the-stack-config)
  - [6.3 Check cluster prerequisites](#63-check-cluster-prerequisites)
  - [6.4 First deploy](#64-first-deploy)
  - [6.5 Publish CI credentials and variables](#65-publish-ci-credentials-and-variables)
  - [6.6 Render the tag workflow](#66-render-the-tag-workflow)
- [7. Verify](#7-verify)
- [8. Troubleshooting](#8-troubleshooting)
- [9. Related Documents](#9-related-documents)

## Quick Reference

```bash
# once per cluster:
just ci create-deploy-key                       # 1. SA + key + preflight (idempotent)
just ci render-composite-deploy                 # 2. PROD_PULUMI_STATE_STORAGE + composite-deploy.yaml render
# every service run — scaffold → gate → deploy → vars → render:
just ci deploy-backend-services                 # 3. all services in config/services.yaml
git add modware-*/Pulumi.dcr-kube1.yaml && git commit && git push   # 4. stack files BEFORE any tag
just ci pr-deploy-workflows                     # 5. open PRs in dictyBase/workflows + service repos
# then: merge the PRs and cut tags
```

Single-service or step-by-step runs — [section 6](#6-individual-recipes):

```bash
just ci deploy-backend-services --services order   # one service only
```

## 1. Create the deployer key

The recipe creates the deployer service account and its key at the standard
path `credentials/<cluster>/deployer.json`, then verifies the key with
`check-deploy-credentials`. An existing key file stays in use — the recipe does
not create a second key.
→ [Deploy credential details](reference/backend/ci-credentials.md)

```bash
just ci create-deploy-key
```

## 2. Prepare the composite deploy workflow

The recipe publishes the org variable `PROD_PULUMI_STATE_STORAGE` from the
cluster registry and renders `bin/ci-render/composite-deploy.yaml` so
`dictyBase/workflows` routes `PROD_DEPLOY_SA_KEY` and `PROD_PULUMI_STATE_STORAGE`
when `environment: production` is passed.
→ [Workflow render details](reference/backend/render-deploy-workflows.md)

```bash
just ci render-composite-deploy
```

## 3. Deploy all backend services

**WARNING: Push cluster-ops `develop` before any service tag.** CI deploys
read cluster-ops from `develop`. An unpushed stack file deploys nothing.

The recipe runs the full chain per service. The services come from
`config/services.yaml`. The recipe is idempotent — a re-run completes the
missing steps only.
→ [Aggregate deploy details](reference/backend/deploy-services.md)

```bash
just ci deploy-backend-services
```

## 4. Push the stack files

The stack configs must live on cluster-ops `develop` — CI deploys read them
from there at deploy time.
→ [First deploy details](reference/backend/bootstrap-service.md)

```bash
git add modware-*/Pulumi.dcr-kube1.yaml && git commit -m "stacks for <cluster>" && git push
```

## 5. Open workflow pull requests

The recipe clones `dictyBase/workflows` and each service repository in a
temporary directory, applies the rendered workflow files from `bin/ci-render/`
(removing obsolete `staging-build.yaml`), pushes the branches, and opens pull
requests ready to merge.
→ [Workflow render details](reference/backend/render-deploy-workflows.md)

```bash
just ci pr-deploy-workflows
```

## 6. Individual recipes

Use these recipes for single-service runs and for diagnostics. Each recipe
also runs alone.

### 6.1 Verify the cluster registry

Later steps read the registry entry. The `register-cluster` recipe creates
entries from bootstrap artifacts.
→ [Registry details](reference/backend/cluster-registry.md)

```bash
just gcp-cluster registry-show
```

### 6.2 Scaffold the stack config

The recipe creates `<folder>/Pulumi.<stack>.yaml` from the template and
initializes the stack in the Pulumi backend. The recipe stops with an error
when the file already exists.
→ [Scaffold details](reference/backend/scaffold-backend-stack.md)

```bash
just gcp-pulumi scaffold-backend-stack --folder modware-order
```

### 6.3 Check cluster prerequisites

The recipe does a read-only probe of these items: the namespace, the ArangoDB
credentials Secret, and the application database. The database probe goes
through the Service that the app connects to. The recipe exits non-zero and
lists each missing prerequisite.
→ [Prerequisites details](reference/backend/prerequisites.md)

```bash
just gcp-pulumi check-backend-prereqs --folder modware-order
```

### 6.4 First deploy

The recipe runs these steps in a fixed order: prereq gate → ensure-stack →
preview → update → rollout wait + image check. Prerequisites come first — the
recipe touches nothing when a prerequisite fails. The recipe is idempotent —
when the deployment runs the wanted tag, it checks the rollout only.
→ [First deploy details](reference/backend/bootstrap-service.md)

```bash
just gcp-pulumi bootstrap-service --folder modware-order
```

### 6.5 Publish CI credentials and variables

The recipes do these steps: key preflight, org-secret publish, and the
`<PROD>_*` repo variables.
→ [CI credentials details](reference/backend/ci-credentials.md)
→ [CI variables details](reference/backend/ci-variables.md)

```bash
just ci check-deploy-credentials
```

### 6.6 Render the tag workflow

The recipe makes the complete `tag-build.yaml` — test, lint, then the
composite deploy — and removes the dead `staging-build.yaml` in the same PR.
→ [Workflow render details](reference/backend/render-deploy-workflows.md)

```bash
just ci render-tag-deploy --app order --project modware-order --out tag-build.yaml
```

## 7. Verify

After the first tag deploy, the running image must match the tag. The service
must answer inside the cluster.
→ [Verification details](reference/backend/bootstrap-service.md#verification)

```bash
kubectl -n prod get deploy order-api-server -o jsonpath='{.spec.template.spec.containers[0].image}'
```

## 8. Troubleshooting

→ [Troubleshooting details](reference/backend/troubleshooting.md)

## 9. Related Documents

- [Aggregate deploy details](reference/backend/deploy-services.md)
- [Upstream deploy refactor plan](plans/upstream-deploy-refactor.md)
- [Modware service plan](plans/modware-service-any-cluster.md)
- [ArangoDB deploy guide](arangodb-deploy.md)
- [kOps cluster setup](kops-setup.md)