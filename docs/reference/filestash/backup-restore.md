# Filestash Backup and Restore

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

The `filestash-backup` Pulumi project backs up the Filestash state with
`restic` to its own GCS bucket. The ArangoDB, Redis, and MinIO backups use
the same pattern.

**Velero does not back up the state.** These are the reasons:

- A disk snapshot of a live SQLite file is not consistent. The backup Job uses `sqlite3 .backup`, which is.
- With Velero, the recovery point depends on a schedule for the full cluster. The `filestash-backup` stack has its own schedule and `backup.maxAgeHours` target.
- The cluster excludes every datastore PVC from Velero. The state PVC follows the same rule.

→ [ADR 0001](../../adr/0001-filestash-state-backup-with-restic.md)

## Table of Contents

- [What the Backup Saves](#what-the-backup-saves)
- [Backup Resources](#backup-resources)
- [State Backup CronJob](#state-backup-cronjob)
- [just filestash configure-backup](#just-filestash-configure-backup)
- [just filestash deploy-backup](#just-filestash-deploy-backup)
- [just filestash backup-now](#just-filestash-backup-now)
- [just filestash list-snapshots](#just-filestash-list-snapshots)
- [just filestash restore-state](#just-filestash-restore-state)
- [just filestash restore-cleanup](#just-filestash-restore-cleanup)

## What the Backup Saves

| Data | Backup |
|---|---|
| Files in `uploads` and `downloads` | The MinIO `restic` backup. Refer to the [MinIO backup details](../minio/backup.md). |
| `config.json` and the SQLite files (`share.sql` and others) | `restic` snapshots in `gs://restic-filestash-backup-<project-id>`. |
| Admin password hash, `CONFIG_SECRET`, MinIO user keys | KMS-encrypted values in `filestash/Pulumi.<stack>.yaml` in Git. `deploy` makes the Secrets from them. |
| `restic` password, GCS project, service account key path | KMS-encrypted values in `filestash-backup/Pulumi.<stack>.yaml` in Git. |

`config.json` holds `general.secret_key`, and every share link depends on it.
A restore needs the snapshot and the same `CONFIG_SECRET`. Both come back
from the snapshot and the stack file. Each snapshot records the SHA-256 of
`CONFIG_SECRET`. The restore Job compares it with Secret `filestash` and
stops before any change when they differ.

**The `restic` password in the stack file is the only key to the snapshots.**
`configure-backup` never replaces it.

## Backup Resources

The `filestash-backup` stack creates these resources.

| Kind | Name | Notes |
|---|---|---|
| GCS bucket | `restic-filestash-backup-<project-id>` | Versioned, 58-day soft delete, the same rules as the other restic buckets. |
| Secret | `filestash-backup-auth` | Keys `resticPass`, `gcsProject`, `gcsCredentials`. Made from the encrypted stack values. |
| CronJob | `filestash-state-backup` | Refer to [State Backup CronJob](#state-backup-cronjob). |

Stack keys (prefix `filestash-backup:properties`):

| Key | Required | Default | Notes |
|---|---|---|---|
| `namespace` | Yes | none | Must equal the `filestash` stack namespace. |
| `bucket` | Yes | none | `restic-filestash-backup-<project-id>`. |
| `schedule` | No | `30 */4 * * *` | Every 4 hours. |
| `keepLast` / `keepDaily` | No | `42` / `30` | `restic forget` retention: the last 7 days of 4-hour snapshots, then one snapshot for each day for 30 days. |
| `secret.resticPass`, `secret.gcsProject`, `secret.serviceAccountFile` | Yes | none | Encrypted. Set by `configure-backup`. |
| `resticImage` / `sqliteImage` | No | pinned by digest | |

The state PVC carries the label `velero.io/exclude-from-backup=true`, and the
pod carries `backup.velero.io/backup-volumes-excludes: state,cache`, the same
as the other datastores.

## State Backup CronJob

`filestash-state-backup` runs on `schedule`. Filestash stays up during the copy.

**Consistency limit.** `sqlite3 .backup` makes each SQLite file consistent by itself. The files and `config.json` are not copied at one point in time. A share that staff make during the copy can be in `share.sql` but not match a settings change made at the same time. Make settings changes outside the backup window (minute 30 of every fourth hour), or run `backup-now` after the change.

1. The pod runs on the node of the Filestash pod, because the PVC is `ReadWriteOnce`.
2. Init container `copy` writes `/backup/state` in an `emptyDir`. It writes these files:
   - A `sqlite3 .backup` copy of each `db/*.sql`, after a `PRAGMA integrity_check`.
   - `counts.txt`, with the row count of each table.
   - `config.json`.
   - `SHA256SUMS`, which also covers `counts.txt`.
   - `manifest.json`, with the SHA-256 of `CONFIG_SECRET`.

   The copy fails closed. `share.sql` must exist. `config.json` must be valid JSON with a nonempty `general.secret_key`. A failed step fails the Job before any upload.
3. Container `restic` initializes the repository only when `restic` reports that it does not exist (exit code 10). A wrong password, a lock, or a network error stops the Job. Then it runs `restic backup /backup/state --tag filestash-backup --host filestash`, and applies the retention policy with `forget --prune`.
4. `concurrencyPolicy: Forbid`, and the Job fails after 30 minutes (`activeDeadlineSeconds`). When no Filestash pod runs, the backup pod has no node and the Job fails. `verify` then reports the snapshot age.

## just filestash configure-backup

### What It Does

Prepares the encrypted values of the `filestash-backup` stack.

### Behavior

1. Binds every `gcloud` call and the key mint to the `sa-manager` key file (`--manager-key`, default `credentials/<project>/sa-manager.json`). It refuses a key that is not `sa-manager@<project>`. The ambient `gcloud` account is never used.
2. Checks that `filestash-backup/Pulumi.<stack>.yaml` exists, that its namespace equals the `filestash` namespace, and that its bucket equals `restic-filestash-backup-<project>`.
3. Creates service account `filestash-backup-sa` only when `gcloud` reports `NOT_FOUND`. Any other error stops the recipe.
4. Grants `roles/storage.objectAdmin`, limited to the bucket by an IAM condition. Then it creates the key file, unless the file is on disk already.
5. Keeps `secret.resticPass` when the stack has it. Otherwise it stores a random 40-character password.
6. Stores `secret.gcsProject` and `secret.serviceAccountFile` (the absolute key path).

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--project` | No | `$PROJECT_ID` | Must match the `bucket` in the stack file. |
| `--key-file` | No | `credentials/<project>/filestash-backup-sa.json` | Git does not hold this file. |
| `--manager-key` | No | `credentials/<project>/sa-manager.json` | Must be the key of `sa-manager@<project>`. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

### Recover the Service Account Key

The stack stores only the path of the key file. The key file itself is in
`credentials/<project>/filestash-backup-sa.json` on the operator host, and Git
does not hold it. When the file is lost, run `configure-backup` again: it makes
a new key, and the snapshots stay readable, because the `restic` password does
not change. Then run `deploy-backup` and delete the old key.

**Service account keys accumulate.** List them with `just filestash backup-keys`, which runs as `sa-manager`.

## just filestash deploy-backup

### What It Does

Deploys the `filestash-backup` stack: the GCS bucket, Secret `filestash-backup-auth`, and CronJob `filestash-state-backup`.

### Behavior

1. Checks that the stack file has `secret.resticPass`, `secret.gcsProject`, and `secret.serviceAccountFile`. If not, it prints `run: just filestash configure-backup`.
2. Checks that `pvc/filestash-state` exists, because the CronJob mounts it. If not, it prints `run: just filestash deploy`.
3. Selects or initializes the stack, then runs `pulumi up` on `filestash-backup/`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | The `filestash-backup` namespace must equal the `filestash` namespace. |

## just filestash backup-now

### What It Does

Makes one state snapshot now and prints its id. Filestash stays up.

### Behavior

1. Stops while a state backup Job or pod is unfinished.
2. Creates a Job from CronJob `filestash-state-backup`.
3. Waits up to 30 minutes. A failed Job prints its logs.
4. Prints the `restic` snapshot id and the `restore-state` command for it.

Record the id before an upgrade.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash list-snapshots

### What It Does

Lists the `restic` snapshots of the Filestash state, the newest last. Use it to select a `--snapshot` id for `restore-state`. The recipe makes no change.

### Behavior

1. Checks for a local `restic` CLI. If it is missing, install it first.
2. Reads the `restic` password, the GCS project, and the key file path from the encrypted `filestash-backup` stack values.
3. Gives them to `restic` in the environment only.
4. Runs `restic snapshots --tag filestash-backup --host filestash` on `gs:restic-filestash-backup-<project-id>:/`.

The key file `credentials/<project>/filestash-backup-sa.json` must be on the
operator host, at the path in `secret.serviceAccountFile`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash restore-state

### What It Does

Restores the Filestash state from a `restic` snapshot. It has two modes:

- **Test restore** (`--target-namespace`): restores into a new namespace and leaves the live service alone.
- **Disaster recovery** (`--in-place yes`): replaces the live state. Destructive.

Get snapshot ids with [`just filestash list-snapshots`](#just-filestash-list-snapshots). The default snapshot is `latest`.

### Test Restore

Preflight, before any change:

1. The snapshot is `latest` or a hexadecimal id.
2. The target is not the live namespace and does not exist.
3. Secret `filestash-backup-auth`, Secret `filestash`, ConfigMap `filestash-seed`, `deployment/filestash`, and `pvc/filestash-state` exist in the live namespace.

Then:

1. Creates the namespace with the label `filestash-restore-test=true`.
2. Applies NetworkPolicy `filestash-deny-egress`: the restored Filestash pod gets no egress, so the restored keys cannot reach MinIO or SMTP. The restore Job keeps egress to GCS.
3. Copies Secret `filestash`, Secret `filestash-backup-auth`, and ConfigMap `filestash-seed`, and creates a new `filestash-state` PVC with the live size and class.
4. Runs Job `filestash-state-restore`: `restic restore` into an `emptyDir`, then `SHA256SUMS`, `share.sql`, `PRAGMA integrity_check`, the `secret_key` check, and the `CONFIG_SECRET` fingerprint check, all before any change. Then the replacement of the state and the row count check.
5. Starts a copy of the live Deployment, with no Service and no route, and checks `/healthz`.

### Disaster Recovery

`--in-place yes [--snapshot <id>]` (destructive). Use it when the state is damaged, or to go back to the snapshot that `upgrade` made.

1. Takes `lease/filestash-maintenance` and suspends the CronJob. At the end, the old suspend value comes back.
2. Refuses while a backup Job or pod is unfinished.
3. Runs Job `filestash-state-check`: the same checks as the restore, with no state PVC mounted. A failed check stops the recipe before any change.
4. Scales the live Deployment to 0 and runs Job `filestash-state-restore`.
5. Scales to 1 only after the Job succeeds. After a failed Job, Filestash stays at 0 replicas.

For a lost PVC, run `just filestash deploy` first. It creates an empty PVC. Then run `--in-place yes`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--snapshot` | No | `latest` | A `restic` snapshot id (8 to 64 hexadecimal characters). |
| `--target-namespace` | Test restore | none | Must not exist, and must not be the live namespace. |
| `--in-place` | Disaster recovery | none | `yes`. Takes no `--target-namespace`. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | `--namespace` is the live namespace. |

## just filestash restore-cleanup

### What It Does

Deletes a restore test namespace.

### Behavior

1. Stops when `--target-namespace` is empty or is the live namespace.
2. Stops when the namespace has no label `filestash-restore-test=true`. Only `restore-state` sets that label.
3. Deletes the namespace.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--target-namespace` | Yes | none | The namespace of the test restore. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | `--namespace` is the live namespace. |
