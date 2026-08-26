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

**Status**: Production procedure. Production `Pulumi.prod.yaml` configs exist for `arangodb-cluster`, `storage_class`, `arangodb-operator`, `create-arangodb-databases`, and `arangodb-backup`, deployed under whatever stack name `$PULUMI_STACK` resolves to for your cluster ([`pulumi-setup.md` §5.1](pulumi-setup.md#51-stack-names)). Do not edit lab `arangodb-single` stacks. Remaining gaps: production kOps bundle, loader prod stacks, PDBs.

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

Also needed before [§2](#2-install-arangodb): CSI + StorageClasses ([`pulumi-setup.md` §6](pulumi-setup.md#6-first-apply--storageclass)), namespace `prod`, and (for backup, [§4.1](#41-scheduled-backup)) Secret `dictycr` with `gcsCredentials`, `gcsProject`, `resticPass` — provisioned by the separate `backup_secrets` Pulumi project, which has **no `Pulumi.prod.yaml` yet**. Author one before [§4.1](#41-scheduled-backup).

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

`arangodb-backup/Pulumi.prod.yaml` provisions the GCS bucket and the Kubernetes Jobs that dump and upload backups. There is **no** restore Pulumi program — restoring is always a manual drill ([§4.2](#42-restore-drill)) using `restic` and `arangorestore` against a separate clone, never the live database.

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

`backup_secrets/main.go` also creates the target namespace and namespace `operators`, from a **local file** on the machine running `pulumi up` (`properties.secret.serviceAccount.filepath`, a GCS-capable service account JSON key) plus plaintext `resticPass`/`gcsProject` config values — not from anything already in the cluster. **It has `Pulumi.experiments.yaml` but no `Pulumi.prod.yaml`.** Author one (namespace `prod`, `keyname: gcsCredentials`, a production GCS service account key file, and a generated restic password) and apply it before this stack:

```bash
just gcp-pulumi ensure-stack --folder backup_secrets
just gcp-pulumi preview --folder backup_secrets
just gcp-pulumi create-resource --folder backup_secrets
kubectl get secret dictycr -n prod
```

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

Run this on a **clone** cluster or scratch namespace — never the live production database. You need `restic` and either a local ArangoDB/`arangorestore` install or Docker.

1. **Fetch the backup credentials from the production cluster** (read-only; does not touch the live database). This repo already has a recipe for this — it fetches `kubeconfig` via `kops export kubeconfig`, reads Secret `dictycr` from the given namespace, and restores the latest restic snapshot to a local directory:

   ```bash
   just arangodb restore-latest-snapshot \
     --bucket restic-arangodb-backup-prod \
     --namespace prod \
     --output-dir ./restore-scratch
   ```

   Run `just cluster-env --env prod --cluster <prod-cluster>` first so `kops export kubeconfig` targets the right cluster. Result: `./restore-scratch/arangodump/` on your machine.

2. **Stand up an empty target.** On the clone/scratch cluster, apply `storage_class`, `arangodb-operator`, and `arangodb-cluster` ([§2.1](#21-operator)–[§2.2](#22-cluster-instance)) so there is a coordinator to restore into. Note the root password you set in that step — you need it in step 4. This is a fresh install; it does **not** reuse production's `arangodb-pass` unless you are restoring in place into the same cluster after a real data-loss event (not a drill).

3. **Port-forward the target coordinator:**

   ```bash
   kubectl port-forward -n <namespace> svc/<coordinator-service> 8529:8529
   ```

4. **Run `arangorestore`** against that port-forward, pointing at the `arangodump` directory from step 1 (macOS needs `--add-host`; drop it and use `127.0.0.1` on Linux, matching the OS branching this repo's local restore recipe already uses):

   ```bash
   docker run --rm --add-host=host.docker.internal:host-gateway \
     -v "$(pwd)/restore-scratch/arangodump:/dump" \
     arangodb/arangodb:3.12.10.1 \
     arangorestore \
       --server.endpoint tcp://host.docker.internal:8529 \
       --server.username root \
       --server.password "<target-cluster-root-password>" \
       --input-directory /dump \
       --all-databases true \
       --include-system-collections \
       --create-database true
   ```

5. **Re-run logical databases** ([§2.3](#23-logical-databases)) if application user `backend` or its grants did not come back with the restore.

6. **Record RPO/RTO.** `restic snapshots` (from step 1's underlying command) shows the snapshot timestamp — that is your RPO. Wall-clock time from step 1 to a verified-working coordinator is your RTO.

## 5. Teardown

Destructive. Snapshot first. Disposable clone only. Never `dev` / `experiments` / `local`.

### 5.1 Reverse destroy order

All default to `$PULUMI_STACK`; pass `--stack <name>` only if this shell's cluster differs from the one you are tearing down.

```bash
just gcp-pulumi remove-resource --folder load-uniprot-mapping
just gcp-pulumi remove-resource --folder load-content-from-s3
just gcp-pulumi remove-resource --folder arangodb-dataloader
just gcp-pulumi remove-resource --folder arangodb-backup
just gcp-pulumi remove-resource --folder create-arangodb-databases
just gcp-pulumi remove-resource --folder arangodb-cluster
just gcp-pulumi remove-resource --folder arangodb-operator
```

Skip stacks you never created. Operator last, only if no `ArangoDeployment` remains.

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

## 8. Related documents

- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`README.md`](../README.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Implementation plan (historical): [`plans/arangodb-production.md`](plans/arangodb-production.md)
