# Filestash Troubleshooting

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

| Symptom | Cause | Fix |
|---|---|---|
| `filestash:properties contains placeholder in ...` | The stack file has `<...>` values. | Set the named keys with `just gcp-pulumi set-config --folder filestash`. Refer to [Stack Values to Set](install.md#stack-values-to-set). |
| `filestash:properties is invalid in backup.maxAgeHours` | The recovery point target is 0. | Set `properties.backup.maxAgeHours`. |
| `lock entries are not ready` | A lock entry has no checksum, license, or reviewer. | For office files, run `just filestash lock-office-deps`. For the other entries, do the [license and review procedure](plugins.md#license-and-review-procedure). |
| `checksum mismatch for <url>` | The vendor changed the file. | Review the new file, then update `sha256`. |
| `Couldn't send email` on an upload link | Staff did not set SMTP, or set a wrong value. | Set the `email` fields in the admin console. |
| Upload through a link fails with access denied | `filestash-intake` cannot finish the upload without `s3:GetObject`. | Do not widen the policy. Record the error and ask for a security review. |
| A link stopped after a key change | The link holds the old MinIO key. | Make the link again. Refer to [Key Rotation](users-and-policies.md#key-rotation). |
| `another run holds lease/<lease>` | A run is active, or a run crashed. | Wait, or run `just filestash release-lock --lease <lease> --holder <id>`. |
| `these objects exist already` | A published key exists. | Use a new dataset name, or remove the file with `unpublish-file`. |
| `CONFIG_SECRET in Secret filestash does not match the snapshot` | The stack has a different `CONFIG_SECRET` from the one in the snapshot. | Restore `filestash/Pulumi.<stack>.yaml` from Git, run `deploy`, then restore again. |
| `restic repository check failed (exit 12)` | Wrong `restic` password. | Restore `filestash-backup/Pulumi.<stack>.yaml` from Git. |
| `no share.sql` or `config.json has no general.secret_key` in a backup Job | Filestash never started, or its state is damaged. | Check the Filestash pod. Do not restore from a snapshot older than the damage. |
| `newest successful state backup age` in `verify` | The CronJob failed, or Filestash was down. | Read the Job logs, then run `just filestash backup-now`. |
| Backup Job fails after 30 minutes with no pod start | No Filestash pod runs, so the pod affinity has no node. | Start Filestash, then run `backup-now`. |
| `has no secrets.configSecret but pvc/filestash-state exists` | The stack file lost its encrypted values. | Restore `filestash/Pulumi.<stack>.yaml` from Git. A new `CONFIG_SECRET` breaks `config.json`. |
| `has no keys for <role> but MinIO user filestash-<role> exists` | The stack file lost the keys. | Restore the stack file from Git. New keys stop every link. |
| `snapshot '<id>' fails its checks. Nothing changed` | The snapshot is damaged. | List the ids with `just filestash list-snapshots`. Then run `restore-state --in-place yes --snapshot <older id>`. |
| `restic` error `wrong password or no key found` | The `restic` password in the stack file changed. | Restore `filestash-backup/Pulumi.<stack>.yaml` from Git. |
| `probe-admin: ... reached admin content` | A path variant passes the block. | Turn the route off and report the variant. |
| `a backup job is running` | A state copy runs. | Wait for it, then run the recipe again. |
| `the restore job failed. Filestash stays at 0 replicas` | The snapshot is damaged or a check failed. | Read the Job logs. List the ids with `just filestash list-snapshots`. Then run `restore-state --in-place yes --snapshot <older id>`. |
| `restic not found — install the restic CLI.` | `list-snapshots` needs a local `restic`. | Install the `restic` CLI on the operator host. |
| `routes.filestash.enabled=true but routes.filestash contains placeholder in hosts` | The Gateway route is on, but its host is still `<filestash-host>`. | Set `properties.routes.filestash.hosts.0` in the `gateway` stack, then run `just gateway deploy`. |
| `invalid --role '<role>' (intake, public, or operator).` | `login-keys` refuses the `publisher` keys and unknown roles. | Use `intake`, `public`, or `operator`. |
| `state PVC use` above 80% | Logs, plugins, or copies fill the disk. | Run `just filestash resize-state --size <quantity>`. |
