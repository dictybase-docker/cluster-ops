# PostgreSQL Import Details (Cross-Project CloudNativePG Recovery)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Imports data from **another cluster's** CloudNativePG backup into a freshly-created Cluster — the same `bootstrap.recovery` mechanism CNPG uses for in-place restore, pointed at a barman object store in a **different GCP project**. No dump files, no `pg_restore`, no extra Job: the operator replays the base backup + WAL straight from the source bucket into the new instance's PVC.

The source backup was written by the same CloudNativePG operator process (barman object store layout: base backups + WAL under `gs://<bucket>/<bucketPath>/<clusterName>/`), so the new cluster reads it natively.

**Physical, not logical.** The new instance inherits the source's on-disk data directory byte for byte, so the **source and target must run the same PostgreSQL major**. For a cross-major move (PG 14 source into a PG 16 target) this path cannot work at all — use [logical import](logical-import.md) instead. The two paths are mutually exclusive on one stack: `restore-logical` treats the config this recipe writes as target state it must destroy — it refuses to start without `--replace-data yes`, then removes `bootstrap.recovery` and `sourceSecret` unconditionally, whether or not they were set.

## Command

```bash
just postgres configure-source --source-cluster <cluster-name>
```

One flag for a source cluster standardized like this repo's own: the recipe resolves the source GCP project from `.env.<env>.<source-cluster>` (its `PROJECT_ID` line), falling back to the backup bucket name in the source stack config (`cloudnative-pg-backup-<project-id>` → project id), and defaults the CNPG cluster name and bucket path to `logto`. A source managed from another checkout needs `--source-project <id>` — the bucket then defaults to `cloudnative-pg-backup-<id>` — or `--source-env-file <path>` to an env file that contains `PROJECT_ID`.

Printed at the top of every run: the resolved project, CNPG cluster, bucket, reader SA, key file, PITR target if any, and where each inferred value came from.

## Source Pre-Flight

The recipe closes by printing this command with the resolved values filled in. Run it before `deploy-cluster` to confirm the reader key can actually see the source backups — a wrong `--source-cnpg-cluster` or a missing grant otherwise surfaces only as a recovery that never starts:

```bash
GOOGLE_APPLICATION_CREDENTIALS=<key-file> gcloud storage ls gs://<bucket>/<bucket-path>/<source-cnpg-cluster>/
```

Expect the barman object-store layout (base backups plus archived WAL) under that prefix. An empty listing or `AccessDenied` means the import would produce an unusable cluster.

## Behavior

1. Resolves the source identity: `--source-project` > `--source-env-file` > probe `.env.*.<source-cluster>` > bucket-name suffix in the source stack config. Probed env files must contain `PROJECT_ID`. Prod stack configs are named after the cluster (`Pulumi.dcr-kube1.yaml`), lab stacks after the env (cluster `dcr-experiments` → `Pulumi.experiments.yaml`) — both are probed. Nothing probed (e.g. a legacy bucket like the lab `dev` stack) → the run stops with the resolution error
2. Creates/reuses reader service account `postgres-source-reader` **in the source project** — source-side IAM runs with the source env file's `GOOGLE_APPLICATION_CREDENTIALS` when probed (the source cluster's own manager identity); otherwise the current identity must hold SA-admin + bucket-admin there
3. Grants `roles/storage.objectViewer` on the source bucket only (bucket-level `gcloud storage buckets add-iam-policy-binding` — the bucket already exists)
4. Mints `credentials/<source-project>/postgres-source-reader.json` — **skipped if the file already exists** (keys are not idempotent; old ones keep working until deleted). Audit: `gcloud iam service-accounts keys list --iam-account postgres-source-reader@<source-project>.iam.gserviceaccount.com --project <source-project>`
5. Stores the recovery source on the cluster stack: `properties.clusters[0].cluster.bootstrap.recovery.{sourceCluster,bucket,bucketPath}` and `properties.sourceSecret.{name,key,filepath}` — and removes a stale `targetTime` from a previous run when `--target-time` is omitted, so "latest" is never silently a PITR

The next `deploy-cluster` then:

