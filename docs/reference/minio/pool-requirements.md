# Pool Requirements for MinIO

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

## MinIO Resource Shape

Stack: `minio`, image `bitnamilegacy/minio:2025.7.23-debian-12-r3`, standalone mode

| Component | Count | Disk |
|-----------|-------|------|
| MinIO server (standalone) | 1 | 150Gi `dictycr-balanced` |

Placement comes from `placement.pool` in `minio/Pulumi.prod.yaml` — the chart values get a `nodeSelector pool=database` plus a `dedicated=database:NoSchedule` toleration. No CPU/memory requests set — chart defaults apply.

> **Lab vs Production**: Lab `dev`/`experiments`/`local` stacks use chart 14.7.10 / image 2024.8.3 with no placement — do not edit them. Use `Pulumi.prod.yaml` only.

## Kubernetes Pool Requirements

Same `stateful-db` pool as ArangoDB and PostgreSQL — see [ArangoDB pool requirements §Kubernetes Pool Requirements](../arangodb/pool-requirements.md#kubernetes-pool-requirements) for the instancegroup fields and how to add the pool or taint. MinIO places no extra constraint on the pool.

### Verification

```bash
just minio check-pool
```

Checks and prints PASS/FAIL for node count (expect 3), taint, Ready status; zone spread is informational.

## Prerequisites

Before installing MinIO ([§2](../../minio-deploy.md#2-install-minio)):

1. **CSI + StorageClasses** — [`pulumi-setup.md` §6](../../pulumi-setup.md#6-first-apply--storageclass)
2. **Namespace `prod`** — ensured by `just postgres configure-backup` or created by hand (`kubectl create namespace prod`) when PostgreSQL is not installed
