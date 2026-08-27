# Production ArangoDB on the Stateful Database Pool

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Shape and pool check](#1-shape-and-pool-check)
- [2. Install ArangoDB](#2-install-arangodb)
  - [2.1 Operator](#21-operator)
  - [2.2 Cluster instance](#22-cluster-instance)
  - [2.3 Logical databases](#23-logical-databases)
- [3. Import data](#3-import-data)
  - [3.1 Graph dumps](#31-graph-dumps)
  - [3.2 Content and UniProt](#32-content-and-uniprot)
- [4. Backup and restore](#4-backup-and-restore)
  - [4.1 Scheduled backup](#41-scheduled-backup)
  - [4.2 Restore drill](#42-restore-drill)
- [5. Teardown](#5-teardown)
- [6. Verify](#6-verify)
- [7. Troubleshooting](#7-troubleshooting)
- [8. Related documents](#8-related-documents)

**Status**: Production procedure. Production `Pulumi.prod.yaml` configs exist for `backup_secrets`, `arangodb-cluster`, `storage_class`, `arangodb-operator`, `create-arangodb-databases`, `arangodb-backup`, and `arangodb-restore`, deployed under whatever stack name `$PULUMI_STACK` resolves to for your cluster ([`pulumi-setup.md` §5.1](pulumi-setup.md#51-stack-names)). Do not edit lab `arangodb-single` stacks. Remaining gaps: production kOps bundle, loader prod stacks, PDBs.

> Provisioning guide for a production ArangoDB **Cluster** on kOps `stateful-db`. Related docs: [§8](#8-related-documents).

## Overview

This guide deploys ArangoDB Cluster 3.12 on an existing HA kOps cluster. Confirm the pool and member shape in [§1](#1-shape-and-pool-check), then apply Pulumi in [§2](#2-install-arangodb).

Work in `just cluster-env --env prod --cluster <prod-cluster>`. That shell sets `PULUMI_STACK` for you (defaults to `<prod-cluster>`); every recipe below picks it up automatically — no `--stack` flag needed unless you want to override it.

Each numbered step is **one `just arangodb ...` command** that configures, applies, and verifies its own stack. The only values you ever type are the ones no tool can invent: a namespace, and the four secrets (root password, application password, restic password, GCS key path).

## Quick Setup

1. Confirm Arango shape and `stateful-db` pool ([§1](#1-shape-and-pool-check)).
2. Operator → Cluster → logical DBs ([§2](#2-install-arangodb)).
3. Backup and restore drill ([§4](#4-backup-and-restore)).
4. Optional import ([§3](#3-import-data)) — no loader `Pulumi.prod.yaml` in repo yet.
5. Tear down on a clone only ([§5](#5-teardown)).

## 1. Shape and pool check

**ArangoDB** (`arangodb-cluster`, image `arangodb:3.12.10.1`, amd64, `externalAccess: None`):

| Role | Count | CPU | Memory | Disk |
|------|-------|-----|--------|------|
| Agents | 3 | 250m | 1Gi | 20Gi `dictycr-ssd` |
| DBServers | 3 | 2 | 12Gi | 150Gi `dictycr-balanced` |
| Coordinators | 3 | 500m | 2Gi | none |

Anti-affinity is **preferred** (hostname 100, zone 50), not required. No PDBs. Lab `arangodb-single` stays Single / 3.11 / arm64 — do not edit it. Use `Pulumi.prod.yaml` only.

**Kubernetes pool** — already installed if `config/kops/<prod-cluster>/instancegroups.yaml` `stateful-db` matches [README Step 3.2](../README.md#step-32--customize-yaml-in-git) **plus** the production taint. Starter has the pool; production adds `dedicated=database:NoSchedule`.

| Field | Expect |
|-------|--------|
| `machineType` | `n2-standard-4` (or `n2-highmem-4` if RAM is tight) |
| `minSize` / `maxSize` | `3` / `3`, zones `us-central1-a/b/c` |
| `nodeLabels.pool` | `database` |
| `taints` | `dedicated=database:NoSchedule` |

```bash
kubectl get nodes -l pool=database -o wide
```

Expect three Ready amd64 nodes, one per zone, each with that taint. Missing pool or taint: edit YAML and apply via [README Day-2](../README.md#4-day-2-operations-git-first-workflow). Do not use `dictycr-dev-staging-dcr-experiments`. HA sizing background: [`kops-gcp-architecture.md` §4.2](kops-gcp-architecture.md#2-statefuldatabase-node-pool-static).

Also needed before [§2](#2-install-arangodb): CSI + StorageClasses ([`pulumi-setup.md` §6](pulumi-setup.md#6-first-apply--storageclass)) and namespaces `prod` + `operators` + Secret `dictycr` (`gcsCredentials`, `gcsProject`, `resticPass`), all three from `backup_secrets` ([§4.1](#41-scheduled-backup)) — apply that stack **first**, before anything else in [§2](#2-install-arangodb), since `arangodb-operator` does not create its own `operators` namespace.

## 2. Install ArangoDB

Three Pulumi projects, applied **in order**: operator → cluster → logical databases. Each one is a single recipe that ensures the stack, writes any secret it needs, previews, applies, and then waits for the result — no manual `ensure-stack` / `preview` / `create-resource` sequences. Run from repo root with `just cluster-env --env prod --cluster <prod-cluster>` active, and only after [§1](#1-shape-and-pool-check) passes.

Every recipe below resolves its stack from `$PULUMI_STACK` or `--stack <name>` and **fails if neither is set** — there is no silent fallback to `dev`.

### 2.1 Operator

**What it does**: Installs the `kube-arangodb` Helm chart (pinned **1.2.42**, `arangodb-operator/Pulumi.prod.yaml`) into namespace `operators`. The operator watches for `ArangoDeployment` custom resources and manages the Cluster's pods, PVCs, and internal Service.

```bash
just arangodb deploy-operator
```

Applies the operator stack and blocks until the operator is actually serving.

- Runs `ensure-stack` → `preview` → `create-resource` on `arangodb-operator`, then waits for a ready `app.kubernetes.io/name=kube-arangodb` pod and asserts the `arangodeployments.database.arangodb.com` CRD exists.
- No secrets. `--namespace` defaults to `operators`; the Helm release does **not** create that namespace, so `backup_secrets` must already be applied ([§4.1](#41-scheduled-backup)).
- Wait budget: `--retries 60 --interval 10` (10 minutes) before it gives up and prints the pods.
- Touches nothing else — no Cluster CR, no databases, no backup.

### 2.2 Cluster instance

**What it does**: `arangodb-cluster/Pulumi.prod.yaml` creates the root-password Secret `arangodb-pass`, then an `ArangoDeployment` Cluster CR matching the shape in [§1](#1-shape-and-pool-check) — 3 agents, 3 dbservers, 3 coordinators, image `arangodb:3.12.10.1`, `externalAccess: None`, TLS `caSecretName: "None"` (plain HTTP, internal only).

```bash
just arangodb deploy-cluster --root-password '<strong-root-password>'
```

Sets the root password, applies the Cluster, and waits for all nine members.

- Runs `ensure-stack` → `set-secret properties.secret.password` → `preview` → `create-resource`, waits for `--members` (default 9) ready pods labelled `arango_deployment=arangodb`, then prints the coordinator Services.
- `--root-password` is required and never generated. It lands encrypted in the stack config and becomes Secret `arangodb-pass`, key `password` — the same secret [§4.1](#41-scheduled-backup) and [§4.2](#42-restore-drill) read.
- `--namespace` defaults to `prod` and must match `properties.namespace` in `Pulumi.prod.yaml`; the recipe only uses it to watch pods, it does not override the stack config.
- Wait budget: `--retries 90 --interval 10` (15 minutes). Full PVC/zone checks come later in [§6](#6-verify).
- Does not create the application user or any logical database.

**Coordinator address** — the next steps, imports, and app services all reach the database at:

```
http://arangodb.prod.svc.cluster.local:8529
```

If the operator names the Service differently, find it with `kubectl get svc -n prod -l arango_deployment=arangodb` and use that instead everywhere below.

### 2.3 Logical databases

**What it does**: `create-arangodb-databases/Pulumi.prod.yaml` creates Secret `backend` (keys `user`, `password`) and then a one-shot Job `backend-create-databases` (label `app=arangodb-create-databases`). Its init containers run `ensure-user` and one `ensure-database` per database; its main containers run one `ensure-grant` each, granting `rw` on `annotation`, `order`, `stock`, `content`, `cgm_ddb`, `chado`, `annofeature`. The `arangoadmin` containers are given no host flag in `main.go`, so the endpoint comes from the image's own default — in practice the in-namespace `arangodb` Service from [§2.2](#22-cluster-instance).

```bash
just arangodb create-databases --app-user '<app-user>' --app-password '<app-password>'
```

Writes both application credentials in one config call, applies, and waits for the Job.

- Runs `ensure-stack`, then a single `pulumi config set-all --path` writing `properties.arangodbSecret.user` and `.pass` as encrypted secrets, then `preview` → `create-resource`, then waits for the Job and prints Secret `backend`.
- **Both flags are required.** `Pulumi.prod.yaml` ships `name`/`userkey`/`passkey` but no `user`/`pass`, so applying without them would create Secret `backend` with empty credentials.
- Job name is derived from `properties.arangodbSecret.name` (`backend` in prod), matching `main.go`'s `"<name>-create-databases"`.
- The Job sets `ttlSecondsAfterFinished: 900`, so it vanishes 15 minutes after finishing; the recipe treats a Job it already observed as done if it disappears mid-wait. Default budget `--retries 60 --interval 10`.
- Does not touch the root password or the Cluster CR.

ArangoDB is now ready for application traffic; continue to [§3 Import data](#3-import-data) (optional) or [§4 Backup and restore](#4-backup-and-restore).

## 3. Import data

Optional. Needs Cluster Ready, databases created, production object storage. **No loader has a `Pulumi.prod.yaml` in this repo** — only `Pulumi.experiments.yaml` / `Pulumi.dev.yaml` / `Pulumi.staging.yaml`. Author the production stack config first; the recipe below refuses to run without it rather than applying a lab config to production.

### 3.1 Graph dumps

`arangodb-dataloader`: MinIO `export/chado` → `chado`, `export/cgm_ddb` → `cgm_ddb`. Namespace `prod`, host from [§2.2](#22-cluster-instance). Prefer `stateless-web`; add the database toleration only if the Job must sit on `stateful-db`.

```bash
just arangodb deploy-loader --folder arangodb-dataloader
```

One recipe for any loader project — config guard, apply, then a Job listing.

- Checks `<folder>/Pulumi.yaml` and `<folder>/Pulumi.<stack>.yaml` both exist, then runs `ensure-stack` → `preview` → `create-resource` and lists Jobs in `--namespace` (default `prod`).
- `--folder` is required; stack comes from `$PULUMI_STACK` or `--stack`, with no `dev` fallback.
- Exits with the "author it before deploying" error today, because `arangodb-dataloader/Pulumi.prod.yaml` does not exist.
- Does not wait on the import Job — loader run times vary by dataset; watch it with `kubectl logs -n prod job/<name> -f`.

### 3.2 Content and UniProt

Same recipe and same missing-config caveat for the other two loaders:

```bash
just arangodb deploy-loader --folder load-content-from-s3
```

- Swap `--folder load-uniprot-mapping` for the UniProt mapping import.
- Neither project has a `Pulumi.prod.yaml` either; `load-content-from-s3` ships `dev`, `experiments`, and `staging` configs only.

## 4. Backup and restore

`arangodb-backup/Pulumi.prod.yaml` provisions the GCS bucket and the Kubernetes Jobs that dump and upload backups. `arangodb-restore/Pulumi.prod.yaml` ([§4.2](#42-restore-drill)) runs the inverse entirely in-cluster — no laptop, no Docker, no port-forward — as one Job with an init container (`restic restore`) and a main container (`arangorestore`) sharing a scratch volume.

### 4.1 Scheduled backup

**What it does**: One Pulumi apply creates four things:

| Resource | Detail |
|----------|--------|
| GCS bucket `restic-arangodb-backup-prod` | Versioned, 58-day soft-delete retention, deletes ARCHIVED objects once 3 newer versions exist. **`ForceDestroy: true`** — see warning below. |
| Ephemeral PVC `arangodb-backup-ephemeral` | 150Gi `dictycr-balanced`, ReadWriteOnce. Recreated per pod run and deleted with it — scratch space for the dump, not a persistent archive. |
| Job `arangodb-backup-job` | Runs once immediately on `pulumi up`. Self-deletes 15 minutes after finishing (`ttlSecondsAfterFinished: 900`). Use it to prove the pipeline works without waiting for 02:00. |
| CronJob `arangodb-backup-cronjob` | Schedule `0 2 * * *`. This repo does not set `spec.timeZone`, so "02:00" means whatever timezone your kOps nodes report — verify it, do not assume UTC. Its triggered Jobs have **no** TTL and accumulate until you clean them up. |

The backup container (`dictybase/database-backup:sha-9c3b8ea`) runs `arangodb-backup --user root --password $(PASSWORD) --output arangodump --repository gs:restic-arangodb-backup-prod:/`, which internally does `arangodump --all-databases --include-system-collections` against coordinator `arangodb:8529` (short in-namespace DNS name, no override needed) into the ephemeral volume, then `restic backup` uploads that directory, initializing the restic repository on first run.

**Secrets it reads** (creates none of its own):

| Env var | Secret | Key | Created by |
|---------|--------|-----|------------|
| `PASSWORD` | `arangodb-pass` | `password` | `arangodb-cluster` ([§2.2](#22-cluster-instance)) — already applied by this point |
| `RESTIC_PASSWORD` | `dictycr` | `resticPass` | `backup_secrets` project (separate from `arangodb-backup`) |
| `GOOGLE_APPLICATION_CREDENTIALS` (mounted file) | `dictycr` | `gcsCredentials` | `backup_secrets` — this key name is itself a config value (`properties.secret.serviceAccount.keyname`), so it must be set to literally `gcsCredentials` for this to line up |
| `GOOGLE_PROJECT_ID` | `dictycr` | `gcsProject` | `backup_secrets` |

`backup_secrets/main.go` also creates namespaces `prod` and `operators`, from a **local file** on the machine running `pulumi up` (`properties.secret.serviceAccount.filepath`, a GCS-capable service account JSON key) plus `resticPass`/`gcsProject` config values — not from anything already in the cluster. `backup_secrets/Pulumi.prod.yaml` commits the non-secret fields; supply the rest and apply **before** anything else in [§2](#2-install-arangodb):

```bash
just arangodb configure-backup-secrets --restic-password '<restic repository password>' --gcs-project '<gcp-project-id>' --gcs-key-file credentials/<project-id>/backup-gcs-sa.json
```

One command for all four values, the apply, and the verification.

- Runs `ensure-stack`, one `pulumi config set-all --path --secret` for `resticPass`, `gcsProject`, `serviceAccount.keyname` and `serviceAccount.filepath`, then `preview` → `create-resource`, then prints both namespaces, Secret `dictycr`, and its data keys.
- All three flags required; nothing is invented. `--key-name` defaults to `gcsCredentials` and must stay that value, because `arangodb-backup` and `arangodb-restore` both look up `bucketSecret.key: gcsCredentials`.
- `--gcs-key-file` must exist on **this** machine and is stored as an absolute path: `main.go` reads it with `os.ReadFile` during `pulumi up`, and `pulumi -C <folder>` changes the working directory, so a relative path is ambiguous.
- Rotating any value is the same command again — it overwrites the config and re-applies the Secret.

Then apply the backup stack itself:

```bash
just arangodb deploy-backup
```

Applies bucket + CronJob + immediate Job, then proves the pipeline end to end.

- Runs `ensure-stack` → `preview` → `create-resource`, waits for Job `arangodb-backup-job` to succeed, tails 30 log lines, and prints the CronJob.
- Fails fast: if the Job reports `failed`, the recipe dumps the last 100 log lines and exits non-zero instead of waiting out the timeout. Budget `--retries 90 --interval 10` (15 minutes).
- The log tail should end with restic's backup summary. A first-run failure is never "repository already exists" — restic initializes the repo itself — so look for a secret-key typo or an `arangodump` auth error (root password mismatch, [§7](#7-troubleshooting)).
- The Job's own `ttlSecondsAfterFinished: 900` can delete it before you read the logs; the recipe says so instead of erroring.
- Does not create or verify Secret `dictycr` — that is `configure-backup-secrets` above.

> **Teardown warning**: because of `ForceDestroy: true`, `just gcp-pulumi remove-resource --folder arangodb-backup` deletes the GCS bucket **and every snapshot in it**, immediately, with no confirmation prompt. `just arangodb teardown` does **not** destroy that stack. Do not run `remove-resource` against a bucket you still need.

### 4.2 Restore drill

One in-cluster Job: init container `restic restore` → main container `arangorestore`, sharing an ephemeral scratch volume. [§4.1](#41-scheduled-backup)'s backup Job in reverse — no laptop download, no local Docker, no port-forward.

Run a drill against a **clone** cluster or scratch namespace. The same Job is the real disaster-recovery path when restoring in place after data loss; only the active kubeconfig/`$PULUMI_STACK` and the `namespace`/`server` you configure differ.

**Before step 1**, the target namespace needs a reachable coordinator — on a clone that means a fresh `arangodb-cluster` apply ([§2.1](#21-operator)–[§2.2](#22-cluster-instance)) there first — plus Secrets `arangodb-pass` and `dictycr`, which this stack reads and never creates (same refs as [§4.1](#41-scheduled-backup)'s table).

1. **Configure the stack** — one non-interactive command replaces `ensure-stack` plus a single `pulumi config set-all`:

   ```bash
   just arangodb configure-restore --namespace <target-namespace>
   ```

   `--namespace` is the only required flag; everything else is derived and printed as a summary before the config is written:

   | Config key | Derived by default | Override |
   |------------|--------------------|----------|
   | `namespace` | none — your call, no safe default | `--namespace <ns>` (required) |
   | `server` | first Service in that namespace labelled `arango_deployment=arangodb`, skipping per-member (`-agnt-`/`-crdn-`/`-prmr-`/`-sngl-`) and `-int`/`-ea` Services; falls back to literal `arangodb` **with a printed warning** if nothing matches or `kubectl` can't reach the cluster | `--server <svc>` |
   | `restoreId` | `drill-<UTC YYYYMMDD-HHMMSS>` — DNS-1123-safe and unique per run | `--restore-id <id>` |
   | `snapshot` | `latest` | `--snapshot <restic-snapshot-id>` |
   | `confirmTarget` | computed `<namespace>/<server>/<restoreId>` | never typed by hand, not a flag |
   | stack | `$PULUMI_STACK` from `cluster-env` | `--stack <name>` |

   Read the printed summary before moving on. Two things still need your judgment: the namespace, and whether the resolved `server` is really the coordinator you mean — if the fallback warning fired, discovery found nothing, so re-run with `--server`. For a point in time older than the last backup, get a snapshot id from step 3 and pass `--snapshot <id>`.

   > **Why the ceremony**: `arangodb-restore` refuses to build the Job unless `confirmTarget` equals `<namespace>/<server>/<restoreId>` exactly (`types.go`, `validateConfirmTarget`), so a stale or copy-pasted config cannot silently restore into the wrong target. The recipe computes that string from the values it just resolved — the gate is unchanged, only the double-typing is gone. It also rejects a `--restore-id` that isn't a DNS-1123 label or is over 46 chars, matching the Go-side limit that keeps the Job name `arangodb-restore-<restoreId>` under 63 chars. Reusing an id updates that same Job; a new id always creates a fresh one.

2. **Apply and follow it to the end:**

   ```bash
   just arangodb apply-restore
   ```

   Previews, applies, and follows both containers in order without you retyping the namespace or restore id.

   - Reads `namespace`, `server`, `restoreId` and `snapshot` back from the stack config, so it cannot drift from what step 1 wrote; missing values produce a "run configure-restore" error instead of a wrong restore.
   - Runs `preview` → `create-resource`, waits for the `restic-restore` init container to terminate `Completed`, prints its log, waits for the Job, then prints the `arangorestore` log and the scratch PVC.
   - Fails fast on a non-zero init container exit and dumps that container's log — `BackoffLimit: 0` means there is no retry to wait for.
   - Budget `--retries 180 --interval 10` (30 minutes per phase); raise it for a large dump.
   - Does not destroy the stack afterwards — see the end of this section.

3. **Wrong snapshot, or want an older point in time?** `restic-restore` failing usually means a wrong `snapshot` ID, a `dictycr` secret mismatch, or an empty bucket (backup never succeeded, [§4.1](#41-scheduled-backup)). List what is actually in the repository:

   ```bash
   just arangodb list-snapshots-in-cluster --namespace <target-namespace>
   ```

   Runs `restic snapshots` in-cluster with the same secret wiring the restore Job uses.

   - Starts a throwaway `restic/restic:0.17.0` pod (`--rm`, `restartPolicy: Never`) with `RESTIC_PASSWORD` from `dictycr.resticPass`, `GOOGLE_PROJECT_ID` from `dictycr.gcsProject`, and `gcsCredentials` mounted at `/var/secret/gcs-credentials` — restic's GCS backend needs a file path, not an inline value.
   - `--namespace` required (that is where Secret `dictycr` lives); `--bucket` defaults to `restic-arangodb-backup-prod`, `--secret` to `dictycr`, `--image` to `restic/restic:0.17.0`.
   - Needs no local `restic` and no kops state store. The laptop-side equivalent, if you have both, is `just arangodb list-restic-snapshots --bucket restic-arangodb-backup-prod --namespace <target-namespace>`.
   - Read-only — it never writes to or prunes the repository.

   Then re-run step 1 with `--snapshot <id>` and step 2 again.

4. **Re-run logical databases** ([§2.3](#23-logical-databases)) if application user `backend` or its grants did not come back with the restore — `arangorestore --create-database true` recreates databases and documents, not necessarily every grant.

5. **Record RPO/RTO.** The snapshot's timestamp (from the list in step 3) is your RPO. Wall-clock time from step 1 to a verified `Completed` Job is your RTO.

The Job and its scratch PVC self-delete `ttlSecondsAfterFinished: 3600` (1 hour) after finishing, so inspect logs before then. Tear the restore stack down once you're done — it's a one-off action, not a standing service:

```bash
just gcp-pulumi remove-resource --folder arangodb-restore
```

## 5. Teardown

Destructive. Clone only. `just arangodb teardown` destroys the Cluster, waits until it is gone, destroys the operator, then reports leftovers.

```bash
just arangodb teardown --namespace prod --delete-pvcs yes
```

- Order: `_destroy-stack` cluster → `_wait-gone` CR/pods/Services → `_destroy-stack` operator → `_wait-gone` operator pods → `_remove-pvcs` → `_report-clean`.
- Omit `--delete-pvcs yes` to leave leftover disks (warning, exit 0). `--namespace` required; stack is `$PULUMI_STACK`.
- Leaves backup, loaders, restore ([§4.2](#42-restore-drill)), `backup_secrets`, and `stateful-db` ([README §4](../README.md#4-day-2-operations-git-first-workflow) / [§6](../README.md#6-disposable-cluster-lifecycle)).

## 6. Verify

```bash
just arangodb verify
```

One read-only pass over pool, operator, storage, members and jobs; prints `PASS` / `FAIL` / `WARN` per check and exits non-zero if any required check fails.

- Checks: 3 `pool=database` nodes all carrying `dedicated=database:NoSchedule`; a Running operator pod in `operators`; StorageClasses `dictycr-balanced` and `dictycr-ssd`; `ArangoDeployment arangodb`; 9 ready member pods; 3 Bound 20Gi `dictycr-ssd` and 3 Bound 150Gi `dictycr-balanced` PVCs; a Service exposing 8529.
- Warns, does not fail, when the create-databases Job is absent (15-minute TTL — check Secret `backend` instead) or the backup CronJob is missing (only expected after [§4.1](#41-scheduled-backup)).
- Tunable for a non-default target: `--namespace`, `--operator-namespace`, `--members`, `--pool`, `--node-count`.
- Zone spread is **not** checked — anti-affinity is preferred, not required ([§1](#1-shape-and-pool-check)).
- Read-only: no Pulumi, no stack name, no writes.

**Pass:** the recipe prints `All required checks passed.` and exits 0.

## 7. Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| No `stateful-db` nodes | IG missing or wrong cluster | [§1](#1-shape-and-pool-check) + [README §3.2](../README.md#step-32--customize-yaml-in-git) |
| Pod Pending, taint | CR missing toleration | `arangodb-cluster/Pulumi.prod.yaml` vs node taint |
| Pod Pending, `arm64` | Lab Single | Wrong stack. Do not edit `Pulumi.dev.yaml` |
| Only one Arango pod | Applied `arangodb-single` | Use `arangodb-cluster` with `$PULUMI_STACK`, not the lab `dev`/`experiments` stacks |
| PVC Pending | No StorageClass / CSI | [`pulumi-setup.md`](pulumi-setup.md) |
| Members pile in one zone | Soft anti-affinity | Expected under pressure; [§1](#1-shape-and-pool-check) |
| Quorum loss after drain | No PDB | Drain via operator |
| Loader Pending after taint | Job has no toleration | Toleration or `stateless-web` |
| App cannot connect | Wrong host or secret | [§2.2](#22-cluster-instance) DNS + Secret `backend` |
| DB Job 401 | Root password mismatch | Secret `arangodb-pass` vs stack config |
| Backup permission error | Missing bucket or `dictycr` | [§4.1](#41-scheduled-backup) |
| Any recipe exits with "no stack name" | Not inside `just cluster-env`, so `$PULUMI_STACK` is unset | Enter the cluster shell, or pass `--stack <name>`. Deliberate: production recipes never fall back to `dev` |
| `arangodb-restore` apply fails, "confirmTarget must exactly equal" | Hand-edited config, or a stale `restoreId`/`namespace`/`server` combo left from a prior run | Re-run `just arangodb configure-restore --namespace <ns>` ([§4.2](#42-restore-drill)) — it recomputes all five values consistently. Do not weaken the check; it exists to stop restores into the wrong target |
| `restic-restore` init container fails | Wrong `snapshot` ID, `dictycr` mismatch, or bucket has no snapshots | List snapshots ([§4.2](#42-restore-drill) step 3); confirm [§4.1](#41-scheduled-backup) actually completed at least once |
| `arangorestore` container never starts | Init container still running or failed | `kubectl get pods -n <ns> -l restore-id=<id>` — initContainers must show `Completed` before the main container starts, that's Job ordering, not a bug |

## 8. Related documents

- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`README.md`](../README.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Implementation plan (historical): [`plans/arangodb-production.md`](plans/arangodb-production.md)
