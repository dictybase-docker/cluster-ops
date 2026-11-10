# MinIO Restore and Cross-Cluster Bootstrap Details

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

## What It Does

The `minio-restore` stack creates a one-off Kubernetes Job (`minio-restore-<restoreId>`, `ttlSecondsAfterFinished: 3600`) that restores buckets and their anonymous S3 policies from a `restic` snapshot in Google Cloud Storage into the in-cluster `minio` Service (`:9000`).

The Job runs two containers in sequence, sharing an ephemeral scratch PVC (`minio-restore-scratch`, `150Gi` `dictycr-balanced`) mounted at `/restore`:

1. **`restic-restore`** (`restic/restic:0.17.0`): Restores `/backup/buckets/*` and `/backup/policies/*.json` from `gs:<bucket>:/` into `/restore`. When `noLock: true` is set (cross-project bootstrap), prepends `--no-lock`. When `s3Bucket` is set, passes `--include /backup/buckets/<s3Bucket>` and `--include /backup/policies/<s3Bucket>.json` to restore only that bucket.
2. **`mc-restore`** (`minio/mc:RELEASE.2025-08-13T08-35-41Z`): Reads root credentials from Secret `minio-root`, creates each missing bucket on `http://minio:9000` (`mc mb --ignore-existing`), mirrors the restored objects (`mc mirror --preserve --overwrite --retry`), and applies the saved anonymous policy JSON (`mc anonymous set-json`).

---

## Cross-Cluster Bootstrap (Cloning from a Source Cluster's GCS Backup)

To populate a new cluster's MinIO instance from a source cluster's `gs://restic-minio-backup-<source-project>` bucket:

**In the SOURCE cluster sub-shell:**

```bash
just minio grant-source-bucket-reader
just minio source-secret-value --namespace prod
```

---

**In the TARGET cluster sub-shell:**

```bash
just minio configure-source-secrets \
  --restic-password '<source-restic-pass>' \
  --gcs-project <source-project-id> \
  --gcs-key-file credentials/<source-project-id>/minio-restic-reader.json

just minio bootstrap-from-snapshot \
  --namespace prod \
  --bucket restic-minio-backup-<source-project-id>
```

### Behavior

1. `grant-source-bucket-reader` creates or reuses `minio-restic-reader@<source-project>.iam.gserviceaccount.com`, grants `roles/storage.objectViewer` on the source bucket, and writes `credentials/<source-project>/minio-restic-reader.json`.
2. `configure-source-secrets` sets `properties.sourceSecret.*` on `minio-backup-secrets` and creates Secret `minio-backup-source` in the target cluster's `prod` namespace without modifying `minio-backup-auth`.
3. `bootstrap-from-snapshot` registers an `EXIT` trap that runs `just minio reset-restore-config`, then runs `configure-bootstrap` (`noLock: true`, `overwrite: true`, Secret `minio-backup-source`, source bucket) followed by `apply-restore`.

### Flags — `bootstrap-from-snapshot`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | Yes | — | Target namespace (`prod`) |
| `--bucket` / `-b` | Yes | — | Source GCS bucket (`restic-minio-backup-<source-project>`) |
| `--snapshot` / `-p` | No | `latest` | Restic snapshot ID or `latest` |
| `--s3-bucket` / `-m` | No | all buckets | Optional single MinIO bucket filter |
| `--server` / `-v` | No | `minio` | Target MinIO Service name |
| `--restore-id` / `-i` | No | `bootstrap-<UTC timestamp>` | DNS-1123 label (at most 49 characters) |
| `--secret` / `-c` | No | `minio-backup-source` | Read-only source Secret name |
| `--stack` / `-s` | No | `$PULUMI_STACK` | Target cluster stack |

---

## Same-Cluster Disaster Recovery Restore

To restore all buckets (or a single bucket with `--s3-bucket <name>`) from this cluster's own `gs://restic-minio-backup-<project-id>` bucket:

```bash
just minio configure-restore --namespace prod
just minio apply-restore
```

### Flags — `configure-restore`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | Target namespace |
| `--server` / `-v` | No | `minio` | Target MinIO Service name |
| `--restore-id` / `-i` | No | `drill-<UTC timestamp>` | DNS-1123 label (at most 49 characters) |
| `--snapshot` / `-p` | No | `latest` | Restic snapshot ID or `latest` |
| `--s3-bucket` / `-b` | No | all buckets | Optional single MinIO bucket to restore |
| `--overwrite` / `-w` | No | `yes` | Passes `--overwrite` to `mc mirror` when `yes` |
| `--stack` / `-s` | No | `$PULUMI_STACK` | Target cluster stack |
