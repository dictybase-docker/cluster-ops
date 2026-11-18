# Filestash Troubleshooting

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

| Symptom | Cause | Fix |
|---|---|---|
| `filestash:properties contains placeholder in ...` | The stack file has `<...>` values. | Set the named keys with `just gcp-pulumi set-config --folder filestash`. |
| `filestash:properties is invalid in backup.maxAgeHours` | The recovery point target is 0. | Set `properties.backup.maxAgeHours`. |
| `lock entries are not ready` | A lock entry has no checksum, license, or reviewer. | Fill the entry. For office files, run `just filestash lock-office-deps`. |
| `checksum mismatch for <url>` | The vendor changed the file. | Review the new file, then update `sha256`. |
| `Couldn't send email` on an upload link | SMTP is not set or is wrong. | Set the `email` fields in the admin console. |
| Upload through a link fails with access denied | `filestash-intake` cannot finish the upload without `s3:GetObject`. | Do not widen the policy. Record the error and ask for a security review. |
| A link stopped after a key change | The link holds the old MinIO key. | Make the link again. Refer to [Key Rotation](users-and-policies.md#key-rotation). |
| `another run holds lease/<lease>` | A run is active, or a run crashed. | Wait, or run `just filestash release-lock --lease <lease> --holder <id>`. |
| `these objects exist already` | A published key exists. | Use a new dataset name, or remove the file with `unpublish-file`. |
| `newest successful state backup age` in `verify` | The CronJob failed, or Filestash was down. | Read the Job logs, then run `just filestash backup-now`. |
| Backup Job fails after 30 minutes with no pod start | No Filestash pod runs, so the pod affinity has no node. | Start Filestash, then run `backup-now`. |
| `has no secrets.configSecret but pvc/filestash-state exists` | The stack file lost its encrypted values. | Restore `filestash/Pulumi.<stack>.yaml` from Git. A new `CONFIG_SECRET` breaks `config.json`. |
| `has no keys for <role> but MinIO user filestash-<role> exists` | The stack file lost the keys. | Restore the stack file from Git. New keys stop every link. |
| `snapshot '<id>' fails its checks. Nothing changed` | The snapshot is damaged. | Run `restore-state --in-place yes --snapshot <older id>`. List the ids with `restic snapshots`. |
| `restic` error `wrong password or no key found` | The `restic` password in the stack file changed. | Restore `filestash-backup/Pulumi.<stack>.yaml` from Git. |
| `probe-admin: ... reached admin content` | A path variant passes the block. | Turn the route off and report the variant. |
| `a backup job is running` | A state copy runs. | Wait for it, then run the recipe again. |
| `the restore job failed. Filestash stays at 0 replicas` | The snapshot is damaged or a check failed. | Read the Job logs, then run `restore-state --in-place yes --snapshot <older id>`. |
| `state PVC use` above 80% | Logs, plugins, or copies fill the disk. | Run `just filestash resize-state --size <quantity>`. |
