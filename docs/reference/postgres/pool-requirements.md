# Pool Requirements for PostgreSQL (CloudNativePG)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## PostgreSQL Resource Shape

Stack: `cloudnative-pg-cluster`, image `ghcr.io/cloudnative-pg/postgresql:16.15-202608240846-system-bookworm` (multi-arch)

| Component | Count | Disk |
|-----------|-------|------|
| Instance (primary only — no replicas) | 1 | 100Gi `dictycr-balanced` |

Placement comes from `placement` in `cloudnative-pg-cluster/Pulumi.prod.yaml`:

- `nodeSelector pool=database` plus a `dedicated=database:NoSchedule` toleration
- pod anti-affinity `preferred` on `topology.kubernetes.io/zone` — inert with a single instance, kept so scaling to 3 instances needs no spec change
- No CPU/memory requests set — the operator's defaults apply

> **Lab vs Production**: Lab `dev`/`experiments` stacks run 1 instance, PostgreSQL 14, no placement — do not edit them. Use `Pulumi.prod.yaml` only. Production also runs 1 instance (no streaming replication, no failover) — the difference from lab is PostgreSQL 16, pool placement, and GCS backup.

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

Before installing PostgreSQL ([§3](../../postgres-deploy.md#3-install-postgresql)):

1. **CSI + StorageClasses** — [`pulumi-setup.md` §6](../../pulumi-setup.md#6-first-apply--storageclass)
2. **Namespaces** — `prod` and `operators`, ensured by `just postgres configure-backup`
