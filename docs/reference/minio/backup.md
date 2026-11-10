# MinIO Backup Details

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

## What It Does

Two stacks and two composite commands back up all buckets and anonymous S3 policies from the in-cluster MinIO service to Google Cloud Storage:

1. **`minio-backup-secrets`**: Creates Secret `minio-backup-auth` (keys `resticPass`, `gcsProject`, `gcsCredentials`) in the `namespace-bootstrap` application namespace (`prod`). The `gcsCredentials` value holds the content of `credentials/<project-id>/minio-backup-sa.json`.
2. **`minio-backup`**: Creates the versioned GCS bucket `restic-minio-backup-<project-id>` (58-day soft-delete retention, `ForceDestroy: true`), a daily **01:30** CronJob (`minio-backup-cronjob`), and an immediate first-run Job (`minio-immediate-backup-job`, `ttlSecondsAfterFinished: 900`).

### Backup Pod Architecture

Each backup Job runs two containers in sequence, sharing an ephemeral scratch PVC (`minio-backup-ephemeral`, `150Gi` `dictycr-balanced`) mounted at `/backup`:

1. **Init container `mc-dump`** (`minio/mc:RELEASE.2025-08-13T08-35-41Z`): Reads root credentials from Secret `minio-root` (`rootUser`, `rootPassword`), connects to `http://minio:9000`, mirrors every bucket into `/backup/buckets/<bucket>` (`mc mirror --preserve --overwrite --retry`), and exports each bucket's anonymous policy into `/backup/policies/<bucket>.json` (`mc anonymous get-json`).
2. **Main container `restic-backup`** (`restic/restic:0.17.0`): Mounts `/var/secret/gcs-credentials` from Secret `minio-backup-auth`, initializes the restic repository in `gs:restic-minio-backup-<project-id>:/` on first run, and snapshots `/backup` with tag `minio-backup`.

---

## Commands

```bash
just minio configure-backup-secrets --restic-password '<restic-pass>'
just minio deploy-backup
```

### Behavior

`configure-backup-secrets`:

1. Creates or reuses service account `minio-backup-sa` and grants `roles/storage.objectAdmin` pinned to `projects/_/buckets/restic-minio-backup-<project-id>` via an IAM condition.
2. Mints `credentials/<project-id>/minio-backup-sa.json` (reuses an existing valid key file on disk).
3. Verifies that the target namespace matches the `namespace-bootstrap` `appNamespace` export.
4. Stores `resticPass`, `gcsProject`, `keyname`, and `filepath` as KMS-encrypted secrets on `minio-backup-secrets` and applies the stack.

`deploy-backup`:

1. Pins `gcp:project` from `$PROJECT_ID` on the `minio-backup` stack.
2. Refuses to run if `resticSecret`, `bucketSecret`, or `projectSecret` points at the read-only `minio-backup-source` Secret.
3. Applies `minio-backup`, waits for `minio-immediate-backup-job` to complete, prints the last 30 lines of `restic-backup` logs, and prints `minio-backup-cronjob`.

---

## Flags — `configure-backup-secrets`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--restic-password` / `-p` | Yes | — | Restic repository password |
| `--setup-sa` | No | `true` | Pass `--no-setup-sa` when supplying a custom key |
| `--gcs-project` / `-g` | No | `$PROJECT_ID` | From the `cluster-env` shell |
| `--gcs-key-file` / `-f` | No | `credentials/<project-id>/minio-backup-sa.json` | Must exist on disk during `pulumi up` |
| `--key-name` / `-k` | No | `gcsCredentials` | Secret data key name |
| `--namespace` / `-n` | No | `namespace-bootstrap` export | Optional guard |
| `--stack` / `-s` | No | `$PULUMI_STACK` | Target cluster stack |

## Flags — `deploy-backup`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | Namespace where the Job runs |
| `--stack` / `-s` | No | `$PULUMI_STACK` | Target cluster stack |
| `--retries` / `-r` | No | `90` | Job wait attempts |
| `--interval` / `-i` | No | `10` | Seconds between probes (15-minute budget) |

---

## Restore and Cross-Cluster Bootstrap

For same-cluster disaster recovery (`configure-restore` + `apply-restore`) and cross-cluster bootstrap (`bootstrap-from-snapshot`), refer to [MinIO Restore and Cross-Cluster Bootstrap Details](restore.md).
