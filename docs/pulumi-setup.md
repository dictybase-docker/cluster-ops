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
  - [5.2 Config sources](#52-config-sources)
  - [5.3 Creating a stack](#53-creating-a-stack)
  - [5.4 Recipe reference](#54-recipe-reference)
- [6. First Apply — StorageClass](#6-first-apply--storageclass)
  - [6.1 Why StorageClass first](#61-why-storageclass-first)
  - [6.2 Deploy storage_class](#62-deploy-storage_class)
  - [6.3 Verify](#63-verify)
- [7. Pulumi Setup Complete](#7-pulumi-setup-complete)
- [8. Next Steps](#8-next-steps)

**Status**:
- **Pulumi backend recipes** (`just gcp-pulumi`, KMS, `pulumi-manager`): aligned with this repo as of 2026-08-24.
- **Live `pulumi up` of a new backend**: not re-run in the session that rewrote this file.

> **Document type:** This is a **provisioning guide** — a sequential
> walkthrough for wiring Pulumi to a kOps cluster and applying the first
> data-plane resource (StorageClass). It starts where [`README.md`](../README.md)
> ends (cluster up and validated). It **stops at the start of ArangoDB**.
> ArangoDB install, import, and teardown live in [`arangodb-deploy.md`](arangodb-deploy.md).

## Overview

This guide takes you from a healthy kOps cluster to a working Pulumi backend and the StorageClass every database PVC needs. After Section 7 you are ready to start ArangoDB — not to finish the data plane.

- **Who it's for**: Operators who finished [kOps cluster creation](../README.md) through [Step 3.7](../README.md#step-37--validate-ha-topology--hardening).
- **What you'll end up with**: `pulumi-manager` key, KMS secrets provider, versioned GCS state bucket, `PULUMI_BACKEND_URL` in the cluster env file, and StorageClass `dictycr-balanced`.
- **How long**: ~10–20 minutes after the cluster is Ready (GCS + KMS + one Pulumi apply).
- **One project = one cluster**: Same rule as the kOps guide. SA keys live under `credentials/<project-id>/`.
- **Out of scope here**: ArangoDB operator and instance, CNPG, Redis, MinIO, loaders, Velero, application charts.

## Quick Setup

Work inside `just cluster-env --env <env> --cluster <cluster-name>`. `kubectl get nodes` must already work.

1. [Confirm cluster handoff.](#11-cluster-handoff) Cluster validated; CSI `pdCSIDriver` enabled.
2. [Confirm tools.](#12-tools) `just`, `kubectl`, `pulumi`, `gcloud`, `jq`.
3. [Configure all Pulumi env vars.](#21-add-to-the-cluster-env-file) Run `just create-cluster-env` with all `--pulumi-*` flags and enter the shell.
4. [Create pulumi-manager key.](#31-create-the-pulumi-manager-key) `just gcp-sa create-sa ...` writing to `${PULUMI_GCP_CREDENTIALS}`.
5. [Create KMS key.](#32-kms-keyring-and-crypto-key) `just gcp-kms create-keyring-and-key ...` matching `${PULUMI_SECRET_PROVIDER}`.
6. [Create state bucket & initialize login.](#33-gcs-state-bucket-and-login) `just gcp-pulumi pulumi-gcs-setup ...`.
7. [Verify switching & state.](#4-switching-between-clusters) `echo "$PULUMI_BACKEND_URL"` and `pulumi -C storage_class stack ls`.
8. [Apply StorageClass.](#62-deploy-storage_class) `just gcp-pulumi preview` then `create-resource` on `storage_class`.
9. **Stop.** Go to [`arangodb-deploy.md`](arangodb-deploy.md) for ArangoDB.

## 1. Prerequisites

### 1.1 Cluster handoff

Finish [`README.md`](../README.md) first:

- Cluster env file active (`just cluster-env --env <env> --cluster <cluster-name>`)
- `kubectl get nodes` healthy
- CSI for GCE PD enabled (`spec.cloudProvider.gce.pdCSIDriver.enabled: true`)
- Storage-friendly topology if this cluster will host databases:
  - **Lab**: a small worker pool is enough
  - **Production HA**: private topology + multi-zone workers + `stateful-db` InstanceGroup ([README Step 3.2](../README.md#step-32--customize-yaml-in-git), [Step 3.7](../README.md#step-37--validate-ha-topology--hardening), [`kops-gcp-architecture.md`](kops-gcp-architecture.md))

StorageClass creation assumes the CSI driver. Without it, PVCs stay Pending.

### 1.2 Tools

Same baseline as the kOps guide: `just`, `asdf` tools (`kubectl`, `pulumi`, `gcloud`), `jq`. `direnv` optional.

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
| Paths under `credentials/<project-id>/` | Per-project isolation, same as kOps |

Do **not** put Pulumi credentials in `.envrc`. Put them in the per-cluster env file only.

## 2. Environment Variables for Pulumi

### 2.1 Add to the cluster env file

Define all Pulumi variables upfront in the gitignored `.env.<env>.<cluster-name>` file alongside kOps operator credentials. Generate or refresh the env file with `just create-cluster-env`:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> --force yes \
  --credentials credentials/<project-id>/kops-cluster-creator.json \
  --kubeconfig clusters/<cluster-name>/kubeconfig \
  --pulumi-gcp-credentials credentials/<project-id>/pulumi-manager.json \
  --pulumi-secret-provider "gcpkms://projects/<project-id>/locations/us-central1/keyRings/<keyring>/cryptoKeys/<key>" \
  --pulumi-backend-url "gs://<pulumi-state-bucket>"
```

**Environment Variable Contract**:
- `PULUMI_GCP_CREDENTIALS`: Path to the service account key for Pulumi operations (`credentials/<project-id>/pulumi-manager.json`).
- `PULUMI_SECRET_PROVIDER`: Full GCP KMS cryptoKey URI for encrypting stack secrets.
- `PULUMI_BACKEND_URL`: GCS bucket URI (`gs://<pulumi-state-bucket>`) for state storage.
- `PROJECT_ID`: Target GCP project ID.

**How variables are consumed**:
- **Implicitly in `just gcp-pulumi` recipes**: Operational recipes (`preview`, `new-stack`, `new-stack-from`, `create-resource`, `remove-resource`) automatically read `${PULUMI_GCP_CREDENTIALS}` for GCP authentication and `${PULUMI_SECRET_PROVIDER}` for KMS encryption. They fail fast (`set -u`) if these variables are unset.
- **Implicitly by the `pulumi` CLI**: The `pulumi` CLI automatically uses `${PULUMI_BACKEND_URL}` for GCS state backend routing.
- **Explicitly in bootstrap helper commands**: Setup recipes (`gcp-sa create-sa`, `gcp-kms create-keyring-and-key`, `pulumi-gcs-setup`) require specific CLI flags, which you supply directly from these exported environment variables (e.g. `--output-file "${PULUMI_GCP_CREDENTIALS}"`).

### 2.2 Activate the cluster shell

```bash
just cluster-env --env <env> --cluster <cluster-name>
```

Confirm exported variables:

```bash
echo "$PULUMI_GCP_CREDENTIALS"
echo "$PULUMI_SECRET_PROVIDER"
echo "$PULUMI_BACKEND_URL"
kubectl get nodes
```

## 3. Pulumi Backend Bootstrap

Execute bootstrap recipes directly from within the activated `just cluster-env` shell.

### 3.1 Create the pulumi-manager key

Generate the service account key at the path specified by `${PULUMI_GCP_CREDENTIALS}`:

```bash
mkdir -p "credentials/${PROJECT_ID}"
just gcp-sa create-sa --sa-name pulumi-manager \
  --roles-file gcs-files/roles-permissions/pulumi-manager-roles.txt \
  --output-file "${PULUMI_GCP_CREDENTIALS}"
```

Roles assigned include Storage Admin and Cloud KMS CryptoKey Encrypter/Decrypter.

### 3.2 KMS keyring and crypto key

Create the KMS keyring and key matching the `${PULUMI_SECRET_PROVIDER}` URI:

```bash
just gcp-kms create-keyring-and-key \
  --keyring-name <keyring-name> \
  --key-name <key-name> \
  --credentials-file "${PULUMI_GCP_CREDENTIALS}" \
  --location us-central1
```

### 3.3 GCS state bucket and login

Create the state bucket and initialize the Pulumi backend:

```bash
just gcp-pulumi pulumi-gcs-setup \
  --sa-json-path "${PULUMI_GCP_CREDENTIALS}" \
  --gcs-bucket <pulumi-state-bucket> \
  --location us-central1
```

`pulumi-gcs-setup` creates the bucket with versioning enabled and executes `pulumi login "gs://<pulumi-state-bucket>"`.

Whenever you update or add environment variables in `.env.<env>.<cluster-name>`, refresh the active shell:

```bash
# Refresh shell with updated cluster env file
just cluster-env --env <env> --cluster <cluster-name>
```

## 4. Switching Between Clusters

Each cluster env file carries `KUBECONFIG`, GCP credentials, KMS key, **and** `PULUMI_BACKEND_URL`. Switching is one command:

```bash
exit
just cluster-env --env dev --cluster cluster-b
pulumi -C storage_class stack ls
```

Verify before any `pulumi up`:

```bash
echo "$PULUMI_BACKEND_URL"
pulumi -C storage_class stack ls
kubectl get nodes
```

> **Common mistake.** `pulumi login` is global. `just cluster-env` without `PULUMI_BACKEND_URL` leaves Pulumi on the last backend — often the wrong project. Set the env var in [§2.1](#21-add-to-the-cluster-env-file).

## 5. Stacks and Configuration

### 5.1 Stack names

Examples in-repo: `dev`, `experiments`, `local`. Pick one name and reuse it for every project you deploy on that cluster.

| Profile | Typical stack | Notes |
|---------|---------------|-------|
| Simple lab | `dev` | Matches many shipped `Pulumi.dev.yaml` files |
| Shared lab | `experiments` | Larger disks in several projects |
| Production | `prod` (new) | New files only — do not overwrite lab stacks. See [`arangodb-deploy.md`](arangodb-deploy.md) |

### 5.2 Config sources

- Each project directory holds `Pulumi.yaml` plus `Pulumi.<stack>.yaml`.
- Secrets use the KMS provider — you need `PULUMI_SECRET_PROVIDER` and the manager key.
- Copy from a known stack when one exists:

```bash
just gcp-pulumi new-stack-from --folder <project-folder> --stack <stack> --from-stack <from-stack>
```

When no base stack exists:

```bash
just gcp-pulumi new-stack --folder <project-folder> --stack <stack>
pulumi -C <folder> -s <stack> config set --path "<key>" "<value>"
```

### 5.3 Creating a stack

Run stack operations directly from the repository root with your cluster environment active:

**When a base configuration exists to copy from:**

```bash
just gcp-pulumi new-stack-from --folder <project-folder> --stack <stack-name> --from-stack <base-stack>
```

**When initializing a fresh stack without a base:**

```bash
just gcp-pulumi new-stack --folder <project-folder> --stack <stack-name>
```

**Preview and apply:**

Always preview changes before applying:

```bash
just gcp-pulumi preview --folder <project-folder> --stack <stack-name>
just gcp-pulumi create-resource --folder <project-folder> --stack <stack-name>
```

> **Do not** use `just pulumi-init-and-deploy` for this guide. That entrypoint deploys non-database projects from `pulumi-files/resources/*.txt` in bulk. Run individual projects one at a time.

### 5.4 Recipe reference

| Recipe | What it does | Environment Variable Dependencies |
|--------|--------------|-----------------------------------|
| `just gcp-pulumi pulumi-gcs-setup` | Creates/versions GCS state bucket and runs `pulumi login` | `PULUMI_GCP_CREDENTIALS` |
| `just gcp-pulumi new-stack` | Initializes stack with KMS secrets provider | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_BACKEND_URL` |
| `just gcp-pulumi new-stack-from` | Initializes stack copying config from another stack | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_BACKEND_URL` |
| `just gcp-pulumi preview` | Runs `pulumi preview` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL` |
| `just gcp-pulumi create-resource` | Runs non-interactive `pulumi up` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL` |
| `just gcp-pulumi remove-resource` | Runs non-interactive `pulumi destroy` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL` |
| `just gcp-pulumi cleanup-resource` | Removes stack metadata (`stack rm`) | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL` |

## 6. First Apply — StorageClass

This is the last resource in **this** guide. ArangoDB is next.

### 6.1 Why StorageClass first

Every later PVC (`dictycr-balanced`, `dictycr-ssd`) needs storage classes defined before pods request volumes. Deploy storage classes once per cluster.

### 6.2 Deploy storage_class

Creates the GCE PD StorageClass (`dictycr-balanced` for standard disk, plus `dictycr-ssd` on prod):

**For lab clusters (`dev` / `experiments`):**

```bash
just gcp-pulumi new-stack-from --folder storage_class --stack dev --from-stack experiments
just gcp-pulumi preview --folder storage_class --stack dev
just gcp-pulumi create-resource --folder storage_class --stack dev
```

**For production clusters (`prod`):**

```bash
just gcp-pulumi preview --folder storage_class --stack prod
just gcp-pulumi create-resource --folder storage_class --stack prod
```

### 6.3 Verify

```bash
kubectl get storageclass dictycr-balanced
echo "$PULUMI_BACKEND_URL"
pulumi -C storage_class -s <stack-name> stack output
```

Expect the StorageClass to exist and `PROVISIONER` to be the GCE PD CSI driver (`pd.csi.storage.gke.io`).

## 7. Pulumi Setup Complete

You are done with **this** guide when:

1. Cluster env file has `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, and `PULUMI_BACKEND_URL`.
2. `pulumi-manager.json` exists under `credentials/<project-id>/`.
3. `just cluster-env` loads the correct backend URL.
4. `kubectl get storageclass dictycr-balanced` succeeds.

**Handoff checklist**

| Item | Where |
|------|--------|
| Cluster env with `PULUMI_*` + `KUBECONFIG` + `PULUMI_BACKEND_URL` | `.env.<env>.<cluster>` |
| `pulumi-manager` key | `credentials/<project-id>/pulumi-manager.json` |
| Pulumi state | `gs://<pulumi-state-bucket>` |
| Stack name | documented (`dev` / `experiments` / `prod`) |
| StorageClass | `dictycr-balanced` (and `dictycr-ssd` for prod) |

To tear down only the StorageClass (destructive if PVCs still reference it):

```bash
just gcp-pulumi remove-resource --folder storage_class --stack <stack-name>
```

**Stop here.** Do not install the ArangoDB operator or instance in this guide.

## 8. Next Steps

- **ArangoDB (production lifecycle):** [`arangodb-deploy.md`](arangodb-deploy.md). Implementation plan: [`plans/arangodb-production.md`](plans/arangodb-production.md).
- **Architecture (pool sizing, HA):** [`kops-gcp-architecture.md`](kops-gcp-architecture.md).
- **Cluster day-2 / teardown:** [`README.md`](../README.md) §§4 and 6.
