# PostgreSQL Backup Details (CloudNativePG)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Two layers, both created by `deploy-cluster` — no separate backup stack:

1. **WAL archiving** — continuous, to `gs://cloudnative-pg-backup-<project-id>/logto`, gzip, max 3 parallel streams (point-in-time recovery window)
2. **ScheduledBackup** — daily base backup (`0 0 0 * * *`), targeted at the primary, `immediate: true` so the first backup starts right after creation, `backupOwnerReference: self` so old backups are garbage-collected with the resource

Retention inside the cluster spec is `60d`; the bucket additionally enforces a 65-day lifecycle delete.

## Prerequisites Command

```bash
just postgres configure-backup
```

## Behavior

- Creates service account `postgres-backup-sa` (idempotent)
- Grants `roles/storage.objectAdmin` with an IAM condition pinning it to `projects/_/buckets/cloudnative-pg-backup-<project-id>` — nothing else in the project
- Mints `credentials/<project-id>/postgres-backup-sa.json` — **skipped if the file already exists** (key creation is not idempotent; old keys keep working until deleted). Audit with `gcloud iam service-accounts keys list --iam-account postgres-backup-sa@<project>.iam.gserviceaccount.com --project <project>`
- Ensures namespaces `prod` and `operators` (idempotent `kubectl apply`; coexists with the ArangoDB `backup_secrets` stack)
- Sets `properties.clusters[0].cluster.backup.bucket` and `properties.backupSecret.filepath` on the cluster stack

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--bucket` | No | `cloudnative-pg-backup-<project-id>` | The bucket is created later by the cluster stack — this recipe only grants access to the name |
| `--project` | No | `$PROJECT_ID` | From the cluster env |
| `--sa-name` | No | `postgres-backup-sa` | Created/reused in this project |
| `--key-file` | No | `credentials/<project>/<sa-name>.json` | Read at `pulumi up` time by the cluster stack |
| `--namespace` | No | `prod` | Namespace ensured alongside `operators` |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## Service Account Pairs — Do Not Mix

- `postgres-backup-sa` — CloudNativePG only; condition pinned to `cloudnative-pg-backup-<project-id>`
- `backup-gcs-sa` — ArangoDB restic only; condition pinned to its restic bucket

Neither key can write the other system's bucket. Reusing ArangoDB's key here fails at backup time with `storage.objects.create` denied — the guard is the IAM condition, not the filename.

## Schedule Format

CloudNativePG `ScheduledBackup` uses the **six-field** cron format (seconds first): `0 0 0 * * *` = daily at 00:00:00. A five-field expression is rejected by the operator.

## Hazard

The bucket lives in the `cloudnative-pg-cluster` stack with **`ForceDestroy: true`** — `pulumi destroy` (i.e. [teardown](teardown.md)) deletes the bucket **and every backup in it**. Soft-delete (58 days) is the only safety net, and only within that window.
