# Pool Requirements for PostgreSQL (CloudNativePG)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## PostgreSQL Resource Shape

Stack: `cloudnative-pg-cluster`, image `ghcr.io/cloudnative-pg/postgresql:16.15-202609101440-standard-trixie` (multi-arch)

| Component | Count | Disk |
|-----------|-------|------|
| Instance (primary only — no replicas) | 1 | 100Gi `dictycr-balanced` |

PostgreSQL tuning: `pgconfig.maxConnections` **200** and `pgconfig.sharedBuffers` **512MB** in the stack config are the only two settings exposed there. Both land in the Cluster's `spec.postgresql.parameters`, alongside a fixed tuning set (work_mem, WAL sizing, checkpoint, logging, autovacuum) hardcoded in `cloudnative-pg-cluster/postgres.go` — edit that file to change anything else.

Placement comes from `placement` in `cloudnative-pg-cluster/Pulumi.dcr-kube1.yaml`:

- `nodeSelector pool=database` plus a `dedicated=database:NoSchedule` toleration
- pod anti-affinity `preferred` on `topology.kubernetes.io/zone` — inert with a single instance, kept so scaling to 3 instances needs no spec change
- No CPU/memory requests set — the operator's defaults apply

> **Lab vs Production**: Lab `dev`/`experiments` stacks run 1 instance, PostgreSQL 14, no placement — do not edit them. Use `Pulumi.dcr-kube1.yaml` only. Production also runs 1 instance (no streaming replication, no failover) — the difference from lab is PostgreSQL 16, pool placement, and GCS backup.

## Kubernetes Pool Requirements

Same `stateful-db` pool as ArangoDB — see [ArangoDB pool requirements §Kubernetes Pool Requirements](../arangodb/pool-requirements.md#kubernetes-pool-requirements) for the instancegroup fields and how to add the pool or taint. PostgreSQL places no extra constraint on the pool (the operand image is multi-arch; amd64 is not required).

### Verification

```bash
just postgres check-pool
```

Checks and prints PASS/FAIL for:
- Node count (expect 3)
- Taint (`dedicated=database:NoSchedule`)
- Ready status
- Zone spread (informational)

## Prerequisites

Before installing the operator ([§2](../../postgres-deploy.md#2-operator)), both from [`pulumi-setup.md` §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces):

1. **CSI + StorageClasses** — `dictycr-balanced` (the data PVC) and `dictycr-ssd`; `just postgres verify` checks for both
2. **Namespaces** — `prod` and `operators`, owned by the `namespace-bootstrap` stack (`just gcp-pulumi apply-namespaces`). No PostgreSQL recipe creates them: `deploy-operator` refuses a namespace that does not match the stack's `operatorsNamespace` export, and `deploy-backup-plugin` aborts when `operators` is missing
