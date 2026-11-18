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
| `no Velero namespace` | No `install-velero/Pulumi.<stack>.yaml` (for example `dcr-kube1`). | Pass `--velero-namespace <ns>`. |
| `fails its checks. Nothing changed` | The selected copy is damaged. | Run `restore-state --in-place yes --copy <older timestamp>`. |
| `these objects exist already` | A published key exists. | Use a new dataset name, or remove the file with `unpublish-file`. |
| `newest state copy age` in `verify` | The CronJob failed or Filestash was down. | Read the Job logs, then run `just filestash backup-now`. |
| Backup Job stays `Pending` | No Filestash pod runs, so the pod affinity has no node. | Start Filestash, then run `backup-now`. |
| `probe-admin: ... reached admin content` | A path variant passes the block. | Turn the route off and report the variant. |
| `Secret filestash is missing but pvc/filestash-state exists` | The Secret was deleted. | Restore Secret `filestash` from Velero. A new `CONFIG_SECRET` breaks `config.json`. |
| `Secret filestash-users is missing but MinIO user filestash-intake exists` | The Secret was deleted. | Restore Secret `filestash-users` from Velero. New keys stop every link. |
| `a backup job is running` | A state copy runs. | Wait for it, then run the recipe again. |
| `the restore job failed. Filestash stays at 0 replicas` | The copy is damaged or a check failed. | Read the Job logs, then run `restore-state --in-place yes --copy <older timestamp>`. |
| `state PVC use` above 80% | Logs, plugins, or copies fill the disk. | Run `just filestash resize-state --size <quantity>`. |
