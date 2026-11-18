# Filestash state is backed up with restic to its own GCS bucket, not Velero

Filestash keeps every share link, its settings, and the encrypted staff
credentials in SQLite files and `config.json` on one PVC. We back up that
state with a dedicated `filestash-backup` stack: a CronJob makes an online
SQLite `.backup` copy (Filestash keeps running) and a `restic` snapshot to
`gs://restic-filestash-backup-<project-id>`, the same pattern as the ArangoDB,
Redis, and MinIO backups. The PVC carries the Velero exclusion like the other
datastores. `CONFIG_SECRET`, the admin password hash, and the MinIO user keys
live as KMS-encrypted Pulumi stack config, so the Secrets they make need no
backup of their own.

## Considered Options

- **Velero disk snapshots.** A snapshot of a live SQLite file is not
  consistent, the recovery point depends on a cluster-wide schedule, and the
  cluster already excludes every datastore from Velero.
- **A state copy in a private MinIO bucket, carried by the MinIO backup.** No
  new service account, but the recovery point becomes the daily MinIO run.
- **Add Filestash to the `minio-backup` Job.** Two services would depend on
  one Job.
