# Plan: Production ArangoDB Cluster

## Table of Contents
- [Goal](#goal)
- [Non-goals](#non-goals)
- [Current state](#current-state)
- [Target state](#target-state)
- [Work items](#work-items)
  - [1. Production kOps bundle](#1-production-kops-bundle)
  - [2. New Pulumi stacks only](#2-new-pulumi-stacks-only)
  - [3. Cluster-capable Pulumi program](#3-cluster-capable-pulumi-program)
  - [4. Placement, anti-affinity, PDB](#4-placement-anti-affinity-pdb)
  - [5. Operator prod stack](#5-operator-prod-stack)
  - [6. Logical DBs, loaders, backup](#6-logical-dbs-loaders-backup)
  - [7. Verification](#7-verification)
- [Suggested file layout](#suggested-file-layout)
- [Order of implementation](#order-of-implementation)
- [Acceptance](#acceptance)
- [Risks](#risks)

**Status**: Implemented (Historical Reference & Architecture Rationale). Core Pulumi program implemented in `arangodb-cluster/` with supporting `prod` stacks for `backup_secrets`, `storage_class`, `arangodb-operator`, `create-arangodb-databases`, `arangodb-backup`, and `arangodb-restore`. Lab stacks remain frozen. Active deployment runbook: [`../arangodb-deploy.md`](../arangodb-deploy.md). Architecture sizing: [`../kops-gcp-architecture.md`](../kops-gcp-architecture.md).

## Goal

Enable a **production** ArangoDB **Cluster** (3.12.10.1, amd64) on the architecture’s **stateful/database** pool, without modifying today’s lab stacks.

## Non-goals

- Do **not** edit `arangodb-single/Pulumi.dev.yaml`, `Pulumi.experiments.yaml`, `Pulumi.local.yaml`.
- Do **not** change `arangodb-single/main.go` in place if that would alter lab `pulumi up` behavior. Prefer a **new** project or a mode that defaults to today’s Single + arm64 when prod fields are absent.
- Do **not** add `stateful-db` to `config/kops/dictycr-dev-staging-dcr-experiments/instancegroups.yaml` (not the production cluster).
- Do **not** taint `config/kops/_starter/` until product agrees every new cluster should inherit the taint.
- Do **not** bump lab operator charts.
- Do **not** implement ActiveFailover (removed in 3.12).

## Current state

| Piece | Today |
|-------|--------|
| `arangodb-single/main.go` | `Mode: "Single"`, `Architecture: ["arm64"]`, no nodeSelector/tolerations |
| Lab stacks | `version: 3.11.6`, sizes 75Gi / 150Gi / 20Gi |
| CRD types in `crds/kubernetes/database/v1` | No `Agents` / `DBServers` / `Coordinators` args |
| Starter `stateful-db` | `n2-standard-4` × 3, label `pool: database`, no taint |
| Experiments kOps bundle | Control plane + `nodes-us-central1-c` only |
| Operator | Chart 1.2.42 in lab stacks; supports 3.12; upstream latest is 1.4.x |
| Loaders / backup | Experiments/dev-oriented secrets; no prod stack files |

## Target state

| Piece | Production |
|-------|------------|
| kOps | New `<prod-cluster>` bundle matching architecture HA plan |
| Pool | 3× `n2-standard-4` (or `n2-highmem-4`), taint `dedicated=database:NoSchedule`, label `pool: database` |
| Operator | New `arangodb-operator/Pulumi.prod.yaml` (chart pin decided below) |
| Database | Cluster 3+3+3, image `arangodb:3.12.10.1`, `architecture: [amd64]` |
| Placement | nodeSelector + toleration on every member group |
| Spread | Pod anti-affinity across zones; PDB on agents/dbservers |
| Disks | `dictycr-balanced`; agents 8Gi; dbservers 150Gi (tune later) |
| App DBs / import / backup | New `Pulumi.prod.yaml` files only |

Image tag `3.12.10.1` exists on Docker Hub (`library/arangodb`) and as git tag `v3.12.10.1`. Confirm chart pin against kube-arangodb docs at implementation time. 1.2.42 is a known-good floor for 3.12; 1.4.x is current upstream.

## Work items

### 1. Production kOps bundle

1. Bootstrap a **new** cluster name with `just gcp-cluster bootstrap-bundle` ([README](../../README.md) Step 3.1).
2. Edit `config/kops/<prod-cluster>/instancegroups.yaml`:
   - Keep architecture HA sizes (3 CP, stateless 3–6, stateful 3 static).
   - Add taint `dedicated=database:NoSchedule` on `stateful-db` only.
3. Commit. Do not change `_starter` or the 2024 experiments bundle.
4. `replace-manifests` → `plan-cluster` → `update-cluster` → rolling-update `stateful-db`.

### 2. New Pulumi stacks only

Create **new** files. Copy structure from lab YAML, not encrypted values.

Proposed:

- `arangodb-operator/Pulumi.prod.yaml`
- `arangodb-cluster/Pulumi.prod.yaml` (new project — preferred) **or** `arangodb-single/Pulumi.prod.yaml` only if the program can stay Single-default
- `create-arangodb-databases/Pulumi.prod.yaml`
- `arangodb-dataloader/Pulumi.prod.yaml`
- `arangodb-backup/Pulumi.prod.yaml`
- Optional: `load-content-from-s3/Pulumi.prod.yaml`, `load-uniprot-mapping/Pulumi.prod.yaml`

Each uses the **production** KMS key and GCS backend, production namespace, production secrets.

### 3. Cluster-capable Pulumi program

Preferred: **new directory** `arangodb-cluster/` so `arangodb-single` lab stays byte-identical.

Steps:

1. Regenerate `crds/kubernetes/database/v1` from the operator CRD that production will install, **or** apply the Cluster `ArangoDeployment` as a typed YAML asset if regeneration is too wide. Regeneration must not break `arangodb-single` compiles.
2. Program reads `properties`:
   - `mode` (`Cluster` for prod)
   - `version` (`3.12.10.1`)
   - `architecture` (`[amd64]`)
   - `nodeSelector`, `tolerations`
   - per-group `count`, PVC size, resources
3. Emit `spec.agents`, `spec.dbservers`, `spec.coordinators` (not `spec.single`).
4. `Environment: Production` (lab stays `Development`).
5. External access: do **not** copy lab `NodePort` blindly. Prefer ClusterIP + in-cluster DNS; document Ingress separately.
6. Tests: config decode + spec shape (mode, counts, selector, architecture). No live cluster required for unit tests.

If a single program is forced: default missing fields to today’s Single + arm64 so existing stacks preview with zero diff.

### 4. Placement, anti-affinity, PDB

On every server group:

```yaml
nodeSelector:
  pool: database
tolerations:
  - key: dedicated
    operator: Equal
    value: database
    effect: NoSchedule
```

Anti-affinity: `kubernetes.io/hostname` required, `topology.kubernetes.io/zone` preferred.

PDB: agents `minAvailable: 2`, dbservers `minAvailable: 2`. Coordinators can be `minAvailable: 1`.

### 5. Operator prod stack

New `Pulumi.prod.yaml` only.

Decision at implement time (record in the stack comment):

| Option | When |
|--------|------|
| Stay on 1.2.42 | Minimal surprise; known 3.12 support |
| Pin 1.4.x | Current upstream; test CRD drift against regenerated types |

Do not change `Pulumi.dev.yaml` / `experiments` / `local`.

### 6. Logical DBs, loaders, backup

1. `create-arangodb-databases`: same DB list; coordinator Service DNS; new secrets; optional toleration if the Job must run on `stateful-db`.
2. `arangodb-dataloader` / content / UniProt: prod stacks; prefer `stateless-web` unless they need the dump PVC next to the DB.
3. `arangodb-backup`: prod GCS bucket; size ephemeral PVC for 3× dbserver dumps; keep CronJob + first Job.
4. Restore is an in-cluster Job (`arangodb-restore`, not a follow-up anymore): init container `restic restore`, main container `arangorestore`, shared scratch volume, no laptop/Docker/port-forward involved ([deploy guide §5.2](../arangodb-deploy.md#52-restore-drill)).

### 7. Verification

- `go test` / `golangci-lint` on new packages.
- `pulumi preview` on **prod** stack only — must not require opening lab stacks.
- Preview of **unchanged** `arangodb-single` `dev` stack must show **no** resource replacements (prove freeze).
- kubectl checks in [deploy guide §7](../arangodb-deploy.md#7-verify).

## Suggested file layout

```
arangodb-cluster/          # new Pulumi project
  main.go
  Pulumi.yaml
  Pulumi.prod.yaml
docs/arangodb-deploy.md    # already written
docs/plans/arangodb-production.md  # this file
config/kops/<prod-cluster>/instancegroups.yaml  # new bundle, not experiments
```

Leave `arangodb-single/` as-is.

## Order of implementation

1. Production kOps bundle + tainted `stateful-db` (no Go).
2. StorageClass + operator **prod** stack.
3. CRD types or YAML asset.
4. `arangodb-cluster` program + unit tests.
5. `Pulumi.prod.yaml` for Cluster, DBs, backup, loaders.
6. Preview freeze check on lab `arangodb-single`.
7. Apply on a prod-like cluster; restore drill.

## Acceptance

- Lab `dev` / `experiments` / `local` ArangoDB YAML and `arangodb-single/main.go` unchanged from pre-plan HEAD.
- Production Cluster 3+3+3 on `pool=database` nodes, amd64, 3.12.10.1.
- Members spread across three zones; PDBs present.
- Logical DBs Job succeeds against coordinators.
- Backup CronJob exists; restore drill notes recorded.
- Deploy guide commands match the new project/stack names.

## Risks

| Risk | Mitigation |
|------|------------|
| CRD regen breaks `arangodb-single` | New project + isolated types, or YAML asset |
| 3× `n2-standard-4` too small for 9 members | Start there (architecture default); step to `n2-highmem-4` |
| Taint starves CNPG/Redis/MinIO | Those programs get their own prod plan; do not drop the taint |
| Community license disk cap | Confirm current ArangoDB license before large imports |
| Applying Cluster next to lab Single | Different cluster, different stack name `prod`, different namespace if needed |
