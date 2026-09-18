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
| `restore-logical` aborts `archive sidecars are required` or `archive checksum mismatch` | A sidecar was not copied with the archive, or the archive changed after `dump-logical` wrote it (truncated copy, interrupted transfer). Both `.sha256` and `.metadata` are mandatory — the second carries the source and dump-client versions | Keep `<archive>`, `<archive>.sha256`, `<archive>.metadata` together; neither sidecar can be rebuilt from the archive, so re-run `just postgres dump-logical --source-cluster <name>`. Check by hand with `shasum -a 256 <archive>` against the sidecar ([details](logical-import.md#behavior--dump-logical)) |
| `dump-logical` aborts `source PostgreSQL version X is not supported by client Y` | The source server is a newer major than the `--client-image`; `pg_dump` reads older servers, never newer ones. Checked before the export, so no partial archive is left | Raise `--client-image` to the source major or newer — but it must still be ≤ the target major, because `restore-logical` rejects an archive written by a client newer than the target. A source newer than the target cannot be migrated this way at all ([details](logical-import.md#behavior--dump-logical)) |
| `restore-logical` aborts `target/client/archive PostgreSQL majors are incompatible (target=... dump-client=... client=...)` | The `--client-image` major does not **equal** the target's `properties.clusters[0].cluster.image.tag` major, the archive was written by a client newer than the restore client, or `image.tag` is unset/non-numeric on the stack | Pin `--client-image` to the target's major (default `postgres:16.15` for the PG 16 target) and confirm `image.tag` on the stack; if the archive's `client_version` is the newer one, re-dump with a client at or below the target major. Nothing has been mutated at this point ([details](logical-import.md#behavior--restore-logical)) |
| `restore-logical` aborts `archive metadata or client version is invalid (...)` | `.metadata` lacks a parsable `source_server_version` or `client_version` — typically an archive from a `dump-logical` that predates the `client_version` field, or a hand-edited sidecar | Re-run `just postgres dump-logical --source-cluster <name>`; the sidecar cannot be reconstructed from the archive ([details](logical-import.md#behavior--dump-logical)) |
| `restore-logical` aborts `pg_restore cannot read archive` | Not a custom-format archive this client can parse — wrong file passed to `--archive`, or an archive version the pinned `postgres:16.15` client does not understand. The checksum passes here: it proves the file is unchanged, not that it is loadable | Reproduce with `pg_restore --list <archive>` in the same image; if it fails there too, re-dump. The target is untouched — this check runs before any mutation ([details](logical-import.md#behavior--restore-logical)) |
| `restore-logical` aborts `target pods still present after 60 seconds; refusing to delete PVCs` | Pods labeled `cnpg.io/cluster=<name>` outlived the Cluster CR and the `jobRole=full-recovery` cleanup. The wait is a fixed 60s (30 × 2s) here — no `--retries`/`--interval` as in `reset-cluster` | The offending pods are printed and the PVCs were deliberately left intact. Clear the stuck pod (`kubectl get pods -n prod -l cnpg.io/cluster=<name>`, check node/volume state), then re-run `restore-logical` on the same archive ([details](logical-import.md#behavior--restore-logical)) |
| `restore-logical` aborts `--app-password is required before target mutation` | The target is always recreated, so the password is mandatory even when the target already exists | Re-run with `--app-password '<app-password>'`; it becomes the target owner role's password, replacing the source's ([details](logical-import.md#roles-and-acls-are-not-imported)) |
| `restore-logical` refuses: `target state requires --replace-data yes (existing Cluster, physical import config, or own backup archive)` | Guard against silently destroying target state. Trips on **any** of: the Cluster CR exists, `bootstrap.recovery`/`sourceSecret` are still on the stack, or this cluster's backup prefix holds objects | Confirm the target really is expendable, then re-run with `--replace-data yes` — it deletes the Cluster CR, its data PVCs, and this cluster's own backup archive, then recreates an empty target ([warnings](logical-import.md#warnings)) |
| `restore-logical` aborts `cannot safely inspect the target backup archive; backup bucket/path/key is incomplete` | Any one of `backup.bucket`, `backup.bucketPath`, `backupSecret.filepath` is missing from the stack — usually `configure-backup` never ran here — or the key file is not on disk. Checked before the backup prefix is listed: the recipe cannot prove the prefix is empty, and phase 2 has to clear that prefix anyway | Run `just postgres configure-backup` to set the three values and mint the key, then retry — nothing was mutated ([backup details](backup.md)) |
| `restore-logical` aborts with a raw `pulumi`, `kubectl`, or `gcloud storage` error during its pre-flight | The state probe fails closed by design. Only three literal replies mean "absent": whole output `error: configuration key '<path>' not found for stack '<stack>'` from `pulumi config get`, a line beginning `Error from server (NotFound):` from `kubectl get cluster`, and a whole line `ERROR: (gcloud.storage.ls) One or more URLs matched no objects.` on the backup prefix. Any other failure (expired credentials, wrong kube context, network) is propagated rather than read as "nothing there" | Fix the underlying access problem and re-run — the abort happened before any mutation, so the target is unchanged ([details](logical-import.md#behavior--restore-logical)) |
| `restore-logical` fails `local port 15433 is already in use` / `port-forward did not open` | A stale `kubectl port-forward` from an earlier run still holds the port, or the target Service has no ready endpoint | Kill the stale forward (`pkill -f 'port-forward.*15433'`) or pass `--port <free-port>`; if no endpoint, check the Cluster came up with `just postgres verify` |
| `restore-logical` ends with an empty target after `pg_restore` errors | `--single-transaction --exit-on-error`: one failing object (commonly a missing or untrusted extension) rolls the whole restore back by design | Read the first `pg_restore` error, fix it on the target (install the extension), then re-run `restore-logical` on the same archive — it is re-restorable ([warnings](logical-import.md#warnings)) |
| `configure-source` fails `cannot resolve the SOURCE GCP project id` | No `--source-project`/`--source-env-file`, and the probed `.env.*.<name>` lacks `PROJECT_ID` or the stack config has a legacy bucket name | Pass `--source-project <id>` explicitly |
| `reset-cluster` refuses: `no recovery source on stack` | Recovery source not configured before the reset | Run `just postgres configure-source --source-cluster <name>` first — resetting without it would bootstrap empty |
| `reset-cluster` times out deleting the Cluster CR | Stuck finalizer | `kubectl get cluster <name> -n prod -o jsonpath='{.metadata.finalizers}'` — resolve it before retrying |
| `reset-cluster` aborts `instance pods still present after N probes — PVCs left in place` | Pods labeled `cnpg.io/cluster=<name>` outlived the Cluster CR. The recipe deletes `jobRole=full-recovery` Jobs/pods first, so what remains is usually a pod stuck Terminating on a node or volume | Inspect with `kubectl get pods -n prod -l cnpg.io/cluster=<name>`, clear it, then re-run — the PVCs were deliberately not touched. Slow-but-healthy termination: raise `--retries`/`--interval` |
| `reset-cluster` or `restore-logical` aborts while clearing the own backup archive | The `gcloud storage rm` on `gs://<bucket>/<bucketPath>/<cluster>/` failed for a real reason (expired or wrong `postgres-backup-sa` key, revoked bucket IAM). Only a whole line equal to `ERROR: (gcloud.storage.rm) One or more URLs matched no objects.` is tolerated — a partial clear would surface later as `Expected empty archive` | Re-run `just postgres configure-backup` to remint the key and IAM, verify with `CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=<key> gcloud storage ls gs://<bucket>/<bucketPath>/<cluster>/`, then retry |
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
