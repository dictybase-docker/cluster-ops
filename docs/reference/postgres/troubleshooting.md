# PostgreSQL Troubleshooting (CloudNativePG)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` first, or pass `--stack <name>` |
| Instance pods Pending | No node matches `pool=database` + taint toleration, or pool missing | `just postgres check-pool`; see [pool requirements](pool-requirements.md) |
| `ImagePullBackOff` on instances | Bad operand tag in `Pulumi.prod.yaml` | Use a tag from the [official catalog](https://github.com/cloudnative-pg/postgres-containers/blob/main/Debian/ClusterImageCatalog-bookworm.yaml); tag must start with the major version |
| Cluster stuck in `Setting up primary` | Backup Secret `postgres-backup-credentials` missing or key file unreadable at `pulumi up` time | Re-run `just postgres configure-backup`; check `properties.backupSecret.filepath` on the stack |
| Backups fail with `storage.objects.create` denied | Wrong SA key (ArangoDB `backup-gcs-sa`), or IAM condition pinned to a different bucket name | Use the `postgres-backup-sa` key; re-run `configure-backup` if the bucket name changed — the condition is pinned to the exact bucket |
| `ScheduledBackup` rejected / never runs | Schedule not in six-field cron format | Use `0 0 0 * * *` (seconds first) — see [backup](backup.md#schedule-format) |
| App cannot connect | Wrong Service or Secret | Writes go to `<cluster>-rw`; password is in Secret `logto-app` (`kubectl get secret logto-app -n prod -o jsonpath='{.data.password}' \| base64 -d`) |
| Import finds no backup / recovery fails to start | `--source-cluster` doesn't match the backup folder name, or reader SA/key not granted on the source bucket | The folder is `gs://<bucket>/<bucketPath>/<sourceCluster>/` — check with `GOOGLE_APPLICATION_CREDENTIALS=<reader-key> gsutil ls`. Re-run `configure-source` with the correct source cluster name |
| `config set` path errors on a fresh stack | `properties.clusters[0]` scaffold missing from `Pulumi.prod.yaml` | Restore the template's `clusters:` list — the recipes address `clusters[0]` by index |
| PVCs left after teardown | Teardown run without `--delete-pvcs yes` | `kubectl delete pvc -n prod -l cnpg.io/cluster=<name>` |

## Reading Operator and Instance Logs

```bash
# Operator
kubectl logs -n operators -l app.kubernetes.io/name=cloudnative-pg --tail=100

# PostgreSQL instance (JSON logs on stdout)
kubectl logs -n prod <cluster>-1 --tail=100

# Cluster events
kubectl describe cluster.postgresql.cnpg.io <cluster> -n prod
```
