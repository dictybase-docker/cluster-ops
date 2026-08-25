# Production ArangoDB on the Stateful Database Pool

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Scope and freeze](#1-scope-and-freeze)
  - [1.1 What this guide is](#11-what-this-guide-is)
  - [1.2 Frozen lab stacks](#12-frozen-lab-stacks)
  - [1.3 Implemented components & operational status](#13-implemented-components--operational-status)
- [2. Production shape from the architecture](#2-production-shape-from-the-architecture)
  - [2.1 Highly Available plan](#21-highly-available-plan)
  - [2.2 Stateful database pool](#22-stateful-database-pool)
  - [2.3 Cluster mode](#23-cluster-mode)
  - [2.4 Tiered storage and backups](#24-tiered-storage-and-backups)
- [3. Prerequisites](#3-prerequisites)
  - [3.1 Production cluster & namespace](#31-production-cluster--namespace)
  - [3.2 Pulumi backend](#32-pulumi-backend)
  - [3.3 Tools, secrets & access](#33-tools-secrets--access)
- [4. Prepare the stateful-db pool](#4-prepare-the-stateful-db-pool)
  - [4.1 Target manifest](#41-target-manifest)
  - [4.2 Isolation](#42-isolation)
  - [4.3 Apply and validate](#43-apply-and-validate)
- [5. Install ArangoDB](#5-install-arangodb)
  - [5.0 Storage classes](#50-storage-classes)
  - [5.1 Operator](#51-operator)
  - [5.2 Production cluster instance](#52-production-cluster-instance)
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

**Status**: Production procedure. Core infrastructure programs (`arangodb-cluster`, `storage_class`, `arangodb-operator`, `create-arangodb-databases`, `arangodb-backup`) have `prod` configurations implemented. Lab `dev` / `experiments` / `local` ArangoDB stacks remain frozen. Production kOps bundle, data loaders, and PDB policies remain operational prerequisites.

> **Document type:** Provisioning guide for a **production** ArangoDB Cluster on the kOps **stateful/database** pool. Cluster sizing comes from [`kops-gcp-architecture.md`](kops-gcp-architecture.md). Cluster bootstrap is [`README.md`](../README.md). Shared Pulumi backend / StorageClass is [`pulumi-setup.md`](pulumi-setup.md). Architecture plan is [`plans/arangodb-production.md`](plans/arangodb-production.md).

## Overview

DictyCR production requires a resilient graph database deployment across failure zones. The architecture doc’s **Highly Available plan** hosts databases on a **static** 3-zone pool. Production ArangoDB provides:

| Layer | Production choice | Why |
|-------|-------------------|-----|
| Cluster topology | Private VPC, 3 control planes, Cilium | [`kops-gcp-architecture.md`](kops-gcp-architecture.md) [§3](kops-gcp-architecture.md#-3-control-plane-sizing--storage), [§7](kops-gcp-architecture.md#-7-gcp-kops-specific-quirks-networking--security), [§8](kops-gcp-architecture.md#-8-high-availability-vs-cost-optimized-comparison) |
| Database pool | 3× `n2-standard-4` (or `n2-highmem-4`), static, 100 GB `pd-balanced` boot | [Architecture §4.2](kops-gcp-architecture.md#2-statefuldatabase-node-pool-static) |
| ArangoDB mode | **Cluster** (3 agents + 3 dbservers + 3 coordinators) | [Architecture §6 Option B](kops-gcp-architecture.md#option-b-self-hosted-in-cluster-database) — engine-level replication and coordinator query routing |
| Image | `arangodb:3.12.10.1` (amd64) | Latest 3.12 patch; 3.11 is EOL |
| Placement | `nodeSelector: pool=database` + taint `dedicated=database:NoSchedule` | Dedicated isolation from stateless web and spot batch pods |
| PVC | Tiered: `dictycr-ssd` (20Gi) for agents, `dictycr-balanced` (150Gi) for dbservers | Raft agency state on fast SSD; collection storage on balanced PD |
| In-cluster networking | `externalAccess.type: None`, ClusterIP coordinator Service | Internal traffic only; services connect via in-cluster DNS |
| Cloud SQL | **Not used** | [Architecture Option A](kops-gcp-architecture.md#option-a-fully-managed-database-strongly-recommended) is PostgreSQL/MySQL only |

- **Who it's for**: Operators provisioning or maintaining the **production** DictyCR database layer.
- **What you end up with**: ArangoDB Cluster on `stateful-db`, app databases, automated restic backups to GCS, teardown path.
- **What you must not do**: Edit `arangodb-single/Pulumi.dev.yaml`, `Pulumi.experiments.yaml`, or `Pulumi.local.yaml`. Leave lab stacks alone.

## Quick Setup

Work against the **production** cluster environment (`just cluster-env --env prod --cluster <prod-cluster>`).

1. Confirm the production cluster matches [§2](#2-production-shape-from-the-architecture).
2. Ensure the production namespace (`prod`) exists.
3. Deploy StorageClasses (`storage_class` with `Pulumi.prod.yaml`) and verify backend ([`pulumi-setup.md`](pulumi-setup.md)).
4. Isolate `stateful-db` pool ([§4](#4-prepare-the-stateful-db-pool)).
5. Deploy operator → Cluster → logical DBs ([§5](#5-install-arangodb)).
6. Configure scheduled backup ([§7](#7-backup-and-restore)) and run a restore drill.
7. Optional data import ([§6](#6-import-data)) if seeding from legacy dumps (requires authoring prod loader stacks).
8. Tear down with [§8](#8-teardown) on a disposable clone — never on live production.

## 1. Scope and freeze

### 1.1 What this guide is

End-to-end **production** lifecycle: pool isolation → storage classes → operator → ArangoDB Cluster 3.12 → databases → backup → teardown.

### 1.2 Frozen lab stacks

These lab files remain byte-frozen and untouched:

| File | Role | Status |
|------|------|--------|
| `arangodb-single/Pulumi.dev.yaml` | Lab Single, **3.11.6**, 75Gi, untainted pool | Frozen |
| `arangodb-single/Pulumi.experiments.yaml` | Lab Single, **3.11.6**, 150Gi | Frozen |
| `arangodb-single/Pulumi.local.yaml` | Local Single, **3.11.6**, 20Gi, arm64 | Frozen |
| `arangodb-single/main.go` | Hardcodes `Mode: Single`, `architecture: arm64` | Frozen |
| `create-arangodb-databases/Pulumi.experiments.yaml` / `Pulumi.local.yaml` | Lab DB bootstrap | Frozen |

### 1.3 Implemented components & operational status

| Component | Repository Path | Implemented State |
|-----------|-----------------|-------------------|
| Storage classes | `storage_class/` | `Pulumi.prod.yaml` defines both `dictycr-balanced` (`pd-balanced`) and `dictycr-ssd` (`pd-ssd`) |
| ArangoDB operator | `arangodb-operator/` | `Pulumi.prod.yaml` pins chart `kube-arangodb/kube-arangodb` 1.2.42 in `operators` namespace |
| ArangoDB Cluster | `arangodb-cluster/` | Standalone program generating `database.arangodb.com/v1` `ArangoDeployment` (mode: Cluster, 3+3+3, amd64, 3.12.10.1) |
| Logical databases | `create-arangodb-databases/` | `Pulumi.prod.yaml` creates Secret `backend` and executes Job `backend-create-databases` for 7 application databases |
| Backup | `arangodb-backup/` | `Pulumi.prod.yaml` manages GCS bucket `restic-arangodb-backup-prod`, CronJob `arangodb-backup-cronjob`, and Job `arangodb-backup-job` |
| kOps cluster bundle | `config/kops/<prod-cluster>/` | Prerequisite: operator bootstraps `<prod-cluster>` with tainted `stateful-db` InstanceGroup |
| Data loaders | `arangodb-dataloader/`, `load-*/` | Lab configs only (`Pulumi.experiments.yaml`); prod stacks authored when migration is needed |

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

Do **not** use the architecture’s Cost-Optimized plan for production ArangoDB. That plan consolidates workloads onto a single node pool without zone isolation.

### 2.2 Stateful database pool

| Knob | Production value | Source |
|------|------------------|--------|
| Machine | `n2-standard-4` (4 vCPU / 16 GB RAM) default; `n2-highmem-4` (4 vCPU / 32 GB RAM) if working sets exceed RAM | [Architecture §4.2](kops-gcp-architecture.md#2-statefuldatabase-node-pool-static) |
| Count | **3 / 3**, static | [Architecture §4.2](kops-gcp-architecture.md#2-statefuldatabase-node-pool-static) — no scale-to-zero |
| Zones | `us-central1-a`, `b`, `c` | [Architecture §4](kops-gcp-architecture.md#-4-worker-node-capacity-disk-sizing--elasticity) / [§3](kops-gcp-architecture.md#-3-control-plane-sizing--storage) |
| Boot disk | 100 GB `pd-balanced` | [Architecture §4.2](kops-gcp-architecture.md#2-statefuldatabase-node-pool-static) |
| Data disks | Separate PVC per Agent and DBServer member | [Architecture §6](kops-gcp-architecture.md#-6-database-storage--retrieval) |
| Label | `pool: database` | Target for pod `nodeSelector` |
| Taint | `dedicated=database:NoSchedule` | Prevents non-database workloads from landing on stateful nodes |

`n2-standard-4` × 3 hosts the 9 cluster members (3 agents, 3 dbservers, 3 coordinators). If RAM utilization becomes high under production load, step the pool to `n2-highmem-4` rather than expanding node count.

### 2.3 Cluster mode

[Architecture §6 Option B](kops-gcp-architecture.md#option-b-self-hosted-in-cluster-database): production HA uses an **operator-managed replica set**, dedicated PVCs, anti-affinity preferences, and backups.

| Role | Count | CPU Request | Memory Request | Storage | Role in Cluster |
|------|-------|-------------|----------------|---------|-----------------|
| **Agents** | 3 | 250m | 1Gi | 20Gi `dictycr-ssd` | Agency Raft consensus & cluster state |
| **DBServers** | 3 | 2 | 12Gi | 150Gi `dictycr-balanced` | Sharded data storage & RocksDB engine |
| **Coordinators** | 3 | 500m | 2Gi | None (stateless) | Query optimization, routing, API endpoint |

- **Pod Anti-Affinity**: `spec.go` applies `preferredDuringSchedulingIgnoredDuringExecution` (hostname weight 100, zone weight 50) for each server role. While designed to distribute members across nodes and zones, this is a soft scheduling preference. Operators should verify zone balance across nodes during deployment.
- **Disruption Management**: The custom resource does not generate custom PodDisruptionBudgets. Safe node draining and maintenance must be performed in coordination with the ArangoDB operator.

### 2.4 Tiered storage and backups

- **StorageClasses**:
  - `dictycr-ssd` (`pd-ssd` via GCP PD CSI driver): Used for Agents. Sub-millisecond fsync guarantees Raft consensus performance.
  - `dictycr-balanced` (`pd-balanced` via GCP PD CSI driver): Used for DBServers and backup scratch volumes.
- **Licensing**: ArangoDB Community 3.12 is used. Confirm current license terms before expanding dataset footprints.
- **Backups**: `arangodump` + restic → GCS bucket `restic-arangodb-backup-prod` daily at 02:00 UTC.

## 3. Prerequisites

### 3.1 Production cluster & namespace

Finish [`README.md`](../README.md) through [Step 3.7](../README.md#step-37--validate-ha-topology--hardening) for a **new production** cluster (`<prod-cluster>`).

Required before deploying ArangoDB:

- Private VPC topology + Cloud NAT + Cilium
- 3-zone control plane
- `stateful-db` InstanceGroup present in the cluster manifest
- `pdCSIDriver.enabled: true` in cluster spec
- Production namespace `prod` created:
  ```bash
  kubectl create namespace prod --dry-run=client -o yaml | kubectl apply -f -
  ```

### 3.2 Pulumi backend

Follow [`pulumi-setup.md`](pulumi-setup.md). Production stacks must use the **production** GCS backend and KMS encryption key (not devenv lab keys).

### 3.3 Tools, secrets & access

| Item | Requirement |
|------|-------------|
| `kubectl` | Configured with production cluster context |
| `pulumi` & `just` | Pulumi CLI 3.x and project `Justfile` |
| GCP KMS Key | Permissions to encrypt/decrypt production stack secrets |
| GCS Bucket | Bucket permissions for state and backup destinations |
| K8s Secret `dictycr` | Secret in namespace `prod` containing keys: `gcsCredentials`, `gcsProject`, `resticPass` |

## 4. Prepare the stateful-db pool

This step is Git/kOps only.

### 4.1 Target manifest

In `config/kops/<prod-cluster>/instancegroups.yaml`, configure `stateful-db`:

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

### 4.2 Isolation

| Setting | Value | Effect |
|---------|-------|--------|
| `nodeLabels.pool` | `database` | Target for pod `nodeSelector` |
| `taints` | `dedicated=database:NoSchedule` | Prevents non-database pods from scheduling on stateful nodes |

All `arangodb-cluster` pods declare matching nodeSelectors and tolerations.

### 4.3 Apply and validate

```bash
# 1. Edit the production kOps cluster bundle
$EDITOR config/kops/<prod-cluster>/instancegroups.yaml

# 2. Apply via Git-first workflow
git commit -am "<prod-cluster>: isolate stateful-db for production ArangoDB"
just gcp-cluster replace-manifests --cluster <prod-cluster>
just gcp-cluster plan-cluster --cluster <prod-cluster>
just gcp-cluster update-cluster --cluster <prod-cluster>
just gcp-cluster rolling-update \
  --cluster <prod-cluster> \
  --instance-group stateful-db \
  --yes yes
```

Validate nodes and taints:

```bash
kubectl get nodes -l pool=database -o wide
kubectl get nodes -l pool=database -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.taints}{"\n"}{end}'
```

Expect 3 nodes (one per zone in `us-central1-a`, `b`, `c`), each with `dedicated=database:NoSchedule`.

## 5. Install ArangoDB

Set active stack environment:

```bash
STACK=prod
```

### 5.0 Storage classes

Deploy both `dictycr-balanced` and `dictycr-ssd` using `storage_class/Pulumi.prod.yaml`:

```bash
# Initialize stack if needed, then preview and apply
pulumi -C storage_class stack select prod || pulumi -C storage_class stack init prod
just gcp-pulumi preview --folder storage_class --stack prod
just gcp-pulumi create-resource --folder storage_class --stack prod

# Verify both StorageClasses exist
kubectl get sc dictycr-balanced dictycr-ssd
```

### 5.1 Operator

Deploy the `kube-arangodb` operator (pinned to chart version `1.2.42` in `arangodb-operator/Pulumi.prod.yaml`):

```bash
pulumi -C arangodb-operator stack select prod || pulumi -C arangodb-operator stack init prod
just gcp-pulumi preview --folder arangodb-operator --stack prod
just gcp-pulumi create-resource --folder arangodb-operator --stack prod

# Verify operator deployment and CRDs
kubectl get pods -n operators -l app.kubernetes.io/name=kube-arangodb
kubectl get crd arangodeployments.database.arangodb.com
```

### 5.2 Production cluster instance

The `arangodb-cluster` program creates Secret `arangodb-pass` and emits an `ArangoDeployment` custom resource configured for production:
- 3 Agents (20Gi SSD) + 3 DBServers (150Gi Balanced) + 3 Coordinators (stateless).
- `externalAccess.type: None` (internal-only traffic).
- TLS `caSecretName: "None"` (HTTP inside cluster).
- Pod nodeSelector `pool: database` and toleration `dedicated=database:NoSchedule`.

```bash
# 1. Select/init stack and configure the root password secret
pulumi -C arangodb-cluster stack select prod || pulumi -C arangodb-cluster stack init prod
pulumi -C arangodb-cluster -s prod config set --path --secret properties.secret.password "<strong-root-password>"

# 2. Preview and apply
just gcp-pulumi preview --folder arangodb-cluster --stack prod
just gcp-pulumi create-resource --folder arangodb-cluster --stack prod

# 3. Monitor rollout
kubectl get arangodeployment -n prod arangodb
kubectl get pods -n prod -l "arango_deployment=arangodb" -o wide
kubectl get pvc -n prod -l "arango_deployment=arangodb"
```

Verify the coordinator Service generated by the operator:

```bash
kubectl get svc -n prod -l "arango_deployment=arangodb"
```

Workloads access ArangoDB inside the cluster at `http://arangodb.prod.svc.cluster.local:8529`.

### 5.3 Logical databases

The `create-arangodb-databases` stack creates Secret `backend` and runs Job `backend-create-databases` to create 7 application databases (`annotation`, `order`, `stock`, `content`, `cgm_ddb`, `chado`, `annofeature`) and grant user `backend` read-write permissions.

```bash
# 1. Select/init stack and configure application user password
pulumi -C create-arangodb-databases stack select prod || pulumi -C create-arangodb-databases stack init prod
pulumi -C create-arangodb-databases -s prod config set --path --secret properties.arangodbSecret.pass "<app-password>"

# 2. Preview and apply
just gcp-pulumi preview --folder create-arangodb-databases --stack prod
just gcp-pulumi create-resource --folder create-arangodb-databases --stack prod

# 3. Verify Job completion and created secret
kubectl get jobs -n prod -l app=arangodb-create-databases
kubectl get secret -n prod backend
```

## 6. Import data

Data loaders are optional one-shot migration tasks.

> **Note:** Production stack configurations (`Pulumi.prod.yaml`) for data loaders are not checked into the repository by default. When performing data migration, author prod stack configs pointing to production object storage and secret endpoints.

### 6.1 Graph dumps

`arangodb-dataloader` imports `chado` and `cgm_ddb` collections from object storage.

When creating `arangodb-dataloader/Pulumi.prod.yaml`:
- Set namespace to `prod`.
- Set ArangoDB host to `arangodb.prod.svc.cluster.local`.
- Reference production MinIO/S3 credentials.
- Ensure the Job schedules on `stateless-web` nodes (or add the database toleration if running on `stateful-db`).

```bash
just gcp-pulumi preview --folder arangodb-dataloader --stack prod
just gcp-pulumi create-resource --folder arangodb-dataloader --stack prod
```

### 6.2 Content and UniProt

`load-content-from-s3` and `load-uniprot-mapping` follow the same procedure: author `Pulumi.prod.yaml` pointing to production services and apply.

## 7. Backup and restore

### 7.1 Scheduled backup

`arangodb-backup` creates GCS bucket `restic-arangodb-backup-prod`, a Kubernetes `CronJob` (`arangodb-backup-cronjob`) running daily at 02:00 UTC, and an immediate verification Job (`arangodb-backup-job`). It executes `arangodump`, bundles the archive, and writes encrypted restic snapshots directly to GCS.

`arangodb-backup/Pulumi.prod.yaml` configuration:
- Destination bucket: `restic-arangodb-backup-prod`.
- Ephemeral PVC: `150Gi` on StorageClass `dictycr-balanced`.
- Credentials Secret: `dictycr` in namespace `prod` (providing `gcsCredentials`, `gcsProject`, `resticPass`).

```bash
# Preview and apply
pulumi -C arangodb-backup stack select prod || pulumi -C arangodb-backup stack init prod
just gcp-pulumi preview --folder arangodb-backup --stack prod
just gcp-pulumi create-resource --folder arangodb-backup --stack prod

# Verify CronJob and initial Job
kubectl get cronjobs,jobs -n prod -l app=arangodb-backup
kubectl logs -n prod -l app=arangodb-backup --tail=50
```

### 7.2 Restore drill

Conduct restore drills on a disposable clone cluster or temporary namespace:

1. Deploy `storage_class`, `arangodb-operator`, and an empty `arangodb-cluster` in the target namespace.
2. Deploy a temporary restore container mounting Secret `dictycr` and ephemeral storage.
3. Execute `restic restore latest` from `gs://restic-arangodb-backup-prod/arangodump`.
4. Run `arangorestore` against `http://<coordinator-service>.<namespace>.svc.cluster.local:8529` using root credentials from Secret `arangodb-pass`.
5. Run `create-arangodb-databases` if application users or permissions need re-initialization.
6. Verify collection document counts and record recovery metrics (RTO and RPO).

## 8. Teardown

To destroy the database layer on a disposable test cluster:

### 8.1 Reverse destroy order

```bash
# 1. Destroy loaders (if created)
just gcp-pulumi remove-resource --folder load-uniprot-mapping --stack prod
just gcp-pulumi remove-resource --folder load-content-from-s3 --stack prod
just gcp-pulumi remove-resource --folder arangodb-dataloader --stack prod

# 2. Destroy backup CronJob and initial job
just gcp-pulumi remove-resource --folder arangodb-backup --stack prod

# 3. Destroy database bootstrap job
just gcp-pulumi remove-resource --folder create-arangodb-databases --stack prod

# 4. Destroy ArangoDB Cluster deployment and secret
just gcp-pulumi remove-resource --folder arangodb-cluster --stack prod

# 5. Destroy operator (only if no other Arango deployments remain)
just gcp-pulumi remove-resource --folder arangodb-operator --stack prod
```

### 8.2 Disks and the node pool

The ArangoDB operator preserves PersistentVolumeClaims after CR deletion to prevent accidental data loss.

```bash
# Inspect and delete PVCs if data is no longer needed
kubectl get pvc -n prod -l arango_deployment=arangodb
kubectl delete pvc -n prod -l arango_deployment=arangodb
```

If decommissioning the dedicated node pool, edit `config/kops/<prod-cluster>/instancegroups.yaml` and update the cluster.

## 9. Verify

Audit the production ArangoDB deployment after installation:

```bash
# 1. Check dedicated node pool and taints
kubectl get nodes -l pool=database -o wide
kubectl get nodes -l pool=database -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.taints}{"\n"}{end}'

# 2. Check operator pod
kubectl get pods -n operators -l app.kubernetes.io/name=kube-arangodb

# 3. Check ArangoDeployment custom resource
kubectl get arangodeployment -n prod arangodb -o yaml

# 4. Check all 9 cluster member pods
kubectl get pods -n prod -l "arango_deployment=arangodb" -o wide

# 5. Check bound PVCs (3 ssd for agents, 3 balanced for dbservers)
kubectl get pvc -n prod -l "arango_deployment=arangodb"

# 6. Check internal coordinator Service
kubectl get svc -n prod -l "arango_deployment=arangodb"

# 7. Check database bootstrap Job status
kubectl get jobs -n prod -l app=arangodb-create-databases

# 8. Check backup CronJob and immediate Job
kubectl get cronjobs,jobs -n prod -l app=arangodb-backup
```

**Verification Checklist**:
- [ ] 3 `stateful-db` nodes Ready across 3 zones, each with taint `dedicated=database:NoSchedule`.
- [ ] Operator pod in `operators` namespace is Running and Ready.
- [ ] StorageClasses `dictycr-balanced` and `dictycr-ssd` present and backed by `pd.csi.storage.gke.io`.
- [ ] 9 ArangoDB pods Running in namespace `prod` (3 agents, 3 dbservers, 3 coordinators).
- [ ] All member pods scheduled exclusively on `pool=database` nodes.
- [ ] 3 Agent PVCs bound on `dictycr-ssd` (20Gi); 3 DBServer PVCs bound on `dictycr-balanced` (150Gi).
- [ ] Coordinator Service reachable on port 8529.
- [ ] Logical DB Job `backend-create-databases` completed successfully.
- [ ] Secret `backend` contains application user credentials.
- [ ] `arangodb-backup` CronJob created and initial verification Job completed.

## 10. Troubleshooting

| Symptom | Cause | Solution |
|---------|-------|----------|
| Member pods stuck in `Pending` with taint error | Nodes lack taint toleration or wrong taint applied | Verify nodes have `dedicated=database:NoSchedule` and `arangodb-cluster/Pulumi.prod.yaml` tolerations match |
| Member pods stuck in `Pending` with architecture mismatch | Image architecture incompatibility | Ensure `architecture: amd64` is configured in `Pulumi.prod.yaml` for `n2` nodes |
| Agent/DBServer PVCs stuck in `Pending` | StorageClasses missing or CSI driver disabled | Run `just gcp-pulumi create-resource --folder storage_class --stack prod` and confirm `pdCSIDriver.enabled: true` |
| Application cannot connect to ArangoDB | Connecting via wrong endpoint or missing credentials | Use internal Service URL `http://arangodb.prod.svc.cluster.local:8529` and credentials from Secret `backend` |
| `backend-create-databases` Job fails with 401 Unauthorized | Root password mismatch | Verify Secret `arangodb-pass` password matches `properties.secret.password` in `arangodb-cluster` |
| Backup Job fails with permission error | GCS credentials or bucket missing | Ensure `restic-arangodb-backup-prod` exists and Secret `dictycr` contains valid GCP service account keys |
| Pods unevenly distributed across zones | Soft anti-affinity constraint under node capacity pressure | Check node distribution across `us-central1-a`, `b`, `c`; pod anti-affinity is soft (preferred) |

## 11. Related documents

- Architecture (sizing, HA vs cost, storage): [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`README.md`](../README.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Implementation plan & rationale: [`plans/arangodb-production.md`](plans/arangodb-production.md)
