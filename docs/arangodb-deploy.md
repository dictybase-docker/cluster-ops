# Production ArangoDB on the Stateful Database Pool

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Scope and freeze](#1-scope-and-freeze)
  - [1.1 What this guide is](#11-what-this-guide-is)
  - [1.2 Do not touch existing stacks](#12-do-not-touch-existing-stacks)
  - [1.3 What exists today](#13-what-exists-today)
- [2. Production shape from the architecture](#2-production-shape-from-the-architecture)
  - [2.1 Highly Available plan](#21-highly-available-plan)
  - [2.2 Stateful database pool](#22-stateful-database-pool)
  - [2.3 Why Cluster mode](#23-why-cluster-mode)
  - [2.4 Storage and backups](#24-storage-and-backups)
- [3. Prerequisites](#3-prerequisites)
  - [3.1 Production cluster](#31-production-cluster)
  - [3.2 Pulumi backend](#32-pulumi-backend)
  - [3.3 Tools and access](#33-tools-and-access)
- [4. Prepare the stateful-db pool](#4-prepare-the-stateful-db-pool)
  - [4.1 Target manifest](#41-target-manifest)
  - [4.2 Isolation](#42-isolation)
  - [4.3 Apply and validate](#43-apply-and-validate)
- [5. Install ArangoDB](#5-install-arangodb)
  - [5.1 Operator](#51-operator)
  - [5.2 Production instance](#52-production-instance)
  - [5.3 Logical databases](#53-logical-databases)
- [6. Import data](#6-import-data)
  - [6.1 Graph dumps](#61-graph-dumps)
  - [6.2 Content and UniProt](#62-content-and-uniprot)
- [7. Backup and restore](#7-backup-and-restore)
  - [7.1 Scheduled backup](#71-scheduled-backup)
  - [7.2 Restore drill](#72-restore-drill)
- [8. Teardown](#8-teardown)
  - [8.1 Reverse destroy order](#81-reverse-destroy-order)
  - [8.2 Disks and the node pool](#82-disks-and-the-node-pool)
- [9. Verify](#9-verify)
- [10. Troubleshooting](#10-troubleshooting)
- [11. Related documents](#11-related-documents)

**Status**: Production procedure. Current `dev` / `experiments` / `local` ArangoDB stacks are **frozen**. Code to make this path real is in [`plans/arangodb-production.md`](plans/arangodb-production.md) — not implemented yet.

> **Document type:** Provisioning guide for a **production** ArangoDB Cluster on the kOps **stateful/database** pool. Cluster sizing comes from [`kops-gcp-architecture.md`](kops-gcp-architecture.md). Cluster bootstrap is [`README.md`](../README.md). Shared Pulumi backend / StorageClass is [`pulumi-setup.md`](pulumi-setup.md).

## Overview

DictyCR production needs a graph database that survives a zone loss. The architecture doc’s **Highly Available plan** puts databases on a **static** 3-zone pool. ArangoDB production therefore means:

| Layer | Production choice | Why |
|-------|-------------------|-----|
| Cluster topology | Private VPC, 3 control planes, Cilium | [`kops-gcp-architecture.md`](kops-gcp-architecture.md) §§3, 7, 8 |
| Database pool | 3× `n2-standard-4` (or `n2-highmem-4`), static, 100 GB `pd-balanced` boot | Architecture §4.2 |
| ArangoDB mode | **Cluster** (3 agents + 3 dbservers + 3 coordinators) | Architecture §6 Option B — engine-level HA, not a single replica |
| Image | `arangodb:3.12.10.1` (amd64) | Latest 3.12 patch; 3.11 is EOL |
| Placement | `nodeSelector: pool=database` + taint `dedicated=database:NoSchedule` | Isolate DB from web/spot |
| PVC | `dictycr-balanced`, one volume **per member** | Architecture §6 — no shared disk |
| Cloud SQL | **Not used** | Architecture Option A is PostgreSQL/MySQL only |

- **Who it's for**: Operators standing up the **production** DictyCR cluster.
- **What you end up with**: ArangoDB Cluster on `stateful-db`, app databases, optional imports, restic backups, a teardown path.
- **What you must not do**: Edit `arangodb-single/Pulumi.dev.yaml`, `Pulumi.experiments.yaml`, or `Pulumi.local.yaml`. Leave today’s lab stacks alone.

## Quick Setup

Work only against the **production** cluster env (`just cluster-env --env prod --cluster <prod-cluster>` or the name you choose).

1. Confirm the production cluster matches [§2](#2-production-shape-from-the-architecture).
2. Confirm StorageClass + Pulumi backend ([`pulumi-setup.md`](pulumi-setup.md)).
3. Isolate `stateful-db` ([§4](#4-prepare-the-stateful-db-pool)).
4. After the code in [`plans/arangodb-production.md`](plans/arangodb-production.md) exists: install operator → Cluster → logical DBs ([§5](#5-install-arangodb)).
5. Import if needed ([§6](#6-import-data)).
6. Turn on backup and run a restore drill ([§7](#7-backup-and-restore)).
7. Tear down with [§8](#8-teardown) on a disposable prod-like clone — never as the first test on the live site.

Until the plan is implemented, stop after §4. You can size and isolate the pool today. You cannot Pulumi-apply a production Cluster from this repo yet.

## 1. Scope and freeze

### 1.1 What this guide is

End-to-end **production** lifecycle: pool → operator → ArangoDB Cluster 3.12 → databases → import → backup → teardown.

It is **not** a change log for the existing Single-mode lab.

### 1.2 Do not touch existing stacks

These files stay as they are:

| File | Role now |
|------|----------|
| `arangodb-single/Pulumi.dev.yaml` | Lab Single, **3.11.6**, 75Gi, no pool pin |
| `arangodb-single/Pulumi.experiments.yaml` | Lab Single, **3.11.6**, 150Gi |
| `arangodb-single/Pulumi.local.yaml` | Local Single, **3.11.6**, 20Gi, arm64 |
| `arangodb-single/main.go` | Hardcodes `Mode: Single`, `architecture: arm64` |
| `create-arangodb-databases/Pulumi.experiments.yaml` / `Pulumi.local.yaml` | Lab DB bootstrap |

Production gets **new** stack files (proposed name `Pulumi.prod.yaml`) and, if needed, a new project. See the [plan](plans/arangodb-production.md).

### 1.3 What exists today

| Fact | Implication |
|------|-------------|
| `arangodb-single` is Single-only | Cannot express Cluster in current Pulumi program |
| Generated CRD types omit `agents` / `dbservers` / `coordinators` | Cluster CR cannot be typed in Pulumi until CRDs are regenerated |
| Starter `stateful-db` has label `pool: database`, **no taint** | Isolation is incomplete until production IG is edited |
| `config/kops/dictycr-dev-staging-dcr-experiments/` has **no** `stateful-db` | That 2024 cluster is not the production target |
| Operator chart in lab stacks is `kube-arangodb` **1.2.42** | Supports 3.12; latest chart is 1.4.x — decide in the plan, do not bump lab |

## 2. Production shape from the architecture

Source: [`kops-gcp-architecture.md`](kops-gcp-architecture.md) Highly Available plan (§§3–8).

### 2.1 Highly Available plan

```
[ Custom GCP VPC, private topology, Cloud NAT, Cilium ]
        │
        ├── 3 × e2-standard-2     control plane   (one per zone, etcd on pd-ssd)
        ├── 3–6 × e2-standard-4   stateless-web   (elastic)
        ├── 3 × n2-standard-4     stateful-db     (static)   ← ArangoDB lives here
        └── 0–4 × e2-standard-4   batch-spot      (optional)
```

Do **not** use the architecture’s Cost-Optimized plan for production ArangoDB. That plan is a consolidated pool + single replica. Architecture §8 calls that low-to-moderate zone tolerance.

### 2.2 Stateful database pool

| Knob | Production value | Source |
|------|------------------|--------|
| Machine | `n2-standard-4` (4/16) default; `n2-highmem-4` (4/32) if indexes must stay in RAM | Architecture §4.2 |
| Count | **3 / 3**, static | Architecture §4.2 — no scale-to-zero |
| Zones | `us-central1-a`, `b`, `c` | Architecture §3 / §4 |
| Boot disk | 100 GB `pd-balanced` | Architecture §4.2 |
| Data disks | Separate PVC per Arango member, `dictycr-balanced` | Architecture §6 |
| Label | `pool: database` | Starter IG + this guide |
| Taint | `dedicated=database:NoSchedule` | Isolate from web/spot (architecture §4.3 pattern) |

`n2-standard-4` × 3 is the architecture default. Cluster mode runs **9** members (3+3+3). If CPU/RAM pressure shows after first deploy, step the pool to `n2-highmem-4` (same count) rather than adding nodes. Adding nodes is allowed only if you also raise dbserver/coordinator counts and keep odd quorum.

### 2.3 Why Cluster mode

Architecture §6 Option B: production self-hosted HA is an **operator-managed replica set**, one PVC per replica, anti-affinity across zones, PDB, backups.

For ArangoDB 3.12:

| Mode | Survives node loss? | This repo today |
|------|---------------------|-----------------|
| Single | No | Lab only (`arangodb-single`) |
| ActiveFailover | Removed in 3.12 | Do not use |
| Cluster (3/3/3) | Yes, if members spread | **Production target** — needs new code |

Cloud SQL (architecture Option A) is not an ArangoDB option.

### 2.4 Storage and backups

- StorageClass: `dictycr-balanced` (GCE `pd-balanced` via CSI). Enable `pdCSIDriver` on the cluster.
- One PVC per agent / dbserver. Coordinators are stateless; still pin them to the pool.
- Community 3.12 binaries are under the ArangoDB Community / BSL license. Confirm current license terms before the dataset grows; do not assume a free unlimited disk.
- Backups: `arangodump` + restic → GCS, plus restore drills (architecture §6).

Suggested first-cut PVC sizes (tune after measuring data):

| Member | PVC |
|--------|-----|
| Agent × 3 | 8Gi each |
| DBServer × 3 | 150Gi each (match today’s experiments Single disk as a floor) |
| Coordinator × 3 | none |

## 3. Prerequisites

### 3.1 Production cluster

Finish [`README.md`](../README.md) through [Step 3.7](../README.md#step-37--validate-ha-topology--hardening) for a **new production** cluster, using the HA numbers in [§2.1](#21-highly-available-plan).

Required before ArangoDB:

- Private topology + Cloud NAT + Cilium
- 3-zone control plane
- `stateful-db` InstanceGroup present (starter template has the skeleton)
- `pdCSIDriver.enabled: true`
- `kubectl get nodes` healthy

Do **not** bolt this onto `dictycr-dev-staging-dcr-experiments`. That bundle has only a control plane and `nodes-us-central1-c`.

### 3.2 Pulumi backend

[`pulumi-setup.md`](pulumi-setup.md) §§3–5, then Phase A StorageClass. Production state lives in the **production** GCS backend and KMS key — not the devenv-194321 lab key.

### 3.3 Tools and access

Same baseline as the kOps guide: `just`, `kubectl`, `pulumi`, `gcloud`, `jq`.

| Need | Why |
|------|-----|
| Production kubeconfig | Apply CRs |
| `pulumi-manager` in the **prod** project | State + secrets |
| Rights to create GCE PDs | PVCs |
| MinIO (imports only) | Loaders |
| GCS + restic secrets (backup) | `arangodb-backup` |

## 4. Prepare the stateful-db pool

This step is Git/kOps only. It does not change Pulumi lab stacks.

### 4.1 Target manifest

In `config/kops/<prod-cluster>/instancegroups.yaml`, `stateful-db` should look like the architecture pool:

```yaml
apiVersion: kops.k8s.io/v1alpha2
kind: InstanceGroup
metadata:
  labels:
    kops.k8s.io/cluster: ${KOPS_CLUSTER_NAME}
  name: stateful-db
spec:
  image: ubuntu-os-cloud/ubuntu-2204-jammy-v20240829
  role: Node
  machineType: n2-standard-4    # or n2-highmem-4
  minSize: 3
  maxSize: 3
  rootVolumeSize: 100
  subnets:
  - us-central1
  zones:
  - us-central1-a
  - us-central1-b
  - us-central1-c
  taints:
  - dedicated=database:NoSchedule
  nodeLabels:
    pool: database
```

Starter template already has the machine, count, zones, and label. Production adds the **taint**. Do that in the **production** bundle only. Do not edit `config/kops/_starter/` until you intend every new cluster to inherit the taint.

### 4.2 Isolation

| Field | Value | Effect |
|-------|-------|--------|
| `nodeLabels.pool` | `database` | Selector target |
| `taints` | `dedicated=database:NoSchedule` | Only tolerated pods land here |

After the taint, CNPG / Redis / MinIO / loaders will **not** schedule on this pool unless those programs also get the toleration. That is intended for a dedicated DB pool. Plan those programs separately; do not weaken the taint.

### 4.3 Apply and validate

```bash
# 1. Edit the production bundle
$EDITOR config/kops/<prod-cluster>/instancegroups.yaml

# 2. Commit, push to state, apply (README §4)
git commit -am "<prod-cluster>: isolate stateful-db for production ArangoDB"
just gcp-cluster replace-manifests --cluster <prod-cluster>
just gcp-cluster plan-cluster --cluster <prod-cluster>
just gcp-cluster update-cluster --cluster <prod-cluster>
just gcp-cluster rolling-update \
  --cluster <prod-cluster> \
  --instance-group stateful-db \
  --yes yes
```

Taint/machine changes need a rolling update. `minSize`/`maxSize` changes do not.

```bash
kubectl get nodes -l pool=database -o wide
kubectl get nodes -l pool=database -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.taints}{"\n"}{end}'
```

Expect three Ready nodes, one per zone, each with `dedicated=database:NoSchedule`. **amd64** (`n2`).

## 5. Install ArangoDB

Commands below assume [`plans/arangodb-production.md`](plans/arangodb-production.md) is done: a production stack exists and the program can emit Cluster + placement.

Until then, treat this section as the **target procedure**, not something you can `pulumi up` today.

Set once:

```bash
STACK=prod
```

Do **not** set `STACK=dev` or copy over `Pulumi.dev.yaml`.

### 5.1 Operator

Helm `kube-arangodb` in `operators`. Lab uses 1.2.42 (3.12-capable). Production should pin a chart version in **new** `arangodb-operator/Pulumi.prod.yaml` only.

```bash
just gcp-pulumi new-stack --folder arangodb-operator --stack prod
# fill production properties; do not --from-stack dev
just gcp-pulumi preview --folder arangodb-operator --stack prod
just gcp-pulumi create-resource --folder arangodb-operator --stack prod
kubectl get pods -n operators | grep -i arango
kubectl get crd | grep arangodb
```

Wait for Ready and `arangodeployments.database.arangodb.com`.

### 5.2 Production instance

Target CR (operator will accept this; Pulumi cannot emit it until the plan lands):

- `spec.mode: Cluster`
- `spec.image: arangodb:3.12.10.1`
- `spec.architecture: [amd64]`
- 3 agents / 3 dbservers / 3 coordinators
- Each group: `nodeSelector.pool=database` + taint toleration
- Agents 8Gi PVC, dbservers 150Gi PVC, StorageClass `dictycr-balanced`
- Anti-affinity so members spread across zones
- PDB so voluntary drains cannot evict quorum
- TLS/bootstrap secrets created in the **prod** stack, not reused from lab

After the program exists:

```bash
just gcp-pulumi new-stack --folder <arangodb-prod-project> --stack prod
just gcp-pulumi preview --folder <arangodb-prod-project> --stack prod
just gcp-pulumi create-resource --folder <arangodb-prod-project> --stack prod
kubectl get arangodeployment -n <prod-ns>
kubectl get pods -n <prod-ns> -l "arango_deployment=arangodb" -o wide
```

Nine members, all on `pool=database` nodes, spread across three zones.

### 5.3 Logical databases

Same names the lab job already knows: `annotation`, `order`, `stock`, `content`, `cgm_ddb`, `chado`, `annofeature`. Grant `rw`.

Use a **new** `create-arangodb-databases/Pulumi.prod.yaml`. Do not copy encrypted lab secrets. Point the Job at the Cluster coordinator Service.

```bash
just gcp-pulumi new-stack --folder create-arangodb-databases --stack prod
just gcp-pulumi preview --folder create-arangodb-databases --stack prod
just gcp-pulumi create-resource --folder create-arangodb-databases --stack prod
kubectl get jobs -n <prod-ns> | grep create-databases
```

Job must Complete.

## 6. Import data

Optional. Needs Cluster Ready, databases created, MinIO in production.

Loaders are one-shot (`backoffLimit: 0`). Run them from **new prod stacks**. Do not reuse `experiments` encrypted MinIO secrets.

### 6.1 Graph dumps

`arangodb-dataloader`: MinIO `export/chado` → `chado`, `export/cgm_ddb` → `cgm_ddb`.

```bash
just gcp-pulumi new-stack --folder arangodb-dataloader --stack prod
just gcp-pulumi preview --folder arangodb-dataloader --stack prod
just gcp-pulumi create-resource --folder arangodb-dataloader --stack prod
```

Give the Job the database toleration or it will not schedule after [§4.2](#42-isolation).

### 6.2 Content and UniProt

`load-content-from-s3` and `load-uniprot-mapping` — same rule: new `Pulumi.prod.yaml`, production secrets, toleration if they must run on `stateful-db` (prefer stateless-web if they are CPU-only).

## 7. Backup and restore

### 7.1 Scheduled backup

`arangodb-backup`: CronJob 02:00 + first Job on apply. `arangodump` → restic → production GCS bucket.

New `arangodb-backup/Pulumi.prod.yaml`. Larger ephemeral PVC than lab 20Gi if dbservers are 150Gi.

Architecture §6: scheduled backups are not enough — prove restore.

### 7.2 Restore drill

No restore Pulumi program exists. On a **clone** cluster:

1. Empty Cluster (or new PVCs).
2. One-off pod, same backup image + restic/GCS secrets.
3. `restic restore latest`.
4. `arangorestore` into the coordinator Service.
5. Re-run logical DB Job if users/grants are missing.
6. Record RPO/RTO.

## 8. Teardown

Destructive. Snapshot first. Use this on a disposable prod-like stack.

### 8.1 Reverse destroy order

```bash
just gcp-pulumi remove-resource --folder load-uniprot-mapping --stack prod
just gcp-pulumi remove-resource --folder load-content-from-s3 --stack prod
just gcp-pulumi remove-resource --folder arangodb-dataloader --stack prod
just gcp-pulumi remove-resource --folder arangodb-backup --stack prod
just gcp-pulumi remove-resource --folder create-arangodb-databases --stack prod
just gcp-pulumi remove-resource --folder <arangodb-prod-project> --stack prod
# last, only if no ArangoDeployment remains
just gcp-pulumi remove-resource --folder arangodb-operator --stack prod
```

Skip stacks you never created. Never run these against `dev` / `experiments` / `local`.

### 8.2 Disks and the node pool

Operator often **keeps** PVCs after CR delete.

```bash
kubectl get pvc -n <prod-ns> | grep -i arango
kubectl delete pvc -n <prod-ns> <name>   # only if data may go
```

Remove `stateful-db` only if nothing else (future CNPG, etc.) should stay:

```bash
# edit production instancegroups.yaml, commit, replace, update
just gcp-cluster replace-manifests --cluster <prod-cluster>
just gcp-cluster update-cluster --cluster <prod-cluster>
```

Full cluster teardown: [`README.md` §6](../README.md#6-disposable-cluster-lifecycle).

## 9. Verify

```bash
kubectl get nodes -l pool=database
kubectl get arangodeployment -A
kubectl get pods -n <prod-ns> -l "arango_deployment=arangodb" -o wide
kubectl get pvc -n <prod-ns> | grep -i arango
kubectl get jobs -n <prod-ns> | grep create-databases
```

Pass bar:

- Three `pool=database` nodes, tainted, one per zone.
- Operator Ready.
- 3 agents + 3 dbservers + 3 coordinators Ready, on those nodes, spread across zones.
- Agent/dbserver PVCs Bound.
- Logical-DB Job succeeded (if run).

## 10. Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| No `stateful-db` nodes | Production bundle never got the IG | [§4.1](#41-target-manifest) — do not use the 2024 experiments cluster |
| Pod Pending, untolerated taint | CR missing toleration | Production program must set it (plan) |
| Pod Pending, `arm64` | Lab program hardcoded arm64 | Production must set `amd64` |
| Only one Arango pod | You applied lab `arangodb-single` | Wrong stack. Stop. Do not “fix” by editing `Pulumi.dev.yaml` |
| Members pile in one zone | Missing anti-affinity | Plan: pod anti-affinity on each group |
| PVC Pending | No StorageClass / CSI | `pulumi-setup` Phase A |
| Quorum loss after drain | No PDB | Plan: PDB `minAvailable` for agents/dbservers |
| Loader Pending after taint | Job has no toleration | Add toleration or schedule on `stateless-web` |

## 11. Related documents

- Architecture (sizing, HA vs cost, storage): [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`README.md`](../README.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Code work (not done): [`plans/arangodb-production.md`](plans/arangodb-production.md)
