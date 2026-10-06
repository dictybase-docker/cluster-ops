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

All four backend services deploy with two commands (the aggregate runs steps
2–6 per service, idempotently):

```bash
just ci create-deploy-key --cluster dcr-kube1          # once per cluster: SA + key
just ci deploy-backend-services --stack dcr-kube1      # scaffold→gate→deploy→vars→render
git add modware-*/Pulumi.dcr-kube1.yaml && git commit && git push   # stack files BEFORE any tag
# then: PR bin/ci-render/<folder>-tag-build.yaml into each service repo, merge, cut tags
```

Single-service or step-by-step runs — each numbered section below documents
one recipe:

```bash
just gcp-pulumi check-backend-prereqs --stack dcr-kube1 --folder modware-stock   # 3. gate only
just gcp-pulumi bootstrap-service --stack dcr-kube1 --folder modware-stock       # 4. converge one service
just gcp-cluster verify-deployer-access --cluster dcr-kube1 --sa-key credentials/dcr-kube1/deployer.json
```

## 1. Verify the cluster registry

Every later step reads the registry entry; `register-cluster` creates entries from bootstrap artifacts.
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

Key creation at the standard `credentials/<cluster>/deployer.json` path, preflight, org-secret publish, then the `<PROD>_*` repo variables — the aggregate runs 5b–5d automatically.
→ [CI credentials details](reference/backend/ci-credentials.md)
→ [CI variables details](reference/backend/ci-variables.md)

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
- [Aggregate deploy details](reference/backend/deploy-services.md)
- [ArangoDB deploy guide](arangodb-deploy.md)
- [kOps cluster setup](kops-setup.md)
