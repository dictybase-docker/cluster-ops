# Redis Install Details

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## What It Does

Applies the `redis-standalone` stack (`Pulumi.prod.yaml`) which creates:

1. PVC `redis-data` (50Gi, `dictycr-balanced`)
2. Secret `redis-auth` — key `password`
3. Deployment `redis` — 1 replica, image `redis:8.4.6`, TCP readiness/liveness probes on 6379, `fsGroup 999` (official image uid), pool placement
4. Service `redis` on 6379

## Command

```bash
just redis deploy --password '<password>'
```

## Behavior

- Runs `ensure-stack` → sets the encrypted `properties.auth.password` → `preview` → `create-resource`
- Waits for a ready `app=redis` pod
- Prints the `redis` Service

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--password` | Yes | — | Stored encrypted as `properties.auth.password`; never generated or defaulted |
| `--name` | No | `redis` | Deployment/Service/PVC base name |
| `--namespace` | No | `prod` | |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` / `--interval` | No | `60` / `10` | 10-minute wait budget |

## Persistence and Auth

- **AOF**: `aof: true` adds `--appendonly yes --appendfsync everysec` — every write is logged, at most ~1s of data lost on a crash. The AOF lives on the PVC, so a rescheduled pod resumes with its data.
- **Auth**: `auth.secretName` + `auth.password` add `--requirepass $(REDIS_PASSWORD)` with the value injected from Secret `redis-auth` via env. Clients must `AUTH` before any command.
- **Probes**: plain TCP checks on 6379 — they work with `--requirepass` because no `AUTH` is needed for the handshake.
- **Image**: `redis:8.4.6` (Redis Open Source 8). The former Redis Stack modules (Search, JSON, TimeSeries, probabilistic) are merged into core since 8.0; the lab's `redis-stack-server` line is EOL since Dec 2025. Lab stacks keep their old image — auth/AOF/placement keys are absent there, so the generated pod spec is unchanged.

## Service and Credentials

| Item | Value |
|------|-------|
| Address | `redis.prod.svc.cluster.local:6379` |
| URL form | `redis://:<password>@redis.prod.svc.cluster.local:6379` |
| Password | Secret `redis-auth`, key `password` |

Read it with `kubectl get secret redis-auth -n prod -o jsonpath='{.data.password}' | base64 -d`.

## No Failover, No Backup Job

Single pod. Data safety rests on the AOF PVC, which survives pod/node replacement but **not** PVC deletion. The repo's `redis-backup/` project is a restic-based CronJob wired to the ArangoDB `dictycr` secret chain — it is **not** part of this guide and not validated for this stack; treat backup automation as not yet implemented. For cache-style workloads, accept the PVC as the durability boundary.