1. Creates Secret `postgres-source-credentials` (content of the reader key, under `gcsCredentials`) plus a read-only **ObjectStore** CR for the source bucket, named `<source-cnpg-cluster>-store`
2. Adds an `externalClusters` entry named after the source CNPG cluster, referencing that ObjectStore via the Barman Cloud plugin config
3. Sets `bootstrap.recovery.source` to the same name — the first instance replays the source backup instead of running initdb

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--source-cluster` | One of the source flags | — | Source cluster name — the `.env.<env>.<name>` suffix (e.g. `dcr-experiments`). Probed for the source `PROJECT_ID` and the source stack config (cluster name, then `dcr-` stripped for lab stacks) |
| `--source-project` | One of the source flags | inferred | Source GCP project id owning the bucket. Required when the source is not managed from this checkout; bucket defaults to `cloudnative-pg-backup-<id>` |
| `--source-env-file` | One of the source flags | probed | Env file to read the source `PROJECT_ID` and manager credential from — must contain `PROJECT_ID`; use when the file lives outside the repo's `.env.*` naming |
| `--source-cnpg-cluster` | No | `logto` | Source CNPG `Cluster` name = the backup folder name. Override only if the source Cluster CR is not `logto` |
| `--bucket` | No | source stack config, else `cloudnative-pg-backup-<source-project>` | Source GCS bucket holding the barman backups |
| `--bucket-path` | No | `logto` | Source cluster's `backup.bucketPath` |
| `--target-time` | No | latest | RFC3339 PITR timestamp; omit for latest available |
| `--sa-name` | No | `postgres-source-reader` | Reader SA created in the source project |
| `--key-file` | No | `credentials/<source-project>/<sa-name>.json` | Read at `pulumi up` time |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## Why a Separate Reader SA

The `postgres-backup-sa` (from [backup](backup.md)) has objectAdmin pinned by IAM condition to **this** cluster's bucket — it cannot read the source bucket, and widening its condition would give a writer key read access to another project's data. The reader SA is source-project-local, read-only, and discarded after the import.

## Service Account Pairs — Do Not Mix

| SA | Lives in | Scope | Used by |
|----|----------|-------|---------|
| `postgres-backup-sa` | this project | objectAdmin on `cloudnative-pg-backup-<project-id>` | ongoing backup + WAL archive |
| `postgres-source-reader` | source project | objectViewer on the source bucket | one-shot import |
| `backup-gcs-sa` | this project | objectAdmin on the ArangoDB restic bucket | ArangoDB only — never PostgreSQL |

## One-Shot Bootstrap

Recovery **is** the bootstrap. Once the first instance exists, `deploy-cluster` re-runs are no-ops for data — the operator does not re-import. To re-import into a running cluster, use [reset-cluster](#reset-re-import-into-a-running-cluster); a full [teardown](teardown.md) also works. To stop referencing the source after a successful import, remove the `bootstrap.recovery` block from the stack config (`pulumi config rm --path properties.clusters[0].cluster.bootstrap.recovery`).

## Reset: Re-import Into a Running Cluster

`bootstrap.recovery` only fires at first-instance creation, so a running cluster cannot re-import in place — the data must be reset first. `reset-cluster` is the surgical form: it clears this cluster's own backup archive (base + WAL), deletes the Cluster CR and data PVCs, refreshes Pulumi state, and the next `deploy-cluster` bootstraps from the recovery source again. The operator, backup bucket, Secrets, and ScheduledBackup stay — but the cluster's own backups must go, because CNPG aborts a recovery whose WAL-archive destination still holds a previous incarnation's `base/` and `wals/` (`Expected empty archive`).

```bash
just postgres reset-cluster --reset-data yes --app-password '<app-password>'
```

Behavior:

1. Refuses to run unless the recovery source is already on the stack (set by [configure-source](#command)) — a reset without it would bootstrap EMPTY, not re-import. Aborts equally when the Cluster CR does not exist (a fresh deploy wants `deploy-cluster` directly)
2. Clears this cluster's own backup archive — `gs://<backup.bucket>/<bucketPath>/<cluster>/`, removed with the backup SA key — so `bootstrap.recovery` starts on an empty WAL-archive destination instead of aborting with `Expected empty archive`. The same helper backs [`restore-logical`](logical-import.md#behavior--restore-logical), which calls it on every run. It needs all three of bucket, bucket path, and key filepath on the stack plus the key file on disk, and it fails closed: only a prefix that holds nothing (`matched no objects` / `does not exist`) is tolerated, while a permission or network failure aborts rather than leaving a half-cleared archive for the recovery to trip over
3. Deletes the Cluster CR (300s timeout; a timeout points at a stuck finalizer), then removes leftover `cnpg.io/jobRole=full-recovery` Jobs/pods from earlier failed recoveries — they carry the cluster label and would otherwise keep the pod wait below spinning until it gives up
4. Waits until no `cnpg.io/cluster=<cluster>` pod remains (`--retries` × `--interval`, default 30 × 10s = 5 min; `restore-logical` runs the same guard on a fixed 60s budget). Still present at the end: the PVCs are left in place and the reset aborts
5. Deletes the data PVCs, then `pulumi refresh --yes` — always — so the next apply recreates the Cluster CR instead of diffing a phantom
6. Chains `deploy-cluster` when `--app-password` is given; otherwise prints the re-import command

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--reset-data` | Yes | — | Must be `yes` — deletes the Cluster CR and every data PVC in it |
| `--app-password` | No | — | Chain `deploy-cluster` after the reset with this app password |
| `--cluster` | No | `logto` | Must match `properties.clusters[0].cluster.name` |
| `--namespace` | No | `prod` | Namespace holding the Cluster |
| `--retries` | No | `30` | Instance pod removal probe attempts |
| `--interval` | No | `10` | Seconds between probes |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## After the Import

- Databases/roles come over as they were in the source. The `logto-app` Secret still applies: `managed.roles` reconciles the owner role's password to the Secret, so the app password is the one you passed to `deploy-cluster --app-password`, not the source cluster's old one.
- Verify with `just postgres verify`, then connect to `logto-rw.prod.svc.cluster.local:5432`.

## Warnings

- **Run order matters.** `configure-source` before `deploy-cluster`. If the Cluster already exists, the import cannot retro-apply — [reset it](#reset-re-import-into-a-running-cluster) first.
- **Same major, not "any supported major".** This operator can *run* PostgreSQL 14–18, but that is the range of majors it supports, not a compatibility range for recovery. A base backup + WAL set only replays into the major that wrote it: a PG 14 backup recovered into a PG 16 cluster never starts — the instance exits with `database files are incompatible with server` (`The data directory was initialized by PostgreSQL version 14, which is not compatible with this version 16`). Cross-major migrations go through [logical import](logical-import.md).
- **Operator version floor.** Independently of the major, the source backup must come from a CloudNativePG version this operator (1.30.x) can read — any 1.x barman object store works.
- **Key hygiene.** The reader key grants read on another project's backups. Delete it when the import is done if it is not needed for a repeat.
- **A logical restore un-configures this path.** [`restore-logical`](logical-import.md) strips `bootstrap.recovery` and `sourceSecret` from the stack. Re-run `configure-source` before any later `reset-cluster`, which aborts with `no recovery source on stack` otherwise.
