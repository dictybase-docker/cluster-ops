# PostgreSQL Troubleshooting (CloudNativePG)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

| Symptom | Cause | Fix |
|---------|-------|-----|
| `deploy-operator` fails `namespaces "operators" not found` | `namespace-bootstrap` stack never applied on this cluster | Run `just gcp-pulumi apply-namespaces` ([pulumi setup §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces)) |
| `deploy-cluster` fails `cannot reference the cnpg-backup-plugin stack` | Barman Cloud plugin stack never applied | Run `just postgres deploy-backup-plugin` (composite tail of `configure-backup`; [plugin details](backup-plugin.md)) |
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` first, or pass `--stack <name>` |
| Instance pods Pending | No node matches `pool=database` + taint toleration, or pool missing | `just postgres check-pool`; see [pool requirements](pool-requirements.md) |
| `ImagePullBackOff` on instances | Bad operand tag in `Pulumi.dcr-kube1.yaml` | Use a `standard`/`minimal` flavor tag published on [GHCR](https://github.com/cloudnative-pg/postgres-containers#image-tags); tag must start with the major version. `system` is deprecated — do not use it |
| Cluster stuck in `Setting up primary` | Backup Secret `postgres-backup-credentials` missing or key file unreadable at `pulumi up` time | Re-run `just postgres configure-backup`; check `properties.backupSecret.filepath` on the stack |
| Backups fail with `storage.objects.create` denied | Wrong SA key (ArangoDB `backup-gcs-sa`), or IAM condition pinned to a different bucket name | Use the `postgres-backup-sa` key; re-run `configure-backup` if the bucket name changed — the condition is pinned to the exact bucket |
| `ScheduledBackup` rejected / never runs | Schedule not in six-field cron format | Use `0 0 0 * * *` (seconds first) — see [backup](backup.md#schedule-format) |
| App cannot connect | Wrong Service or Secret | Writes go to `<cluster>-rw`; password is in Secret `logto-app` (`kubectl get secret logto-app -n prod -o jsonpath='{.data.password}' \| base64 -d`) |
| Cluster came up empty, but the source data was wanted | `configure-source` never ran, or ran **after** `deploy-cluster` — `bootstrap.recovery` only fires at first-instance creation, so a later run changes nothing | Run `just postgres configure-source --source-cluster <name>`, then re-import with [`reset-cluster`](import.md#reset-re-import-into-a-running-cluster) — `deploy-cluster` alone will not re-import |
| Import finds no backup / recovery fails to start | `--source-cnpg-cluster` doesn't match the backup folder name, or reader SA/key not granted on the source bucket | The folder is `gs://<bucket>/<bucketPath>/<source-cnpg-cluster>/` — check with `GOOGLE_APPLICATION_CREDENTIALS=<reader-key> gsutil ls`. Re-run `configure-source` with the correct source CNPG cluster name |
| `configure-source` fails `cannot resolve the SOURCE GCP project id` | No `--source-project`/`--source-env-file`, and the probed `.env.*.<name>` lacks `PROJECT_ID` or the stack config has a legacy bucket name | Pass `--source-project <id>` explicitly |
| `reset-cluster` refuses: `no recovery source on stack` | Recovery source not configured before the reset | Run `just postgres configure-source --source-cluster <name>` first — resetting without it would bootstrap empty |
| `reset-cluster` times out deleting the Cluster CR | Stuck finalizer | `kubectl get cluster <name> -n prod -o jsonpath='{.metadata.finalizers}'` — resolve it before retrying |
| `config set` path errors on a fresh stack | `properties.clusters[0]` scaffold missing from `Pulumi.dcr-kube1.yaml` | Restore the template's `clusters:` list — the recipes address `clusters[0]` by index |
| PVCs left after teardown | Teardown run without `--delete-pvcs yes` | `kubectl delete pvc -n prod -l cnpg.io/cluster=<name>` |

## kubectl cnpg Plugin

Cluster-ops CLI for CloudNativePG. Install via [krew](https://krew.sigs.k8s.io/docs/user-guide/setup/install/) (or Homebrew: `brew install krew`, then add `~/.krew/bin` to `PATH`):

```bash
kubectl krew install cnpg
```

| Task | Command |
|------|---------|
| Health / node status | `kubectl cnpg status <cluster> -n prod` |
| `psql` shell on primary | `kubectl cnpg psql <cluster> -n prod` |
| On-demand backup | `kubectl cnpg backup <cluster> -n prod` |
| Rolling restart / config reload | `kubectl cnpg restart|reload <cluster> -n prod` |
| Diagnostics bundle | `kubectl cnpg report cluster <cluster> -n prod` |

Full list: `kubectl cnpg --help`.

## Reading Operator and Instance Logs

```bash
# Operator
kubectl logs -n operators -l app.kubernetes.io/name=cloudnative-pg --tail=100

# PostgreSQL instance (JSON logs on stdout)
kubectl logs -n prod <cluster>-1 --tail=100

# Cluster events
kubectl describe cluster.postgresql.cnpg.io <cluster> -n prod
```
