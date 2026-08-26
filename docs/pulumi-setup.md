# Pulumi Setup Guide

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Prerequisites](#1-prerequisites)
  - [1.1 Cluster handoff](#11-cluster-handoff)
  - [1.2 Tools](#12-tools)
  - [1.3 Access](#13-access)
- [2. Environment Variables for Pulumi](#2-environment-variables-for-pulumi)
  - [2.1 Add to the cluster env file](#21-add-to-the-cluster-env-file)
  - [2.2 Activate the cluster shell](#22-activate-the-cluster-shell)
- [3. Pulumi Backend Bootstrap](#3-pulumi-backend-bootstrap)
  - [3.1 Create the pulumi-manager key](#31-create-the-pulumi-manager-key)
  - [3.2 KMS keyring and crypto key](#32-kms-keyring-and-crypto-key)
  - [3.3 GCS state bucket and login](#33-gcs-state-bucket-and-login)
- [4. Switching Between Clusters](#4-switching-between-clusters)
- [5. Stacks and Configuration](#5-stacks-and-configuration)
  - [5.1 Stack names](#51-stack-names)
  - [5.2 Creating a stack](#52-creating-a-stack)
  - [5.3 Recipe reference](#53-recipe-reference)
- [6. First Apply — StorageClass](#6-first-apply--storageclass)
  - [6.1 Why StorageClass first](#61-why-storageclass-first)
  - [6.2 Deploy storage_class](#62-deploy-storage_class)
  - [6.3 Verify](#63-verify)
- [7. Pulumi Setup Complete](#7-pulumi-setup-complete)
- [8. Next Steps](#8-next-steps)

**Status**:
- **Pulumi backend recipes** (`just gcp-pulumi`, KMS, `pulumi-manager`): aligned with this repo as of 2026-08-24.
- **Live `pulumi up` of a new backend**: not re-run in the session that rewrote this file.

> **Document type:** Provisioning guide. Starts where [`README.md`](../README.md) ends (cluster up and validated). Stops after StorageClass. ArangoDB install, import, and teardown: [`arangodb-deploy.md`](arangodb-deploy.md).

## Overview

This guide wires Pulumi to a healthy kOps cluster and applies the first data-plane resource (StorageClass).

- **Who it's for**: Operators who finished [kOps cluster creation](../README.md) through [Step 3.7](../README.md#step-37--validate-ha-topology--hardening).
- **What you'll end up with**: `pulumi-manager` key, KMS secrets provider, versioned GCS state bucket, cluster env file with `PULUMI_*` vars, StorageClass `dictycr-balanced`.
- **How long**: ~10–20 minutes after the cluster is Ready.
- **One project = one cluster**: Same rule as the kOps guide ([README §1.1](../README.md#11-the-gcp-project), [§1.5](../README.md#15-per-project-file-isolation)).

## Quick Setup

Work inside `just cluster-env --env <env> --cluster <cluster-name>`.

1. [Prerequisites](#1-prerequisites)
2. [Generate and activate cluster env](#2-environment-variables-for-pulumi)
3. [Bootstrap backend](#3-pulumi-backend-bootstrap) (`create-sa` → `create-keyring-and-key` → `pulumi-gcs-setup`)
4. [Apply StorageClass](#6-first-apply--storageclass)
5. Continue with [`arangodb-deploy.md`](arangodb-deploy.md)

## 1. Prerequisites

### 1.1 Cluster handoff

Finish [`README.md`](../README.md) first:

- Cluster env file active (`just cluster-env --env <env> --cluster <cluster-name>`)
- `kubectl get nodes` healthy
- CSI for GCE PD enabled (`spec.cloudProvider.gce.pdCSIDriver.enabled: true`). Without it, PVCs stay Pending.
- Storage-friendly topology if this cluster will host databases:
  - **Lab**: a small worker pool is enough
  - **Production HA**: private topology + multi-zone workers + `stateful-db` InstanceGroup ([README Step 3.2](../README.md#step-32--customize-yaml-in-git), [Step 3.7](../README.md#step-37--validate-ha-topology--hardening), [`kops-gcp-architecture.md`](kops-gcp-architecture.md))

### 1.2 Tools

Same baseline as the kOps guide ([README §1.2](../README.md#12-core-system-tools), [§1.4](../README.md#14-asdf-managed-tools)): `just`, `asdf` tools (`kubectl`, `pulumi`, `gcloud`), `jq`. `direnv` optional.

```bash
just install-tool --name <tool> --version <version>
pulumi version
gcloud version
```

### 1.3 Access

| Need | Why |
|------|-----|
| Working kubeconfig | Pulumi Kubernetes provider applies StorageClass |
| IAM to create SA keys / KMS / GCS | Pulumi manager identity and state |
| Paths under `credentials/<project-id>/` | Per-project isolation ([README §1.5](../README.md#15-per-project-file-isolation)) |

Do **not** put Pulumi credentials in `.envrc`. Put them in the per-cluster env file only.

## 2. Environment Variables for Pulumi

### 2.1 Add to the cluster env file

`create-cluster-env` writes gitignored `.env.<env>.<cluster-name>` ([README §1.3](../README.md#13-environment-variables--the-cluster-env-file)). It infers the GCP project from `config/kops/<cluster-name>/cluster.yaml` (or `$PROJECT_ID` / `--project`) and fills canonical paths:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> --force yes
```

| Variable | Default |
|----------|---------|
| `PROJECT_ID` | `spec.project` in `config/kops/<cluster-name>/cluster.yaml` |
| `GOOGLE_APPLICATION_CREDENTIALS` | `credentials/<project-id>/kops-cluster-creator.json` |
| `KUBECONFIG` | `clusters/<cluster-name>/kubeconfig` |
| `PULUMI_GCP_CREDENTIALS` | `credentials/<project-id>/pulumi-manager.json` |
| `PULUMI_SECRET_PROVIDER` | `gcpkms://projects/<project-id>/locations/us-central1/keyRings/<cluster-name>/cryptoKeys/<cluster-name>` |
| `PULUMI_BACKEND_URL` | `gs://pulumi-state-<project-id>` |
| `PULUMI_STACK` | `<cluster-name>` — the stack every project on this cluster uses ([§5.1](#51-stack-names)) |

Override with flags (e.g. `--pulumi-secret-provider "..."`) when a default is wrong. If `PROJECT_ID` cannot be inferred, the recipe stops and says so.

**Environment variable contract**: recipes read exported env vars first. CLI flags override. Missing required values abort with an error.

### 2.2 Activate the cluster shell

```bash
just cluster-env --env <env> --cluster <cluster-name>
```

The recipe prints which variables are set and which are still empty. Stay in that sub-shell for the rest of this guide. Type `exit` or Ctrl-D to leave.

## 3. Pulumi Backend Bootstrap

Once per GCP project, from the activated cluster shell.

### 3.1 Create the pulumi-manager key

```bash
just gcp-sa create-sa --sa-name pulumi-manager
```

Writes the JSON key to `${PULUMI_GCP_CREDENTIALS}`.

### 3.2 KMS keyring and crypto key

```bash
just gcp-kms create-keyring-and-key
```

Uses `${PULUMI_SECRET_PROVIDER}` and `${PULUMI_GCP_CREDENTIALS}`.

### 3.3 GCS state bucket and login

```bash
just gcp-pulumi pulumi-gcs-setup
```

Creates `${PULUMI_BACKEND_URL}` with versioning and runs `pulumi login`.

## 4. Switching Between Clusters

Each env file carries kubeconfig, credentials, KMS URI, and `PULUMI_BACKEND_URL`. Switch with:

```bash
exit
just cluster-env --env dev --cluster cluster-b
```

Before `pulumi up`, confirm the printed inventory shows a real `PULUMI_BACKEND_URL` and `kubectl get nodes` is the cluster you expect.

> **Common mistake.** `pulumi login` is global. A cluster shell without `PULUMI_BACKEND_URL` leaves Pulumi on the last backend. Set it in [§2.1](#21-add-to-the-cluster-env-file).

## 5. Stacks and Configuration

### 5.1 Stack names

One project (`arangodb-cluster`, `storage_class`, ...) can hold many stacks — one per cluster it is deployed to. **Do not reuse the same stack name across different clusters** for anything beyond throwaway lab work: a stack name should identify *which cluster* it targets, so `pulumi stack ls` and `pulumi -s <name> ...` are unambiguous even when you operate several clusters side by side.

| Profile | Stack name | Why |
|---------|-----------|-----|
| Simple lab | `dev` | Matches shipped `Pulumi.dev.yaml`; one shared throwaway cluster, name collision is fine |
| Shared lab | `experiments` | Same reasoning — one shared cluster |
| Any real cluster (staging, prod-us, prod-eu, ...) | the cluster's own name, e.g. `dcr-kube1` | Unique per cluster, never collides even if two clusters are both "production" |

`create-cluster-env` ([§2.1](#21-add-to-the-cluster-env-file)) already writes `PULUMI_STACK=<cluster-name>` for you. All `just gcp-pulumi` recipes below default `--stack` to `$PULUMI_STACK` when the flag is omitted, so day-to-day commands do not need to spell out a stack name at all. Override with `--pulumi-stack <name>` on `create-cluster-env` only if you deliberately want a name other than the cluster's own (e.g. sharing one stack across a small pool of near-identical clusters).

Each project directory holds `Pulumi.yaml` plus `Pulumi.<stack>.yaml`. Secrets use the KMS provider (`PULUMI_SECRET_PROVIDER` + manager key).

### 5.2 Creating a stack

From repo root, cluster shell active. `ensure-stack` selects the stack named by `$PULUMI_STACK` if it exists, or initializes it if not — the one-liner every other Pulumi command in this repo's guides should use instead of a raw `pulumi stack select || stack init`:

```bash
just gcp-pulumi ensure-stack --folder <project-folder>
```

To seed it from an existing stack's config instead of starting empty:

```bash
just gcp-pulumi new-stack-from --folder <project-folder> --from-stack <base-stack>
```

Set plain config keys directly against `$PULUMI_STACK` if you started empty:

```bash
pulumi -C <project-folder> -s "${PULUMI_STACK}" config set --path "<key>" "<value>"
```

For **secret** values, use `set-secret` instead of the raw `pulumi ... --secret` invocation:

```bash
just gcp-pulumi set-secret --folder <project-folder> --key "<key>" --value "<secret-value>"
```

Preview, then apply — both also default to `$PULUMI_STACK`:

```bash
just gcp-pulumi preview --folder <project-folder>
just gcp-pulumi create-resource --folder <project-folder>
```

Pass `--stack <name>` explicitly on any recipe to override `$PULUMI_STACK` for one command.

> **Do not** use `just pulumi-init-and-deploy` here. That entrypoint deploys many projects from `pulumi-files/resources/*.txt` in bulk. Run one project at a time.

### 5.3 Recipe reference

All of these accept `--stack <name>`; if omitted they use `$PULUMI_STACK`, falling back to `dev` only for the recipes that predate that variable.

| Recipe | What it does | Environment variables |
|--------|--------------|-----------------------|
| `just gcp-pulumi pulumi-gcs-setup` | Create/version GCS state bucket + `pulumi login` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL` |
| `just gcp-pulumi ensure-stack` | `stack select`, or `stack init` if it does not exist yet | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_STACK` (required) |
| `just gcp-pulumi set-secret` | `config set --path --secret <key> <value>` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_STACK` |
| `just gcp-pulumi new-stack` | `stack init` with KMS secrets provider | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi new-stack-from` | Init + copy config from another stack | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi preview` | `pulumi preview` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi create-resource` | `pulumi up -f -y` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi remove-resource` | `pulumi destroy -f -y` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi cleanup-resource` | `stack rm --preserve-config` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |

## 6. First Apply — StorageClass

Deploy once per cluster. Later PVCs (`dictycr-balanced`, `dictycr-ssd`) depend on it. Database sizing and class choice: [`kops-gcp-architecture.md` §6](kops-gcp-architecture.md#-6-database-storage--retrieval). Production ArangoDB consumes these classes in [`arangodb-deploy.md` §2](arangodb-deploy.md#2-install-arangodb).

### 6.1 Why StorageClass first

ArangoDB, CNPG, Redis, and MinIO all request these classes. Apply them before any database stack.

### 6.2 Deploy storage_class

Lab (`dev` / `experiments`, literal names):

```bash
just gcp-pulumi new-stack-from --folder storage_class --stack dev --from-stack experiments
just gcp-pulumi preview --folder storage_class --stack dev
just gcp-pulumi create-resource --folder storage_class --stack dev
```

Any real cluster (uses `$PULUMI_STACK`, no `--stack` needed):

```bash
just gcp-pulumi ensure-stack --folder storage_class
just gcp-pulumi preview --folder storage_class
just gcp-pulumi create-resource --folder storage_class
```

### 6.3 Verify

```bash
kubectl get storageclass dictycr-balanced
```

Expect `PROVISIONER` `pd.csi.storage.gke.io`. On a production stack, also check `dictycr-ssd`.

## 7. Pulumi Setup Complete

| Item | Where |
|------|--------|
| Cluster env with `PULUMI_*` + `KUBECONFIG` | `.env.<env>.<cluster>` |
| `pulumi-manager` key | `credentials/<project-id>/pulumi-manager.json` |
| Pulumi state | `gs://<pulumi-state-bucket>` |
| Stack name | `$PULUMI_STACK` (defaults to the cluster name; see [§5.1](#51-stack-names)) |
| StorageClass | `dictycr-balanced` (and `dictycr-ssd` for prod) |

Tear down only StorageClass (destructive if PVCs still reference it):

```bash
just gcp-pulumi remove-resource --folder storage_class --stack <stack-name>
```

## 8. Next Steps

| Document | Use it for |
|----------|------------|
| [`README.md`](../README.md) | Cluster bootstrap, [env files](../README.md#13-environment-variables--the-cluster-env-file), [day-2](../README.md#4-day-2-operations-git-first-workflow), [teardown](../README.md#6-disposable-cluster-lifecycle) |
| [`kops-gcp-architecture.md`](kops-gcp-architecture.md) | HA vs cost, [stateful-db pool](kops-gcp-architecture.md#-4-worker-node-capacity-disk-sizing--elasticity), [database storage](kops-gcp-architecture.md#-6-database-storage--retrieval) |
| [`arangodb-deploy.md`](arangodb-deploy.md) | Production ArangoDB Cluster after StorageClass |
| [`plans/arangodb-production.md`](plans/arangodb-production.md) | Why Cluster mode, frozen lab stacks, remaining gaps |
