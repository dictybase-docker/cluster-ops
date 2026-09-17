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
| Import finds no backup / recovery fails to start | `--source-cnpg-cluster` doesn't match the backup folder name, or reader SA/key not granted on the source bucket | The folder is `gs://<bucket>/<bucketPath>/<source-cnpg-cluster>/` — check with `GOOGLE_APPLICATION_CREDENTIALS=<reader-key> gcloud storage ls`. Re-run `configure-source` with the correct source CNPG cluster name |
| Re-import loops: `full-recovery` pods `Error`, `Expected empty archive` | This cluster's own backup archive still holds the previous incarnation's `base/` + `wals/` — CNPG requires an empty WAL-archive destination before a recovery | `reset-cluster` clears it; by hand: `CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=credentials/<project>/postgres-backup-sa.json gcloud storage rm -r gs://cloudnative-pg-backup-<project>/logto/logto/`, then re-run `reset-cluster` |
| Import instance/`full-recovery` pod exits `database files are incompatible with server` (`initialized by PostgreSQL version 14 ... not compatible with this version 16`) | Physical recovery across PostgreSQL majors — base backup + WAL only replays into the major that wrote it; the operator's 14–18 support is not cross-major compatibility | Not retryable, and no flag fixes it. Abandon §4/§6 for this source and migrate logically: [`dump-logical` + `restore-logical`](logical-import.md) |
| `restore-logical` aborts `archive checksum mismatch` or `checksum file not found` | The `.sha256` sidecar was not copied with the archive, or the archive changed after `dump-logical` wrote it (truncated copy, interrupted transfer) | Keep `<archive>`, `<archive>.sha256`, `<archive>.metadata` together; re-run `just postgres dump-logical --source-cluster <name>`. Check by hand with `shasum -a 256 <archive>` against the sidecar ([details](logical-import.md#behavior--dump-logical)) |
| `restore-logical` refuses: `target Cluster '<name>' already exists` | Guard against silently destroying a live target | Re-run with `--replace-data yes` — it deletes the Cluster CR, its data PVCs, and this cluster's own backup archive, then recreates an empty target ([warnings](logical-import.md#warnings)) |
| `restore-logical` fails `local port 15433 is already in use` / `port-forward did not open` | A stale `kubectl port-forward` from an earlier run still holds the port, or the target Service has no ready endpoint | Kill the stale forward (`pkill -f 'port-forward.*15433'`) or pass `--port <free-port>`; if no endpoint, check the Cluster came up with `just postgres verify` |
| `restore-logical` ends with an empty target after `pg_restore` errors | `--single-transaction --exit-on-error`: one failing object (commonly a missing or untrusted extension) rolls the whole restore back by design | Read the first `pg_restore` error, fix it on the target (install the extension), then re-run `restore-logical` on the same archive — it is re-restorable ([warnings](logical-import.md#warnings)) |
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
