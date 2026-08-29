# ArangoDB Backup Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## What It Does

`arangodb-backup/Pulumi.prod.yaml` creates four resources:

| Resource | Detail |
|----------|--------|
| GCS bucket `restic-arangodb-backup-prod` | Versioned, 58-day soft-delete retention, deletes ARCHIVED objects once 3 newer versions exist. **`ForceDestroy: true`** |
| Ephemeral PVC `arangodb-backup-ephemeral` | 150Gi `dictycr-balanced`, ReadWriteOnce. Recreated per pod run — scratch space, not persistent archive |
| Job `arangodb-backup-job` | Runs immediately on `pulumi up`. Self-deletes after 15 min (`ttlSecondsAfterFinished: 900`). Proves pipeline works without waiting for 02:00 |
| CronJob `arangodb-backup-cronjob` | Schedule `0 2 * * *`. No `spec.timeZone` — "02:00" is node timezone, verify it. Triggered Jobs have **no** TTL and accumulate |

### Backup Container

Image: `dictybase/database-backup:sha-9c3b8ea`

Runs `arangodb-backup --user root --password $(PASSWORD) --output arangodump --repository gs:restic-arangodb-backup-prod:/`:
1. `arangodump --all-databases --include-system-collections` against `arangodb:8529`
2. `restic backup` uploads to GCS (initializes repo on first run)

## Secrets Required

Creates none — reads existing secrets:

| Env var | Secret | Key | Created by |
|---------|--------|-----|------------|
| `PASSWORD` | `arangodb-pass` | `password` | `arangodb-cluster` |
| `RESTIC_PASSWORD` | `dictycr` | `resticPass` | `backup_secrets` |
| `GOOGLE_APPLICATION_CREDENTIALS` | `dictycr` | `gcsCredentials` | `backup_secrets` |
| `GOOGLE_PROJECT_ID` | `dictycr` | `gcsProject` | `backup_secrets` |

## Configure Backup Secrets

`backup_secrets/main.go` creates namespaces `prod` and `operators`, plus Secret `dictycr` from a **local file** on the machine running `pulumi up`.

```bash
just arangodb configure-backup-secrets --restic-password '<restic repository password>'
```

Inside a `just cluster-env` shell both GCS arguments default: `--gcs-project` to `$PROJECT_ID` and `--gcs-key-file` to `credentials/$PROJECT_ID/backup-gcs-sa.json`. Pass them explicitly only to point at a different project or key path.

### Behavior

1. Runs `ensure-stack`
2. `pulumi config set-all --path --secret` for `resticPass`, `gcsProject`, `serviceAccount.keyname`, `serviceAccount.filepath`
3. `preview` → `create-resource`
4. Prints both namespaces, Secret `dictycr`, and data keys

### `dictycr` is this project's identity only

`dictycr` must always hold **this** cluster's GCP project id and a service account with write access to `restic-arangodb-backup-prod`. Never load a foreign project's service account JSON into it.

The cross-project first load in [deploy guide §4](../../arangodb-deploy.md#4-import-data) uses a **separate** Secret, `dictycr-source` (created by `source_backup_secrets`, read-only, source project). It exists for the bootstrap restore and nothing else. `just arangodb deploy-backup` aborts if any of `resticSecret`/`bucketSecret`/`projectSecret` is set to `dictycr-source`, because backing up through the read-only foreign identity would target the wrong repository.

### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--restic-password` | Yes | — | Restic repository password |
| `--gcs-project` | No | `$PROJECT_ID` | From the cluster env file; recipe fails if both are empty |
| `--gcs-key-file` | No | `credentials/$PROJECT_ID/backup-gcs-sa.json` | Must exist on this machine, stored as absolute path |
| `--key-name` | No | `gcsCredentials` | Must stay `gcsCredentials` — both backup and restore look up this key |

Rotating any value: same command again — overwrites config, re-applies Secret.

## Deploy Backup

```bash
just arangodb deploy-backup
```

### Behavior

1. `ensure-stack` → `preview` → `create-resource`
2. Waits for Job `arangodb-backup-job` to succeed
3. Tails 30 log lines
4. Prints CronJob

### Failure Handling

- If Job reports `failed`: dumps last 100 log lines, exits non-zero
- Budget: `--retries 90 --interval 10` (15 minutes)
- First-run failure is never "repository already exists" — look for secret-key typo or `arangodump` auth error
- Job's own TTL can delete it before you read logs; recipe says so instead of erroring

## Teardown Warning

> **`ForceDestroy: true`** means `just gcp-pulumi remove-resource --folder arangodb-backup` deletes the GCS bucket **and every snapshot in it** immediately, with no confirmation.

`just arangodb teardown` does **not** destroy the backup stack.
