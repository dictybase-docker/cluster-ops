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
  - [5.1 Reverse destroy order](#51-reverse-destroy-order)
  - [5.2 Disks and the node pool](#52-disks-and-the-node-pool)
- [6. Verify](#6-verify)
- [7. Troubleshooting](#7-troubleshooting)
- [8. Related documents](#8-related-documents)

**Status**: Production procedure. Production `Pulumi.prod.yaml` configs exist for `backup_secrets`, `arangodb-cluster`, `storage_class`, `arangodb-operator`, `create-arangodb-databases`, `arangodb-backup`, and `arangodb-restore`, deployed under whatever stack name `$PULUMI_STACK` resolves to for your cluster ([`pulumi-setup.md` §5.1](pulumi-setup.md#51-stack-names)). Do not edit lab `arangodb-single` stacks. Remaining gaps: production kOps bundle, loader prod stacks, PDBs.

> Provisioning guide for a production ArangoDB **Cluster** on kOps `stateful-db`. Related docs: [§8](#8-related-documents).

## Overview

This guide deploys ArangoDB Cluster 3.12 on an existing HA kOps cluster. Confirm the pool and member shape in [§1](#1-shape-and-pool-check), then apply Pulumi in [§2](#2-install-arangodb).

Work in `just cluster-env --env prod --cluster <prod-cluster>`. That shell sets `PULUMI_STACK` for you (defaults to `<prod-cluster>`); every `just gcp-pulumi` command below uses it automatically — no `--stack` flag needed unless you want to override it.

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

Three Pulumi projects, applied **in order**: operator → cluster → logical databases. Each follows the same four steps — `ensure-stack` (select or init `$PULUMI_STACK`), set a secret if one is required, preview, apply. Run all commands from repo root with `just cluster-env --env prod --cluster <prod-cluster>` active, and only after [§1](#1-shape-and-pool-check) passes.

### 2.1 Operator

**What it does**: Installs the `kube-arangodb` Helm chart (pinned **1.2.42**, `arangodb-operator/Pulumi.prod.yaml`) into namespace `operators`. The operator watches for `ArangoDeployment` custom resources and manages the Cluster's pods, PVCs, and internal Service.

**No secrets to set for this stack.**

```bash
just gcp-pulumi ensure-stack --folder arangodb-operator
just gcp-pulumi preview --folder arangodb-operator
just gcp-pulumi create-resource --folder arangodb-operator
```

**Verify before continuing:**

```bash
kubectl get pods -n operators -l app.kubernetes.io/name=kube-arangodb
kubectl get crd arangodeployments.database.arangodb.com
```

Both must exist and the operator pod must be Running before [§2.2](#22-cluster-instance).

### 2.2 Cluster instance

**What it does**: `arangodb-cluster/Pulumi.prod.yaml` creates the root-password Secret `arangodb-pass`, then an `ArangoDeployment` Cluster CR matching the shape in [§1](#1-shape-and-pool-check) — 3 agents, 3 dbservers, 3 coordinators, image `arangodb:3.12.10.1`, `externalAccess: None`, TLS `caSecretName: "None"` (plain HTTP, internal only).

**Set the root password first** — this is the one secret this stack needs:

```bash
just gcp-pulumi ensure-stack --folder arangodb-cluster
just gcp-pulumi set-secret --folder arangodb-cluster --key properties.secret.password --value "<strong-root-password>"
```

**Preview and apply:**

```bash
just gcp-pulumi preview --folder arangodb-cluster
just gcp-pulumi create-resource --folder arangodb-cluster
```

**Verify:**

```bash
kubectl get arangodeployment -n prod arangodb
kubectl get pods -n prod -l "arango_deployment=arangodb" -o wide
```

Wait until all 9 pods are Running before [§2.3](#23-logical-databases). Full PVC/zone checks: [§6](#6-verify).

**Coordinator address** — the next steps, imports, and app services all reach the database at:

```
http://arangodb.prod.svc.cluster.local:8529
```

If the operator names the Service differently, find it with `kubectl get svc -n prod -l arango_deployment=arangodb` and use that instead everywhere below.

### 2.3 Logical databases

**What it does**: `create-arangodb-databases/Pulumi.prod.yaml` runs a one-shot Job (`backend-create-databases`, label `app=arangodb-create-databases`) against the coordinator address from [§2.2](#22-cluster-instance). It creates application user Secret `backend`, then creates and grants `rw` on seven databases: `annotation`, `order`, `stock`, `content`, `cgm_ddb`, `chado`, `annofeature`.

**Set the application user's password first:**

```bash
just gcp-pulumi ensure-stack --folder create-arangodb-databases
just gcp-pulumi set-secret --folder create-arangodb-databases --key properties.arangodbSecret.pass --value "<app-password>"
```

**Preview and apply:**

```bash
just gcp-pulumi preview --folder create-arangodb-databases
just gcp-pulumi create-resource --folder create-arangodb-databases
```

**Verify:**

```bash
kubectl get jobs -n prod -l app=arangodb-create-databases
kubectl get secret -n prod backend
```

The Job must show `Completed`. ArangoDB is now ready for application traffic; continue to [§3 Import data](#3-import-data) (optional) or [§4 Backup and restore](#4-backup-and-restore).

## 3. Import data

Optional. Needs Cluster Ready, databases created, production object storage. No loader `Pulumi.prod.yaml` in repo — author when importing.

### 3.1 Graph dumps

`arangodb-dataloader`: MinIO `export/chado` → `chado`, `export/cgm_ddb` → `cgm_ddb`. Namespace `prod`, host from [§2.2](#22-cluster-instance). Prefer `stateless-web`; add the database toleration only if the Job must sit on `stateful-db`.

```bash
just gcp-pulumi ensure-stack --folder arangodb-dataloader
just gcp-pulumi preview --folder arangodb-dataloader
just gcp-pulumi create-resource --folder arangodb-dataloader
```

### 3.2 Content and UniProt

Same rule for `load-content-from-s3` and `load-uniprot-mapping`.

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

`backup_secrets/main.go` also creates namespaces `prod` and `operators`, from a **local file** on the machine running `pulumi up` (`properties.secret.serviceAccount.filepath`, a GCS-capable service account JSON key) plus plaintext `resticPass`/`gcsProject` config values — not from anything already in the cluster. `backup_secrets/Pulumi.prod.yaml` commits the non-secret fields; set the rest once via `set-secret` (commands are in that file's header comment), then apply it **before** anything else in [§2](#2-install-arangodb):

```bash
just gcp-pulumi ensure-stack --folder backup_secrets
just gcp-pulumi set-secret --folder backup_secrets --key properties.secret.resticPass --value "<restic repository password>"
just gcp-pulumi set-secret --folder backup_secrets --key properties.secret.gcsProject --value "<gcp-project-id>"
just gcp-pulumi set-secret --folder backup_secrets --key properties.secret.serviceAccount.keyname --value "gcsCredentials"
just gcp-pulumi set-secret --folder backup_secrets --key properties.secret.serviceAccount.filepath --value "credentials/<project-id>/backup-gcs-sa.json"
just gcp-pulumi preview --folder backup_secrets
just gcp-pulumi create-resource --folder backup_secrets
kubectl get secret dictycr -n prod
```

Then apply the backup stack itself:

```bash
just gcp-pulumi ensure-stack --folder arangodb-backup
just gcp-pulumi preview --folder arangodb-backup
just gcp-pulumi create-resource --folder arangodb-backup
```

**Verify:**

```bash
kubectl get jobs -n prod arangodb-backup-job
kubectl logs -n prod job/arangodb-backup-job
kubectl get cronjobs -n prod arangodb-backup-cronjob
```

The Job must show `Completed`; the log tail should end with restic's backup summary. If it fails on the first run, that is not "repository already exists" (the recipe initializes it automatically) — look for a secret-key typo or an `arangodump` auth error (root password mismatch, [§7](#7-troubleshooting)).

> **Teardown warning**: because of `ForceDestroy: true`, `just gcp-pulumi remove-resource --folder arangodb-backup` deletes the GCS bucket **and every snapshot in it**, immediately, with no confirmation prompt. Do not run it against a bucket you still need ([§5.1](#51-reverse-destroy-order)).

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

2. **Preview and apply:**

   ```bash
   just gcp-pulumi preview --folder arangodb-restore
   just gcp-pulumi create-resource --folder arangodb-restore
   ```

3. **Watch both containers in order** — the init container must complete before the main one starts:

   ```bash
   kubectl get pods -n <target-namespace> -l restore-id=<restoreId> -w
   kubectl logs -n <target-namespace> job/arangodb-restore-<restoreId> -c restic-restore
   kubectl logs -n <target-namespace> job/arangodb-restore-<restoreId> -c arangorestore
   ```

   `restic-restore` failing usually means a wrong `snapshot` ID, a `dictycr` secret mismatch, or an empty bucket (backup never succeeded, [§4.1](#41-scheduled-backup)). To list available snapshots, run restic in-cluster with the same secret wiring the restore Job uses (`RESTIC_PASSWORD` from `dictycr.resticPass`, credentials mounted as a file — restic's GCS backend needs a path, not an inline value):

   ```bash
   kubectl run restic-list --rm -it --restart=Never -n <target-namespace> \
     --image restic/restic:0.17.0 \
     --overrides='{"spec":{"containers":[{"name":"restic-list","image":"restic/restic:0.17.0","args":["-r","gs:restic-arangodb-backup-prod:/","snapshots"],"env":[{"name":"RESTIC_PASSWORD","valueFrom":{"secretKeyRef":{"name":"dictycr","key":"resticPass"}}},{"name":"GOOGLE_PROJECT_ID","valueFrom":{"secretKeyRef":{"name":"dictycr","key":"gcsProject"}}},{"name":"GOOGLE_APPLICATION_CREDENTIALS","value":"/var/secret/gcs-credentials"}],"volumeMounts":[{"name":"gcs-credentials","mountPath":"/var/secret","readOnly":true}]}],"volumes":[{"name":"gcs-credentials","secret":{"secretName":"dictycr","items":[{"key":"gcsCredentials","path":"gcs-credentials"}]}}]}}'
   ```

   With `restic` installed locally and a kops state store in the shell, `just arangodb list-restic-snapshots --bucket restic-arangodb-backup-prod --namespace <target-namespace>` does the same thing from your laptop. Either way, re-run step 1 with `--snapshot <id>`, then re-apply.

4. **Verify:**

   ```bash
   kubectl get job -n <target-namespace> arangodb-restore-<restoreId>
   kubectl get pvc -n <target-namespace> -l type=arangodb-restore-scratch
   ```

   Job must show `Completed`. The Job and its scratch PVC self-delete `ttlSecondsAfterFinished: 3600` (1 hour) after finishing — inspect logs before then if something looks off.

5. **Re-run logical databases** ([§2.3](#23-logical-databases)) if application user `backend` or its grants did not come back with the restore — `arangorestore --create-database true` recreates databases and documents, not necessarily every grant.

6. **Record RPO/RTO.** The snapshot's timestamp (from the `restic snapshots` list in step 3) is your RPO. Wall-clock time from step 1 to a verified `Completed` Job is your RTO.

Tear the restore stack down once you're done inspecting it — it's a one-off action, not a standing service:

```bash
just gcp-pulumi remove-resource --folder arangodb-restore
```

## 5. Teardown

Destructive. Snapshot first. Disposable clone only. Never `dev` / `experiments` / `local`.

### 5.1 Reverse destroy order

All default to `$PULUMI_STACK`; pass `--stack <name>` only if this shell's cluster differs from the one you are tearing down. `arangodb-restore` is a one-off action, not a standing service — remove it as soon as you're done inspecting a drill/restore, independent of a full teardown ([§4.2](#42-restore-drill)):

```bash
just gcp-pulumi remove-resource --folder arangodb-restore
just gcp-pulumi remove-resource --folder load-uniprot-mapping
just gcp-pulumi remove-resource --folder load-content-from-s3
just gcp-pulumi remove-resource --folder arangodb-dataloader
just gcp-pulumi remove-resource --folder arangodb-backup
just gcp-pulumi remove-resource --folder create-arangodb-databases
just gcp-pulumi remove-resource --folder arangodb-cluster
just gcp-pulumi remove-resource --folder arangodb-operator
```

Skip stacks you never created. Operator last, only if no `ArangoDeployment` remains. `backup_secrets` is foundational (namespaces + `dictycr`) and is intentionally not in this list — only remove it if you are decommissioning the whole cluster.

### 5.2 Disks and the node pool

Operator often **keeps** PVCs after CR delete.

```bash
kubectl get pvc -n prod -l arango_deployment=arangodb
kubectl delete pvc -n prod -l arango_deployment=arangodb   # only if data may go
```

Remove `stateful-db` only if nothing else should stay: [README §4](../README.md#4-day-2-operations-git-first-workflow). Full cluster teardown: [README §6](../README.md#6-disposable-cluster-lifecycle).

## 6. Verify

```bash
kubectl get nodes -l pool=database -o wide
kubectl get nodes -l pool=database -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.taints}{"\n"}{end}'
kubectl get pods -n operators -l app.kubernetes.io/name=kube-arangodb
kubectl get sc dictycr-balanced dictycr-ssd
kubectl get arangodeployment -n prod arangodb -o yaml
kubectl get pods -n prod -l "arango_deployment=arangodb" -o wide
kubectl get pvc -n prod -l "arango_deployment=arangodb"
kubectl get svc -n prod -l "arango_deployment=arangodb"
kubectl get jobs -n prod -l app=arangodb-create-databases
kubectl get cronjobs,jobs -n prod arangodb-backup-cronjob arangodb-backup-job
```

Pass: 3 tainted `pool=database` nodes; operator Ready; both StorageClasses; 9 members Running; agent PVCs 20Gi `dictycr-ssd` and dbserver PVCs 150Gi `dictycr-balanced` Bound; coordinator :8529; DB Job Complete; backup CronJob present if that stack was applied. Zone spread is preferred, not guaranteed.

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
| `arangodb-restore` apply fails, "confirmTarget must exactly equal" | Hand-edited config, or a stale `restoreId`/`namespace`/`server` combo left from a prior run | Re-run `just arangodb configure-restore --namespace <ns>` ([§4.2](#42-restore-drill)) — it recomputes all five values consistently. Do not weaken the check; it exists to stop restores into the wrong target |
| `restic-restore` init container fails | Wrong `snapshot` ID, `dictycr` mismatch, or bucket has no snapshots | List snapshots ([§4.2](#42-restore-drill) step 3); confirm [§4.1](#41-scheduled-backup) actually completed at least once |
| `arangorestore` container never starts | Init container still running or failed | `kubectl get pods -n <ns> -l restore-id=<id>` — initContainers must show `Completed` before the main container starts, that's Job ordering, not a bug |

## 8. Related documents

- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`README.md`](../README.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Implementation plan (historical): [`plans/arangodb-production.md`](plans/arangodb-production.md)
