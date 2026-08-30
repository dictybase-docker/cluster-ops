# PostgreSQL Import Details (Cross-Project CloudNativePG Recovery)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Imports data from **another cluster's** CloudNativePG backup into a freshly-created Cluster — the same `bootstrap.recovery` mechanism CNPG uses for in-place restore, pointed at a barman object store in a **different GCP project**. No dump files, no `pg_restore`, no extra Job: the operator replays the base backup + WAL straight from the source bucket into the new instance's PVC.

The source backup was written by the same CloudNativePG operator process (barman object store layout: base backups + WAL under `gs://<bucket>/<bucketPath>/<clusterName>/`), so the new cluster reads it natively.

## Command

```bash
just postgres configure-source \
  --source-cluster <source-cluster-name> \
  --bucket <source-bucket> \
  --source-project <source-project-id>
```

## Behavior

- Creates/reuses reader service account `postgres-source-reader` **in the source project**
- Grants `roles/storage.objectViewer` on the source bucket only (bucket-level `gsutil iam ch` — the bucket already exists)
- Mints `credentials/<source-project>/postgres-source-reader.json` — **skipped if the file already exists** (keys are not idempotent; old ones keep working until deleted). Audit: `gcloud iam service-accounts keys list --iam-account postgres-source-reader@<source-project>.iam.gserviceaccount.com --project <source-project>`
- Stores the recovery source on the cluster stack: `properties.clusters[0].cluster.bootstrap.recovery.{sourceCluster,bucket,bucketPath[,targetTime]}` and `properties.sourceSecret.{name,key,filepath}`

The next `deploy-cluster` then:

1. Creates Secret `postgres-source-credentials` (content of the reader key, under `gcsCredentials`)
2. Adds an `externalClusters` entry named after the source cluster, pointing at `gs://<bucket>/<bucketPath>` with those credentials
3. Sets `bootstrap.recovery.source` to the same name — the first instance replays the source backup instead of running initdb

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--source-cluster` | Yes | — | The source cluster's **CNPG `Cluster` name** — CNPG stores backups under a folder named after it, and the recovery source must match that folder. This is the backup folder name, not this cluster's name |
| `--bucket` | Yes | — | Source GCS bucket holding the barman backups |
| `--source-project` | Yes | — | GCP project owning the source bucket |
| `--bucket-path` | No | `logto` | Source cluster's `backup.bucketPath` |
| `--target-time` | No | latest | RFC3339 PITR timestamp; omit for latest available |
| `--sa-name` | No | `postgres-source-reader` | Reader SA created in the source project |
| `--key-file` | No | `credentials/<source-project>/<sa-name>.json` | Read at `pulumi up` time |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## Why a Separate Reader SA

The `postgres-backup-sa` (from [backup](backup.md)) has objectAdmin pinned by IAM condition to **this** cluster's bucket — it cannot read the source bucket, and widening its condition would give a writer key read access to another project's data. The reader SA is source-project-local, read-only, and discarded after the import.

## Service Account Pairs — Do Not Mix

| SA | Lives in | Scope | Used by |
|----|----------|-------|---------|
| `postgres-backup-sa` | this project | objectAdmin on `cloudnative-pg-backup-<project-id>` | ongoing backup + WAL archive |
| `postgres-source-reader` | source project | objectViewer on the source bucket | one-shot import |
| `backup-gcs-sa` | this project | objectAdmin on the ArangoDB restic bucket | ArangoDB only — never PostgreSQL |

## One-Shot Bootstrap

Recovery **is** the bootstrap. Once the first instance exists, `deploy-cluster` re-runs are no-ops for data — the operator does not re-import. To re-import, [teardown](teardown.md) the Cluster and redeploy with the recovery source still set. To stop referencing the source after a successful import, remove the `bootstrap.recovery` block from the stack config (`pulumi config rm --path properties.clusters[0].cluster.bootstrap.recovery`).

## After the Import

- Databases/roles come over as they were in the source. The `logto-app` Secret still applies: `managed.roles` reconciles the owner role's password to the Secret, so the app password is the one you passed to `deploy-cluster --app-password`, not the source cluster's old one.
- Verify with `just postgres verify`, then connect to `logto-rw.prod.svc.cluster.local:5432`.

## Warnings

- **Run order matters.** `configure-source` before `deploy-cluster`. If the Cluster already exists, the import cannot retro-apply — teardown first.
- **Source version floor.** The source backup must come from a CloudNativePG version this operator (1.30.x) can read — any 1.x barman object store works, but the WAL format must be a PostgreSQL major this operator supports (14–18).
- **Key hygiene.** The reader key grants read on another project's backups. Delete it when the import is done if it is not needed for a repeat.
