# Pool Requirements for Redis

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## Redis Resource Shape

Stack: `redis-standalone`, image `redis:8.4.6` (official), single pod

| Component | Count | Disk |
|-----------|-------|------|
| Redis server (standalone) | 1 | 50Gi `dictycr-balanced` |

Placement comes from `placement.pool` in `redis-standalone/Pulumi.prod.yaml` — the pod spec gets a `nodeSelector pool=database` plus a `dedicated=database:NoSchedule` toleration. No CPU/memory requests set — Kubernetes defaults apply.

> **Lab vs Production**: Lab `dev`/`experiments` stacks run `redis-stack-server:7.4.0-v0` with no auth, AOF, or placement — do not edit them. Use `Pulumi.prod.yaml` only.

## Kubernetes Pool Requirements

Same `stateful-db` pool as ArangoDB and PostgreSQL — see [ArangoDB pool requirements §Kubernetes Pool Requirements](../arangodb/pool-requirements.md#kubernetes-pool-requirements) for the instancegroup fields and how to add the pool or taint. Redis places no extra constraint on the pool.

### Verification

```bash
just redis check-pool
```

Checks and prints PASS/FAIL for node count (expect 3), taint, Ready status; zone spread is informational.

## Prerequisites

Before installing Redis ([§2](../../redis-deploy.md#2-install-redis)):

1. **CSI + StorageClasses** — [`pulumi-setup.md` §6](../../pulumi-setup.md#6-first-apply--storageclass)
2. **Namespace `prod`** — ensured by `just postgres configure-backup` or created by hand (`kubectl create namespace prod`) when PostgreSQL is not installed
