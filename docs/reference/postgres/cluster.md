# PostgreSQL Cluster Details (CloudNativePG)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Applies the `cloudnative-pg-cluster` stack (`Pulumi.prod.yaml`) and creates, per configured cluster:

1. GCS backup bucket `cloudnative-pg-backup-<project-id>` (versioning on, 58-day soft-delete, 65-day lifecycle delete, **`ForceDestroy: true`**)
2. Secret `postgres-backup-credentials` — content of the `postgres-backup-sa` JSON key under key `gcsCredentials`
3. Secret `logto-app` (`kubernetes.io/basic-auth`) — username `logto`, password from `--app-password`
4. `Cluster postgresql.cnpg.io/v1` — 1 instance, PostgreSQL **16**, data PVC on `dictycr-balanced`, pool placement
5. `ScheduledBackup` — daily base backup to the bucket (see [backup](backup.md))

## Command

```bash
just postgres deploy-cluster --app-password '<app-password>'
```

## Behavior

- Runs `ensure-stack` → sets the encrypted `bootstrap.userSecret.password` → `preview` → `create-resource`
- Waits for 1 ready `cnpg.io/cluster=logto` pod
- Waits for Cluster status phase `Cluster in healthy state`

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--app-password` | Yes | — | Stored encrypted as `properties.clusters[0].cluster.bootstrap.userSecret.password`; never generated or defaulted |
| `--cluster` | No | `logto` | Must match `properties.clusters[0].cluster.name` in the stack config |
| `--namespace` | No | `prod` | Namespace holding the Cluster |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--instances` | No | `1` | Expected pod count for the readiness wait |
| `--retries` / `--interval` | No | `90` / `10` | 15-minute wait budget |

## Bootstrap Ownership

The bootstrap `database` and `owner` (`logto`/`logto` in prod) are **fixed at first initdb**. Changing them in config afterwards does not rename the database or the role — it breaks idempotence. The owner role is also declared under `managed.roles` with `login`/`createdb`/`createrole` and `passwordSecret` pointing back at the same Secret — the operator reconciles the role password from `logto-app`, so rotating the password means updating that Secret (via `deploy-cluster --app-password`) and the operator applies it. Superuser access (`postgres` role) is enabled via `superuser: true`; its credentials land in Secret `logto-superuser`.

## Services

The operator creates three Services per cluster — no manual wiring:

| Service | Targets | Use |
|---------|---------|-----|
| `logto-rw` | primary only | application writes |
| `logto-ro` | replicas only | read-only queries |
| `logto-r` | any instance | anything |

All on port **5432**, e.g. `postgres://logto@logto-rw.prod.svc.cluster.local:5432/logto`. With `instances: 1` there are no replicas — all three Services resolve to the single pod, and `-rw` is the only one worth using.

## Single-Instance Trade-off

No streaming replication: a pod/node failure means downtime until Kubernetes reschedules the pod and the PVC reattaches (minutes), and there is no promoted replica to hide it. Data safety is unaffected — the WAL archive + daily base backup still give point-in-time recovery — but recovery from total PVC loss is a full restore, not a failover. Scale up by setting `instances: 3` in `Pulumi.prod.yaml`; the operator adds replicas in place, no re-bootstrap.

## PostgreSQL 16

The operand image is pinned to the immutable official-catalog tag `16.15-202608240846-system-bookworm` — major version stays **16**; only the patch-level timestamp moves. Current tags come from the [official bookworm catalog](https://github.com/cloudnative-pg/postgres-containers/blob/main/Debian/ClusterImageCatalog-bookworm.yaml). Operator-major compatibility (1.30.x supports PG 14–18) is what constrains upgrades; bump the tag in `cloudnative-pg-cluster/Pulumi.prod.yaml`.
