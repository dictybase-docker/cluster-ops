# PostgreSQL Backup Details (CloudNativePG)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Backups run through the **Barman Cloud CNPG-I plugin** — the in-tree `barmanObjectStore` support is deprecated since CNPG 1.26 and removed in 1.31. Two layers, both created by `deploy-cluster`:

1. **WAL archiving** — continuous, to `gs://cloudnative-pg-backup-<project-id>/logto`, gzip, max 3 parallel streams (point-in-time recovery window)
2. **ScheduledBackup** — daily base backup (`0 0 0 * * *`), `method: plugin`, targeted at the primary, `immediate: true` so the first backup starts right after creation, `backupOwnerReference: self` so old backups are garbage-collected with the resource

The plugin reads an **`ObjectStore` CR** (`barmancloud.cnpg.io/v1`, name `<cluster>-store`) created by the cluster stack; `retentionPolicy: 60d` lives on the ObjectStore, and the bucket additionally enforces a 65-day lifecycle delete.
→ [Plugin details](backup-plugin.md)

## Prerequisites Command

```bash
just postgres configure-backup
```

Composite tail: after the SA/key/config steps it also runs [`deploy-backup-plugin`](backup-plugin.md) so one command wires identity, config, and plugin. That tail needs a **running** operator (it aborts when the `clusters.postgresql.cnpg.io` CRD is absent), so [`deploy-operator`](operator.md) comes first.

## Behavior

- Creates service account `postgres-backup-sa` (idempotent)
- Grants `roles/storage.objectAdmin` **and** `roles/storage.bucketViewer`, both with an IAM condition pinning them to `projects/_/buckets/cloudnative-pg-backup-<project-id>` — nothing else in the project. The `objectAdmin` role alone is insufficient: barman-cloud's destination check performs a bucket GET (`storage.buckets.get`), which `objectAdmin` does not include — without `bucketViewer`, WAL archiving and backups fail with 403
- Mints `credentials/<project-id>/postgres-backup-sa.json` — **skipped if the file already exists** (key creation is not idempotent; old keys keep working until deleted). Audit with `gcloud iam service-accounts keys list --iam-account postgres-backup-sa@<project>.iam.gserviceaccount.com --project <project>`
- Creates no namespaces — the `namespace-bootstrap` stack owns `prod`/`operators` ([`pulumi-setup.md` §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces))
- Sets `properties.clusters[0].cluster.backup.bucket` and `properties.backupSecret.filepath` on the cluster stack
- Deploys the Barman Cloud plugin (composite tail) — see [backup-plugin](backup-plugin.md)

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--bucket` | No | `cloudnative-pg-backup-<project-id>` | The bucket is created later by the cluster stack — this recipe only grants access to the name |
| `--project` | No | `$PROJECT_ID` | From the cluster env |
| `--sa-name` | No | `postgres-backup-sa` | Created/reused in this project |
| `--key-file` | No | `<repo>/credentials/<project>/<sa-name>.json` (absolute) | Stored as-is in the stack config and read at `pulumi up` time — must be absolute, `pulumi -C` changes the working directory |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## Service Account Pairs — Do Not Mix

- `postgres-backup-sa` — CloudNativePG only; condition pinned to `cloudnative-pg-backup-<project-id>`
- `backup-gcs-sa` — ArangoDB restic only; condition pinned to its restic bucket

Neither key can write the other system's bucket. Reusing ArangoDB's key here fails at backup time with `storage.objects.create` denied — the guard is the IAM condition, not the filename.

## Schedule Format

CloudNativePG `ScheduledBackup` uses the **six-field** cron format (seconds first): `0 0 0 * * *` = daily at 00:00:00. A five-field expression is rejected by the operator.

## Restore

- **Own backups, in place** (same cluster, same bucket) — no recipe yet, reserved for a future revision. CloudNativePG can do it via a `bootstrap.recovery` pointed at this cluster's own ObjectStore, but nothing in this repo wires that path.
- **Another cluster's backups** — that is the import path: [`configure-source`](import.md) plus a fresh `deploy-cluster`, or [`reset-cluster`](import.md#reset-re-import-into-a-running-cluster) when the cluster already exists.
- **Bucket contents** are readable with the backup key for manual inspection:

  ```bash
  GOOGLE_APPLICATION_CREDENTIALS=credentials/<project-id>/postgres-backup-sa.json \
    gcloud storage ls --recursive gs://cloudnative-pg-backup-<project-id>/logto/logto/
  ```

  The layout is `<destinationPath>/<serverName>/`. `destinationPath` is `gs://<bucket>/<bucketPath>` (`bucketPath: logto`), and the cluster's own store sets no `serverName`, so barman uses the **cluster name** — also `logto` ([ObjectStore CRD](../../../crds/kubernetes/barmancloud/v1/pulumiTypes.go): *"the cluster name is used if this parameter is omitted"*). Hence the doubled `logto/logto/`. A recovery source store is the same shape with `serverName` pinned to the source cluster ([import](import.md#behavior)).

## Hazard

The bucket lives in the `cloudnative-pg-cluster` stack with **`ForceDestroy: true`** — `pulumi destroy` (i.e. [teardown](teardown.md)) deletes the bucket **and every backup in it**. Soft-delete (58 days) is the only safety net, and only within that window.
