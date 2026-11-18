# Filestash Verify Details

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [just filestash verify](#just-filestash-verify)
  - [What It Does](#what-it-does)
  - [Behavior](#behavior)
  - [Flags](#flags)

## just filestash verify

### What It Does

Read-only checks. Each failed group prints `FAIL`, and the recipe exits non-zero at the end.

### Behavior

| Group | Check |
|---|---|
| Rollout | `deployment/filestash` is ready. |
| PVC | `pvc/filestash-state` is `Bound`, and its use is 80% or less. |
| HTTP (port-forward) | `/healthz` answers. `/about` lists each plugin in the lock file plus `plg_search_stateless` and `plg_image_c`. `/admin/tty/` returns 404. |
| Bucket policies | The anonymous policy on `downloads` equals `filestash/policies/downloads-anonymous.json`. `uploads` is private. |
| Snapshot age | The stack sets `backup.maxAgeHours`, and the newest successful state backup is younger than it. The check takes the newer of the last CronJob run and the last `backup-now` Job. |
| Backup Secret | Secret `filestash-backup-auth` exists. |
| Velero exclusion | `pvc/filestash-state` has the label `velero.io/exclude-from-backup=true`. The `restic` snapshots back up the state. Refer to [Backup and restore](backup-restore.md). |

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack` | No | `$PULUMI_STACK` | |
| `--namespace` | No | `properties.namespace` | A value that differs from the stack file stops the recipe. |
