# ArangoDB Troubleshooting

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| No `stateful-db` nodes | IG missing or wrong cluster | [Pool requirements](pool-requirements.md) + [README §3](../../kops-setup.md#3-cluster-bootstrap-git-native-flow) |
| Pod Pending, taint | CR missing toleration | `arangodb-cluster/Pulumi.prod.yaml` vs node taint |
| Pod Pending, `arm64` | Lab Single | Wrong stack. Do not edit `Pulumi.dev.yaml` |
| Only one Arango pod | Applied `arangodb-single` | Use `arangodb-cluster` with `$PULUMI_STACK`, not lab stacks |
| PVC Pending | No StorageClass / CSI | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Members pile in one zone | Soft anti-affinity | Expected under pressure; see [pool requirements](pool-requirements.md) |
| Quorum loss after drain | No PDB | Drain via operator |
| Loader Pending after taint | Job has no toleration | Add toleration or use `stateless-web` |
| App cannot connect | Wrong host or secret | [Cluster details](cluster.md) DNS + Secret `backend` |
| DB Job 401 | Root password mismatch | Secret `arangodb-pass` vs stack config |
| Backup permission error | Missing bucket or `dictycr` | [Backup details](backup.md) |
| Recipe exits with "no stack name" | Not inside `just cluster-env`, so `$PULUMI_STACK` unset | Enter cluster shell, or pass `--stack <name>`. Deliberate: prod recipes never fall back to `dev` |
| `arangodb-restore` apply fails, "confirmTarget must exactly equal" | Hand-edited config, or stale `restoreId`/`namespace`/`server` | Re-run `just arangodb configure-restore --namespace <ns>` ([restore details](restore.md)). Do not weaken check |
| `restic-restore` init container fails | Wrong `snapshot` ID, `dictycr` mismatch, or empty bucket | List snapshots ([restore details](restore.md)); confirm backup completed at least once |
| `arangorestore` container never starts | Init container still running or failed | `kubectl get pods -n <ns> -l restore-id=<id>` — init must show `Completed` first (Job ordering, not bug) |

## Cross-Project Bootstrap (deploy guide §4)

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| restic `403` / `accessDenied` while **locking** | Read-only source SA cannot write a restic lock file | `noLock=true` must be set — use `configure-bootstrap`/`bootstrap-from-snapshot`, not `configure-restore` |
| restic `403` / `accessDenied` while **reading** | Source SA lacks `roles/storage.objectViewer` on the source bucket, or `dictycr-source` `gcsProject` holds **this** project's id | Re-check the bucket IAM binding in the source project; `gcsProject` must be the **source** project id |
| restic `wrong password` | `dictycr-source` was given this cluster's restic password | Re-run `configure-source-secrets` with the **source** repository password |
| restic `repository does not exist` | Wrong `--bucket`, or the source repo uses a path prefix so the URI is not `gs:<bucket>:/` | Confirm with `just arangodb list-source-snapshots --bucket <b>`; check the source backup's actual repository URI |
| `snapshot not found`, or recipe rejects the id | Typo, or `latest` / non-hex value | `just arangodb list-source-snapshots ...` and pass a full id (8–64 lowercase hex) |
| `confirmTarget must exactly equal` during bootstrap | Hand-edited stack config | Re-run `just arangodb configure-bootstrap ...` |
| `create-databases` 401 after bootstrap | Restored `_users` replaced `root`, so Secret `arangodb-pass` no longer matches | Run `just arangodb reset-root-password`, then re-run `create-databases` |
| `configure-source-secrets` fails: namespace `prod` missing | `source_backup_secrets` creates no namespaces by design | Run `just arangodb configure-backup-secrets ...` first ([§2](../../arangodb-deploy.md#2-prerequisites)) |
| `deploy-backup` aborts naming `dictycr-source` | Backup stack still points at the read-only bootstrap identity | Set the three secret names back to `dictycr`; never back up through the source identity |
| A later DR drill reads the **source** bucket | Bootstrap overlay was never reset | `just arangodb reset-restore-config` (`bootstrap-from-snapshot` does this from a `trap`) |
| Restored graphs disappear or get overwritten | Loader Jobs run after a successful bootstrap | Skip loaders unless a database was genuinely absent from the dump ([import details](import.md)) |
| Scratch volume runs out of space | Dump larger than `properties.storage.size` (150Gi) | Raise `properties.storage.size` for the bootstrap run |
| Failed restore, retry behaves oddly | Previous attempt left a dirty scratch volume | Job uses `BackoffLimit: 0`; a new `restoreId` gets a fresh ephemeral PVC — read the logs before retrying |
| `403` that mentions org policy or VPC Service Controls | Source project blocks GCS access from foreign identities or from this VPC | Needs an org/network exception — not a recipe bug |
