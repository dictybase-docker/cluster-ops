# Redis Install Details

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## What It Does

Applies the `redis-standalone` stack (`Pulumi.dcr-kube1.yaml`) which creates:

1. PVC `redis-data` (50Gi, `dictycr-balanced`)
2. Deployment `redis` — 1 replica, image `redis:8.4.6`, TCP readiness/liveness probes on 6379, `fsGroup 999` (official image uid), pool placement
3. Service `redis` on 6379

## Command

```bash
just redis deploy
```

## Behavior

- Runs `ensure-stack` → `preview` → `create-resource`
- Waits for a ready `app=redis` pod
- Prints the `redis` Service

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--name` | No | `redis` | Deployment/Service/PVC base name |
| `--namespace` | No | `prod` | |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` / `--interval` | No | `60` / `10` | 10-minute wait budget |

## Persistence

- **AOF**: `aof: true` adds `--appendonly yes --appendfsync everysec` — every write is logged, at most ~1s of data lost on a crash. The AOF lives on the PVC, so a rescheduled pod resumes with its data.
- **Probes**: plain TCP checks on 6379 — the Redis protocol handshake needs no auth.
- **Image**: `redis:8.4.6` (Redis Open Source 8). The former Redis Stack modules (Search, JSON, TimeSeries, probabilistic) are merged into core since 8.0; the lab's `redis-stack-server` line is EOL since Dec 2025. Lab stacks keep their old image — the generated pod spec is unchanged there.

## No Authentication

The stack sets no `requirepass` — the server **accepts every in-cluster connection**. Any pod can read, write, and `FLUSHALL` every key. This is a deliberate trade: zero client-side changes for existing apps, with security effort concentrated on PostgreSQL and ArangoDB. Exposure stays in-cluster — port 6379 is ClusterIP-only, no external ingress exists.

Adding password auth later means one env/URL change per client plus the Secret wiring; `redis-standalone`'s git history carries the previous `--requirepass` implementation.

## Service

| Item | Value |
|------|-------|
| Address | `redis.prod.svc.cluster.local:6379` |
| URL form | `redis://redis.prod.svc.cluster.local:6379` |

## No Failover, No Backup Job

Single pod. Data safety rests on the AOF PVC, which survives pod/node replacement but **not** PVC deletion. The daily restic backup to GCS ([backup details](backup.md)) is the off-cluster copy — deploy it right after this stack.
