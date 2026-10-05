# Backend Service Deploy

Production procedure for a modware gRPC service on any cluster in this repo.
Inside a `cluster-env` shell of the target cluster; all recipes default their
identity from it.

## Table of Contents
- [Quick Reference](#quick-reference)
- [1. Verify the cluster registry](#1-verify-the-cluster-registry)
- [2. Scaffold the stack config](#2-scaffold-the-stack-config)
- [3. Check cluster prerequisites](#3-check-cluster-prerequisites)
- [4. First deploy](#4-first-deploy)
- [5. Publish CI credentials and variables](#5-publish-ci-credentials-and-variables)
- [6. Render the tag workflow](#6-render-the-tag-workflow)
- [7. Verify](#7-verify)
- [8. Troubleshooting](#8-troubleshooting)
- [9. Related Documents](#9-related-documents)

## Quick Reference

```bash
# Add a service to an existing cluster (dcr-kube1 example):
just gcp-cluster registry-show --cluster dcr-kube1          # 1. registry sanity
just gcp-pulumi scaffold-backend-stack --stack dcr-kube1 \
  --folder modware-order --port 9250                         # 2. stack config
just gcp-pulumi check-backend-prereqs --stack dcr-kube1 \
  --folder modware-order                                     # 3. prereq gate
just gcp-pulumi bootstrap-service --stack dcr-kube1 \
  --folder modware-order --image-tag <published-tag>        # 4. first deploy
git add modware-order/Pulumi.dcr-kube1.yaml && git commit -m "…" && git push  # 4b. push BEFORE any tag
just ci check-deploy-credentials --cluster dcr-kube1 --sa-key <path>  # 5a. key preflight
just ci set-deploy-secret --cluster dcr-kube1 --sa-key <path> \
  --repos dictyBase/modware-order                           # 5b. publish key
just ci sync-deploy-vars --cluster dcr-kube1 \
  --repos dictyBase/modware-order                           # 5c. vars
just ci render-tag-deploy --stack dcr-kube1 --app order \
  --project modware-order --out tag-build.yaml              # 6. workflow file
# 7. open the PR in the service repo, merge, push a tag
```

Optional steps:

```bash
just gcp-cluster verify-deployer-access --cluster dcr-kube1 --sa-key <path>  # 3b. deeper probe
```

## 1. Verify the cluster registry

Every later step reads the registry entry; confirm it resolves and prints all keys.
→ [Registry details](reference/backend/cluster-registry.md)

```bash
just gcp-cluster registry-show --cluster dcr-kube1
```

## 2. Scaffold the stack config

Creates `modware-order/Pulumi.<stack>.yaml` from the template and initializes the stack in the Pulumi backend. Fails when the file already exists.
→ [Scaffold details](reference/backend/scaffold-backend-stack.md)

```bash
just gcp-pulumi scaffold-backend-stack --stack dcr-kube1 --folder modware-order --port 9250
```

## 3. Check cluster prerequisites

One composite read-only probe: namespace, ArangoDB credentials Secret, and the application database. It exits non-zero listing every missing prerequisite.
→ [Prerequisites details](reference/backend/prerequisites.md)

```bash
just gcp-pulumi check-backend-prereqs --stack dcr-kube1 --folder modware-order
```

## 4. First deploy

**Preflight-gated composite:** prereqs → ensure-stack → preview → update → rollout + image check. The stack file commit and push stay manual — CI deploys clone cluster-ops `develop`, so an unpushed stack file deploys nothing.
→ [First deploy details](reference/backend/bootstrap-service.md)

```bash
just gcp-pulumi bootstrap-service --stack dcr-kube1 --folder modware-order --image-tag <published-tag>
```

## 5. Publish CI credentials and variables

Key preflight (fail-closed), key publish to the org secret, then the `<PROD>_*` repo variables.
→ [CI credentials and variables details](reference/backend/ci-variables.md)

```bash
just ci check-deploy-credentials --cluster dcr-kube1 --sa-key config/keys/deployer-dcr-kube1.json
```

## 6. Render the tag workflow

Renders the complete `tag-build.yaml` — test, lint, then the composite deploy — plus the deletion of the dead `staging-build.yaml`.
→ [Workflow render details](reference/backend/render-deploy-workflows.md)

```bash
just ci render-tag-deploy --stack dcr-kube1 --app order --project modware-order --out tag-build.yaml
```

## 7. Verify

After the first tag deploy: running image matches the tag, and the service answers inside the cluster.
→ [Verification details](reference/backend/bootstrap-service.md#verification)

```bash
kubectl -n prod get deploy order-api-server -o jsonpath='{.spec.template.spec.containers[0].image}'
```

## 8. Troubleshooting

→ [Troubleshooting details](reference/backend/troubleshooting.md)

## 9. Related Documents

- [Upstream deploy refactor plan](plans/upstream-deploy-refactor.md)
- [Modware service plan](plans/modware-service-any-cluster.md)
- [ArangoDB deploy guide](arangodb-deploy.md)
- [kOps cluster setup](kops-setup.md)
