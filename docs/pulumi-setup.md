# Pulumi Database Setup Guide

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Prerequisites](#1-prerequisites)
  - [1.1 Cluster Handoff](#11-cluster-handoff)
  - [1.2 Tools](#12-tools)
  - [1.3 Access](#13-access)
- [2. Dev vs HA Topology](#2-dev-vs-ha-topology)
- [3. Environment Variables for Pulumi](#3-environment-variables-for-pulumi)
  - [3.1 Add to Your Cluster Env File](#31-add-to-your-cluster-env-file)
  - [3.2 Activate the Cluster Shell](#32-activate-the-cluster-shell)
- [4. Pulumi Backend Bootstrap](#4-pulumi-backend-bootstrap)
  - [4.1 Create the pulumi-manager Key](#41-create-the-pulumi-manager-key)
  - [4.2 KMS Keyring and Crypto Key](#42-kms-keyring-and-crypto-key)
  - [4.3 GCS State Bucket and Login](#43-gcs-state-bucket-and-login)
- [5. Switching Between Clusters](#5-switching-between-clusters)
- [6. Stacks and Configuration](#6-stacks-and-configuration)
  - [6.1 Stack Names](#61-stack-names)
  - [6.2 Config Sources](#62-config-sources)
  - [6.3 Creating a Stack](#63-creating-a-stack)
- [7. Deploy the Database Layer](#7-deploy-the-database-layer)
  - [7.1 Order of Operations](#71-order-of-operations)
  - [7.2 Phase A — StorageClass](#72-phase-a--storageclass)
  - [7.3 Phase B — Operators](#73-phase-b--operators)
  - [7.4 Phase C — ArangoDB](#74-phase-c--arangodb)
  - [7.5 Phase D — PostgreSQL (CloudNativePG)](#75-phase-d--postgresql-cloudnativepg)
  - [7.6 Phase E — Redis](#76-phase-e--redis)
  - [7.7 Phase F — MinIO](#77-phase-f--minio)
- [8. Verify Database Readiness](#8-verify-database-readiness)
- [9. Database Setup Complete](#9-database-setup-complete)

**Last tested**: 2026-07-16

> **Document type:** This is a **provisioning guide** — a sequential walkthrough for deploying the **database layer and its dependencies** on a kops cluster with Pulumi. It starts where [`docs/kops-setup-draft.md`](kops-setup-draft.md) ends (cluster up and validated). It does **not** cover application services, Velero whole-cluster backup, or loaders.

## Overview

After the cluster is healthy, install only what data plane needs:

| Component | Pulumi project | Role |
|-----------|----------------|------|
| StorageClass | `storage_class` | GCE CSI class used by DB PVCs (`dictycr-balanced`) |
| ArangoDB operator | `arangodb-operator` | Helm operator for ArangoDB CRDs |
| ArangoDB instance | `arangodb-single` | Single-mode ArangoDB deployment + root secret |
| Logical DBs | `create-arangodb-databases` | Job: ensure user/DBs/grants |
| CNPG operator | `cloudnative-pg-operator` | CloudNativePG operator (Helm) |
| PostgreSQL | `cloudnative-pg-cluster` | CNPG `Cluster` + user secret + backup bucket |
| Redis | `redis-standalone` | Single-replica Redis Deployment + PVC |
| MinIO | `minio` | Object store (Helm) used by data/backup-related flows |

- **Who it's for**: Operators who finished [kops cluster creation](kops-setup-draft.md) through Phase 6/7 validation.
- **What you'll end up with**: Storage class, ArangoDB (+ app databases), PostgreSQL via CNPG, Redis, MinIO — ready for application stacks.
- **How long**: Roughly 20–45 minutes after Pulumi backend exists (download images + PVC bind dominate).
- **One project = one cluster**: Same rule as the kops guide. SA keys and paths use `credentials/<project-id>/`.
- **Out of scope here**: `event-messenger`, GraphQL/frontends, `install-velero`, `arangodb-dataloader`, and other non-database apps.

## Quick Setup

Work inside an activated cluster env shell (`just cluster-env --env <env> --cluster <cluster-name>`). Assume kubeconfig already works (`kubectl get nodes`).

1. [Prerequisites](#1-prerequisites) — cluster validated; `pulumi`, `kubectl`, credentials ready.
2. [Add Pulumi env vars](#3-environment-variables-for-pulumi) to the per-cluster env file; re-enter `cluster-env`.
3. [Bootstrap Pulumi backend](#4-pulumi-backend-bootstrap) — `pulumi-manager` key, KMS, GCS state, set `PULUMI_BACKEND_URL` in the env file.
4. [Set up switching](#5-switching-between-clusters) so `just cluster-env` targets the right state backend every time.
5. [Create stacks](#6-stacks-and-configuration) for your stack name (often `dev`, copy from a base stack when available).
6. [Deploy in order](#7-deploy-the-database-layer) — StorageClass → operators → ArangoDB + DBs → PostgreSQL → Redis → MinIO.
7. [Verify](#8-verify-database-readiness) with the kubectl checks.
8. **Stop** at [Section 9](#9-database-setup-complete). Do not continue into application charts in this guide.

## 1. Prerequisites

### 1.1 Cluster Handoff

Finish [`docs/kops-setup-draft.md`](kops-setup-draft.md) first:

- Cluster env file active (`just cluster-env --env <env> --cluster <cluster-name>`)
- `kubectl` talks to the cluster (`kubectl get nodes` healthy)
- Phase 4c security edits applied if you care about production access controls
- Storage-friendly topology in place for your target:
  - **Simple dev**: single control plane / small worker pool is enough
  - **HA-oriented**: private topology + multi-zone workers + **stateful** InstanceGroup for database pods (see [kops §1.3.4](kops-setup-draft.md#134-optional-tuning-knobs) and [Phase 4d](kops-setup-draft.md#phase-4d))

CSI for GCE PD should already be enabled on the cluster (`pdCSIDriver` in the kops manifest). StorageClass creation assumes that.

### 1.2 Tools

Same tooling baseline as the kops guide: `just`, `asdf` tools (`kubectl`, `pulumi`, `gcloud`), `jq`, `direnv` optional. Re-run if needed:

```bash
just install-tool --name <tool> --version <version>
```

### 1.3 Access

| Need | Why |
|------|-----|
| Working kubeconfig | Pulumi Kubernetes provider applies CRDs/Deployments |
| IAM to create SA keys / KMS / GCS | Pulumi manager identity and state |
| Project-scoped paths under `credentials/<project-id>/` | Matches per-project isolation |

Do **not** put Pulumi credentials in `.envrc`. Put them in the per-cluster env file only.

## 2. Dev vs HA Topology

This repo’s **programs** are mostly single-instance. HA is partial: some knobs exist; some HA modes are **not implemented**. Treat the matrix as source of truth when choosing stack values.

| Layer | Pulumi project | Simple dev (supported) | HA (supported?) | Notes |
|-------|----------------|------------------------|-----------------|-------|
| StorageClass | `storage_class` | `pd-balanced` / `dictycr-balanced` | Same class; durability is disk + multi-AZ scheduling | Not a HA toggle — cluster/zone layout decides risk |
| ArangoDB operator | `arangodb-operator` | Deploy operator | Same | `deploymentReplication` flag exists on operator chart; current stack files set `false` |
| ArangoDB data | `arangodb-single` | **Single** mode (hardcoded in code) | **Not implemented** | `Mode: "Single"` in `arangodb-single/main.go`. No Cluster/ActiveFailover project in this repo |
| Arango logical DBs | `create-arangodb-databases` | Job creates named DBs + grants | Same | Needs running Arango + root secret. Stacks: `experiments` / `local` only (no `Pulumi.dev.yaml`) |
| CNPG operator | `cloudnative-pg-operator` | Operator in `operators` (or configured ns) | Same | Prerequisite for any CNPG cluster |
| PostgreSQL | `cloudnative-pg-cluster` | `instances: 1` in shipped stacks | **Config knob only** — set `instances: 3` for multi-instance CNPG | Program passes `Instances` into CR. Stock `Pulumi.*.yaml` still use `1`. Prefer stateful nodes + multi-zone for true HA |
| Redis | `redis-standalone` | 1 replica Deployment | **Not implemented** | Replicas hardcoded to `1` |
| MinIO | `minio` | Single Helm release + PVC | **Not implemented** as distributed MinIO | Bitnami chart values in code are standalone-style; no multi-node HA mode in project |

**Practical guidance**

- **Simple dev**: Use existing `dev` / small disk sizes; keep `instances: 1` on CNPG; Arango Single is expected.
- **HA-oriented production**:
  - Place DB pods on the **stateful** node pool (taints/labels as designed in kops InstanceGroups).
  - Raise CNPG `instances` (e.g. `3`) and size disks up in stack config before `pulumi up`.
  - Accept gaps: Arango stays Single until a separate Cluster-mode project exists; Redis and MinIO stay single-node — plan external managed services or new projects if you need HA for those.

## 3. Environment Variables for Pulumi

### 3.1 Add to Your Cluster Env File

Edit `.env.<env>.<cluster-name>` (same file as kops). Example values — replace placeholders:

```bash
export PULUMI_GCP_CREDENTIALS="${PWD}/credentials/<project-id>/pulumi-manager.json"
export PULUMI_SECRET_PROVIDER="gcpkms://projects/<project-id>/locations/us-central1/keyRings/<keyring>/cryptoKeys/<key>"
export PULUMI_BACKEND_URL="gs://<pulumi-state-bucket>"
# Kubernetes provider uses the cluster kubeconfig from this env file:
# export KUBECONFIG=...
```

Also keep kops-required vars (`PROJECT_ID`, `KUBECONFIG`, etc.) already defined per [kops §1.3.2](kops-setup-draft.md#132-required-variables).

> **Why `PULUMI_BACKEND_URL` matters.** `pulumi login` writes to a global file (`~/.pulumi/credentials.yaml`), not to the shell. If you switch to a different cluster whose state lives in a different GCS bucket, Pulumi commands silently target the wrong backend. Setting `PULUMI_BACKEND_URL` in the per-cluster env file makes the backend follow the shell — each `just cluster-env` activation switches the cluster, kubeconfig, credentials, **and** state backend together.

Recipes under `just gcp-pulumi` export `GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"` for the duration of Pulumi commands. Your active cluster credential can stay as `kops-cluster-creator` for plain `kubectl`.

### 3.2 Activate the Cluster Shell

```bash
just cluster-env --env <env> --cluster <cluster-name>
```

Confirm:

```bash
echo "$PULUMI_GCP_CREDENTIALS"
echo "$PULUMI_SECRET_PROVIDER"
echo "$PULUMI_BACKEND_URL"
kubectl get nodes
```

> The `pulumi stack ls` check isn't available until after backend bootstrap ([§4](#4-pulumi-backend-bootstrap)). Skip it for now — you'll verify the backend when switching clusters in [§5](#5-switching-between-clusters).

## 4. Pulumi Backend Bootstrap

Do this **once per GCP project** (thereafter re-login if needed).

### 4.1 Create the pulumi-manager Key

```bash
mkdir -p credentials/${PROJECT_ID}
just gcp-sa create-sa --sa-name pulumi-manager \
  --roles-file gcs-files/roles-permissions/pulumi-manager-roles.txt \
  --output-file credentials/${PROJECT_ID}/pulumi-manager.json
```

Roles listed in that file include storage admin and KMS crypto operator (needed for state + secret decryption).

Set / refresh `PULUMI_GCP_CREDENTIALS` in the env file to that path, then re-run `just cluster-env`.

> Root `initialize-pulumi` still defaults to a flat `credentials/pulumi-manager.json`. Prefer the project subdirectory command above so paths stay consistent with the kops guide.

### 4.2 KMS Keyring and Crypto Key

```bash
just gcp-kms create-keyring-and-key \
  --keyring-name <keyring-name> \
  --key-name <key-name> \
  --credentials-file credentials/${PROJECT_ID}/pulumi-manager.json \
  --location us-central1
```

Then set:

```bash
export PULUMI_SECRET_PROVIDER="gcpkms://projects/${PROJECT_ID}/locations/us-central1/keyRings/<keyring-name>/cryptoKeys/<key-name>"
```

Persist the same line in the cluster env file.

### 4.3 GCS State Bucket and Login

Pick a **globally unique** bucket (example: `pulumi-state-<project-id>`):

```bash
just gcp-pulumi pulumi-gcs-setup \
  --sa-json-path credentials/${PROJECT_ID}/pulumi-manager.json \
  --gcs-bucket <pulumi-state-bucket> \
  --location us-central1
```

This creates the bucket if missing (versioning on) and runs `pulumi login gs://<pulumi-state-bucket>`.

> After bootstrap, edit the cluster env file and replace the `PULUMI_BACKEND_URL` placeholder with the real bucket name (`gs://<pulumi-state-bucket>`). Then **re-enter the shell** so the new value takes effect:
>
> ```bash
> exit                                    # leave current sub-shell
> just cluster-env --env <env> --cluster <cluster-name>   # re-enter — now PULUMI_BACKEND_URL is real
> echo "$PULUMI_BACKEND_URL"             # should show gs://<pulumi-state-bucket>
> ```
>
> The env var overrides the global `pulumi login` setting, so each `just cluster-env` activation targets the correct state backend automatically. You can still run `pulumi login` for interactive use outside the sub-shell, but the env var is the safe default.

## 5. Switching Between Clusters

Each cluster env file carries its own `KUBECONFIG`, GCP credentials, KMS key, **and** `PULUMI_BACKEND_URL`. Switching is a single command:

```bash
# Working on cluster A
exit                                    # leave sub-shell
just cluster-env --env dev --cluster cluster-b         # enter cluster B
# KUBECONFIG, PULUMI_BACKEND_URL, KMS all changed together
pulumi -C storage_class stack ls       # shows stacks from cluster B's bucket
```

**Verify you're targeting the right backend** before any `pulumi up`:

```bash
echo "$PULUMI_BACKEND_URL"
pulumi -C storage_class stack ls       # confirm stacks from this backend are visible
kubectl get nodes                      # confirm you're talking to the right cluster
```

> **Common mistake.** `pulumi login` is global state. If you run `just cluster-env` without `PULUMI_BACKEND_URL` set, Pulumi stays pointed at whatever backend was last logged into — often the wrong project. The env var (set in [§3.1](#31-add-to-your-cluster-env-file)) prevents this.

## 6. Stacks and Configuration

### 6.1 Stack Names

Examples in-repo: `dev`, `experiments`, `local`. Choose one stack name and use it for **every** database project you deploy so config and secrets stay aligned.

| Profile | Typical stack | Disk sizes | CNPG `instances` |
|---------|---------------|------------|------------------|
| Simple dev | `dev` | Smaller PVCs (see shipped `Pulumi.dev.yaml`) | `1` |
| Larger / shared lab | `experiments` | Larger PVCs | `1` unless you change config |
| HA-oriented | new stack copied from closest base | Larger PVCs; multi-zone nodes | Set `instances: 3` (or more) **before** first successful create if possible |

### 6.2 Config Sources

- Each project directory holds `Pulumi.yaml` plus `Pulumi.<stack>.yaml` config.
- Secrets use the KMS secrets provider — you need `PULUMI_SECRET_PROVIDER` and the manager key.
- **Copy-from** a known stack when one exists for that project:

```bash
just gcp-pulumi new-stack-from --folder <project-folder> --stack <stack> --from-stack <from-stack>
```

When no base stack exists (e.g. some projects lack `Pulumi.dev.yaml`):

```bash
just gcp-pulumi new-stack --folder <project-folder> --stack <stack>
```

Then edit config with `pulumi -C <folder> -s <stack> config` / `config set --path` as needed (passwords, bucket names, `instances`).

### 6.3 Creating a Stack

Always from repo root, with env shell active. The pattern is the same for every project:

**When a base stack exists** (e.g. `experiments` → `dev`):

```bash
just gcp-pulumi new-stack-from --folder <project-folder> --stack ${STACK} --from-stack ${FROM_STACK}
```

**When no base stack exists** (project has no `Pulumi.<from-stack>.yaml`):

```bash
just gcp-pulumi new-stack --folder <project-folder> --stack ${STACK}
# Then set config manually:
pulumi -C <project-folder> -s ${STACK} config set --path "<key>" "<value>"
```

**Always preview before apply**:

```bash
just gcp-pulumi preview --folder <project-folder> --stack ${STACK}
just gcp-pulumi create-resource --folder <project-folder> --stack ${STACK}
```

> **Do not** use `just pulumi-init-and-deploy` for this guide. That entrypoint also deploys non-database projects from `pulumi-files/resources/*.txt` and continues after mid-list failures. Run projects **one at a time** in the order below.

## 7. Deploy the Database Layer

Set once in the shell:

```bash
export STACK="<stack-name>"          # e.g. dev
export FROM_STACK="<base-stack>"     # e.g. experiments — or omit copy-from path when empty
```

<a id="71-order-of-operations"></a>

### 7.1 Order of Operations

```
storage_class
    → arangodb-operator
    → cloudnative-pg-operator
    → arangodb-single → create-arangodb-databases
    → cloudnative-pg-cluster
    → redis-standalone
    → minio
```

Both operators run **after** StorageClass and **before** any database instance. The two operator installs may run back-to-back (or in parallel if you split terminals). Instances must wait for their operator + StorageClass.

<a id="72-phase-a--storageclass"></a>

### 7.2 Phase A — StorageClass

Creates the GCE PD StorageClass used by Arango/CNPG/Redis/MinIO PVCs (shipped name: `dictycr-balanced`, type `pd-balanced`).

```bash
just gcp-pulumi new-stack-from --folder storage_class --stack ${STACK} --from-stack ${FROM_STACK}
just gcp-pulumi preview --folder storage_class --stack ${STACK}
just gcp-pulumi create-resource --folder storage_class --stack ${STACK}
```

Check:

```bash
kubectl get storageclass dictycr-balanced
```

<a id="73-phase-b--operators"></a>

### 7.3 Phase B — Operators

**ArangoDB operator**

```bash
just gcp-pulumi new-stack-from --folder arangodb-operator --stack ${STACK} --from-stack ${FROM_STACK}
just gcp-pulumi preview --folder arangodb-operator --stack ${STACK}
just gcp-pulumi create-resource --folder arangodb-operator --stack ${STACK}
```

Check:

```bash
kubectl get pods -n operators
# or the namespace set in that stack's config
```

**CloudNativePG operator**

```bash
just gcp-pulumi new-stack-from --folder cloudnative-pg-operator --stack ${STACK} --from-stack ${FROM_STACK}
just gcp-pulumi preview --folder cloudnative-pg-operator --stack ${STACK}
just gcp-pulumi create-resource --folder cloudnative-pg-operator --stack ${STACK}
```

Check:

```bash
kubectl get pods -n operators
kubectl get crd | grep postgresql.cnpg.io
```

<a id="74-phase-c--arangodb"></a>

### 7.4 Phase C — ArangoDB

**C1 — Single instance** (`arangodb-single`)

Deploys ArangoDB **Single** mode, root password secret, and PVC via the StorageClass.

```bash
just gcp-pulumi new-stack-from --folder arangodb-single --stack ${STACK} --from-stack ${FROM_STACK}
just gcp-pulumi preview --folder arangodb-single --stack ${STACK}
just gcp-pulumi create-resource --folder arangodb-single --stack ${STACK}
```

**Dev vs HA:** config changes storage size / version only. Mode remains Single. HA Cluster mode is a repo gap.

Check:

```bash
kubectl get arangodeployment -A
kubectl get pods -n dev -l "arango_deployment=arangodb" 2>/dev/null || kubectl get pods -n dev | grep -i arango
```

Wait until the deployment reports ready before logical DB creation.

**C2 — Logical databases** (`create-arangodb-databases`)

Creates AppDB user secret + Job (`ensure-user` / `ensure-database` / `ensure-grant`). Default experiments list includes annotation, order, stock, content, and related DBs.

```bash
# Often no Pulumi.dev.yaml — init empty or copy from experiments/local
just gcp-pulumi new-stack-from --folder create-arangodb-databases --stack ${STACK} --from-stack ${FROM_STACK}
# or: just gcp-pulumi new-stack --folder create-arangodb-databases --stack ${STACK}
just gcp-pulumi preview --folder create-arangodb-databases --stack ${STACK}
just gcp-pulumi create-resource --folder create-arangodb-databases --stack ${STACK}
```

Check:

```bash
kubectl get jobs -n dev | grep create-databases
kubectl logs -n dev job/<job-name>
```

Job must complete successfully. Failures are usually wrong root secret name/key or Arango not reachable.

<a id="75-phase-d--postgresql-cloudnativepg"></a>

### 7.5 Phase D — PostgreSQL (CloudNativePG)

Depends on CNPG operator + StorageClass. Program also creates a GCS backup bucket and wires Barman backups using a GCP SA JSON referenced in config (`backupSecret.filepath`).

**Before apply (HA path):** edit stack config so the cluster entry has `instances: 3` (or desired odd number), adequate `storage.size` / WAL settings, and a unique backup bucket name in your project.

```bash
just gcp-pulumi new-stack-from --folder cloudnative-pg-cluster --stack ${STACK} --from-stack ${FROM_STACK}
# Adjust instances / buckets / secrets for your project, then:
just gcp-pulumi preview --folder cloudnative-pg-cluster --stack ${STACK}
just gcp-pulumi create-resource --folder cloudnative-pg-cluster --stack ${STACK}
```

Ensure the GCP SA JSON path in config points at a real file under `credentials/<project-id>/…` with rights to the backup bucket.

**Dev vs HA**

| Profile | `instances` | Notes |
|---------|-------------|-------|
| Simple dev | `1` | Matches shipped YAML |
| HA-oriented | `3` | CNPG manages primary/replicas; still place pods on durable node pool |

Check:

```bash
kubectl get clusters.postgresql.cnpg.io -A
kubectl get pods -n dev -l cnpg.io/cluster=logto   # name may differ per config
```

<a id="76-phase-e--redis"></a>

### 7.6 Phase E — Redis

```bash
just gcp-pulumi new-stack-from --folder redis-standalone --stack ${STACK} --from-stack ${FROM_STACK}
just gcp-pulumi preview --folder redis-standalone --stack ${STACK}
just gcp-pulumi create-resource --folder redis-standalone --stack ${STACK}
```

**HA:** not available — Deployment `replicas: 1` only.

Check:

```bash
kubectl get deploy,svc,pvc -n dev | grep -i redis
```

<a id="77-phase-f--minio"></a>

### 7.7 Phase F — MinIO

Object storage used alongside the data plane (credentials secret + Bitnami chart). Simple dev often disables web UI and API ingress; lab/prod-like stacks may enable ingress + larger disks (see `Pulumi.experiments.yaml`).

```bash
just gcp-pulumi new-stack-from --folder minio --stack ${STACK} --from-stack ${FROM_STACK}
just gcp-pulumi preview --folder minio --stack ${STACK}
just gcp-pulumi create-resource --folder minio --stack ${STACK}
```

**HA:** not implemented as distributed MinIO in this project.

Check:

```bash
kubectl get pods,svc,pvc -n dev | grep -i minio
```

## 8. Verify Database Readiness

Run after all phases for the chosen profile:

```bash
# Nodes + storage
kubectl get nodes
kubectl get storageclass dictycr-balanced

# Operators
kubectl get pods -n operators

# ArangoDB
kubectl get arangodeployment -A
kubectl get jobs -n dev | grep -i create-databases

# PostgreSQL (CNPG)
kubectl get clusters.postgresql.cnpg.io -A
kubectl get pods -n dev | grep -E 'logto|cnpg|postgres' || true

# Redis + MinIO
kubectl get deploy,svc -n dev | grep -iE 'redis|minio'
```

Pulumi-side (optional, per project):

```bash
pulumi -C arangodb-single -s ${STACK} stack output
pulumi -C cloudnative-pg-cluster -s ${STACK} stack output
```

All DB pods should be Running/Ready; CNPG cluster condition healthy; ArangoDB create-databases Job succeeded.

## 9. Database Setup Complete

You are done with **this** guide when:

1. StorageClass exists.
2. ArangoDB Single is up and logical DB Job succeeded.
3. CNPG operator + PostgreSQL cluster are healthy (1 instance for simple dev, or multi-instance if you configured HA).
4. Redis and MinIO are Running.

**Stop here.** Application Deployments, ingress for apps, graph loaders (`arangodb-dataloader`), and cluster backup (`install-velero`, optional `arangodb-backup`) are separate workstreams.

**Handoff checklist**

| Item | Where |
|------|--------|
| Cluster env file with `PULUMI_*` + `KUBECONFIG` + `PULUMI_BACKEND_URL` | `.env.<env>.<cluster>` |
| `pulumi-manager` key | `credentials/<project-id>/pulumi-manager.json` |
| Pulumi state | `gs://<pulumi-state-bucket>` |
| Stack name used | documented for the team (`dev` / `experiments` / custom) |
| Known HA gaps | Arango Single-only; Redis single; MinIO single — track if production needs more |

To tear down a single database project (careful — destructive):

```bash
just gcp-pulumi remove-resource --folder <project-folder> --stack ${STACK}
```

Prefer snapshot/backup policy before destroy on shared environments.
