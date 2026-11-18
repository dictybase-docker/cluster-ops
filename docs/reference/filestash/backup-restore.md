# Filestash Backup and Restore

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [What Is Backed Up](#what-is-backed-up)
- [State Copy CronJob](#state-copy-cronjob)
- [just filestash backup-now](#just-filestash-backup-now)
- [just filestash restore-state](#just-filestash-restore-state)
- [just filestash restore-cleanup](#just-filestash-restore-cleanup)

## What Is Backed Up

| Data | Backup |
|---|---|
| Files in `uploads` and `downloads` | The MinIO `restic` backup. Refer to the [MinIO backup details](../minio/backup.md). |
| `config.json` and the SQLite files | Completed copies in `/app/data/state/backups/`, then the Velero backup of the PVC. |
| Secret `filestash` | Velero. A restore needs the same `CONFIG_SECRET`. |

`config.json` holds `general.secret_key`. Every share link depends on it. A restore without the same `config.json` and `CONFIG_SECRET` breaks all links.

## State Copy CronJob

`filestash-state-backup` runs on `backup.schedule` (default every 4 hours). Filestash stops for the copy, because separate SQLite copies and a later `config.json` copy do not give one consistent state.

1. `scale-down` scales `deployment/filestash` to 0 and waits until no pod runs.
2. `copy` writes `backups/.partial-<ts>/`: `sqlite3 .backup` of each `db/*.sql`, `PRAGMA integrity_check`, the row count of each table, `config.json`, `SHA256SUMS`, and `manifest.json`. Then it renames the folder to `backups/<ts>`.
3. `copy` deletes partial folders older than one hour, and completed copies after the newest `backup.keep`.
4. `scale-up` always scales to 1, then fails the Job if a step failed.

The Job runs on the node of the Filestash pod, because the PVC is `ReadWriteOnce`.

## just filestash backup-now

Runs a Job from the CronJob and waits, then runs `velero backup create filestash-<ts> --include-namespaces <ns> --selector app.kubernetes.io/name=filestash --wait`. Fails unless the backup phase is `Completed`.

## just filestash restore-state

### Test Restore

`--backup <name> --target-namespace <new-namespace>`. Preflight, before any change:

1. The target is not the live namespace and does not exist.
2. The Velero backup is `Completed`.
3. The backup holds `pvc/filestash-state` and Secret `filestash`.

Then:

1. Creates the namespace with the label `filestash-restore-test=true` and the NetworkPolicy `deny-all-egress`. The restored keys cannot reach MinIO or SMTP.
2. Restores the PVC, Secrets, and ConfigMaps with `--namespace-mappings`.
3. Runs Job `filestash-state-restore`: checks `SHA256SUMS`, replaces the SQLite files and `config.json` with the newest completed copy, and compares the row counts.
4. Starts a copy of the live Deployment in the target, with no Service and no route, and checks `/healthz`.

### Disaster Recovery

`--in-place yes` (destructive). Scales the live Deployment to 0, runs the restore Job on the live PVC from its own newest completed copy, and scales to 1. Use it when the SQLite files are damaged. For a lost PVC, restore the Velero backup into the live namespace first.

| Flag | Required | Notes |
|---|---|---|
| `--backup` | Test restore | Velero backup name. |
| `--target-namespace` | Test restore | Must not exist. |
| `--in-place` | Disaster recovery | `yes` |

## just filestash restore-cleanup

Deletes a restore test namespace. Refuses a namespace without the label `filestash-restore-test=true`.
