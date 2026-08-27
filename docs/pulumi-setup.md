# Pulumi Setup Guide

Wires Pulumi to a healthy kOps cluster and applies the first data-plane resource (StorageClass).

Starts where [`README.md`](../README.md) ends (cluster up and validated). Stops after StorageClass — ArangoDB install, import, and teardown live in [`arangodb-deploy.md`](arangodb-deploy.md).

**Status**:
- **Pulumi backend recipes** (`just gcp-pulumi`, KMS, `pulumi-manager`): aligned with this repo as of 2026-08-24.
- **Live `pulumi up` of a new backend**: not re-run in the session that rewrote this file.

**You end up with**: `pulumi-manager` key, KMS secrets provider, versioned GCS state bucket, cluster env file with `PULUMI_*` vars, StorageClass `dictycr-balanced`. Takes ~10–20 minutes once the cluster is Ready.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Prerequisites](#1-prerequisites)
- [2. Cluster Environment](#2-cluster-environment)
- [3. Backend Bootstrap](#3-backend-bootstrap)
- [4. Switching Between Clusters](#4-switching-between-clusters)
- [5. Stacks and Configuration](#5-stacks-and-configuration)
- [6. First Apply — StorageClass](#6-first-apply--storageclass)
- [7. Setup Complete](#7-setup-complete)
- [8. Related Documents](#8-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# 1. Verify toolchain
just gcp-pulumi check-tools

# 2. Generate the cluster env file
just create-cluster-env --env <env> --cluster <cluster-name> --force yes

# 3. Enter the cluster shell — stay here for everything below
just cluster-env --env <env> --cluster <cluster-name>

# 4. Bootstrap the backend (once per GCP project)
just gcp-sa create-sa --sa-name pulumi-manager
just gcp-kms create-keyring-and-key
just gcp-pulumi pulumi-gcs-setup

# 5. Verify backend wiring
just gcp-pulumi check-backend

# 6. Apply StorageClass
just gcp-pulumi ensure-stack --folder storage_class
just gcp-pulumi preview --folder storage_class
just gcp-pulumi create-resource --folder storage_class

# 7. Verify StorageClass (prod ships both classes; lab ships balanced only)
just gcp-pulumi check-storageclass --classes dictycr-balanced,dictycr-ssd

# 8. Continue with arangodb-deploy.md
```

Switching to another cluster later:
```bash
exit
just cluster-env --env <env> --cluster <other-cluster>
just gcp-pulumi check-backend
```

---

## 1. Prerequisites

Cluster must be up and validated through [README Step 3.7](../README.md#step-37--validate-ha-topology--hardening), with the PD CSI driver enabled — without it PVCs stay Pending.
→ [Prerequisites detail](reference/pulumi/prerequisites.md)

```bash
just gcp-pulumi check-tools
```

Prints PASS/FAIL and the version for `pulumi`, `gcloud`, `kubectl`, and `jq`.

---

## 2. Cluster Environment

`create-cluster-env` writes a gitignored `.env.<env>.<cluster-name>` holding every `PULUMI_*` variable, inferred from `config/kops/<cluster-name>/cluster.yaml`.
→ [Cluster env detail](reference/pulumi/cluster-env.md)

```bash
just create-cluster-env --env <env> --cluster <cluster-name> --force yes
just cluster-env --env <env> --cluster <cluster-name>
```

Stay in the sub-shell that `cluster-env` opens for the rest of this guide. Credentials belong in this file, never in `.envrc`.

---

## 3. Backend Bootstrap

Once per GCP project, from the activated cluster shell. Creates the manager identity, the KMS key that encrypts Pulumi secrets, and the versioned state bucket.
→ [Backend bootstrap detail](reference/pulumi/backend-bootstrap.md)

```bash
just gcp-sa create-sa --sa-name pulumi-manager
just gcp-kms create-keyring-and-key
just gcp-pulumi pulumi-gcs-setup
just gcp-pulumi check-backend
```

`check-backend` confirms the `PULUMI_*` variables, the bucket and its versioning, the KMS key, and that the active `pulumi login` points where this shell expects.

---

## 4. Switching Between Clusters

Each env file carries its own kubeconfig, credentials, KMS URI, and backend URL. Leave one sub-shell and enter another — there is no in-place switch.
→ [Switching detail](reference/pulumi/switching-clusters.md)

```bash
exit
just cluster-env --env dev --cluster cluster-b
just gcp-pulumi check-backend
```

> `pulumi login` is machine-wide. A cluster shell missing `PULUMI_BACKEND_URL` silently leaves Pulumi on the previous backend, so run `check-backend` after every switch.

---

## 5. Stacks and Configuration

One stack per cluster per project; `create-cluster-env` already set `PULUMI_STACK` to the cluster name, so `--stack` is rarely needed.
→ [Stack names](reference/pulumi/stack-names.md) · [Stack config](reference/pulumi/stack-config.md) · [Full recipe reference](reference/pulumi/recipes.md)

```bash
just gcp-pulumi ensure-stack --folder <project-folder>
just gcp-pulumi set-config --folder <project-folder> --key "<key>" --value "<value>"
just gcp-pulumi set-secret --folder <project-folder> --key "<key>" --value "<secret-value>"
just gcp-pulumi preview --folder <project-folder>
just gcp-pulumi create-resource --folder <project-folder>
```

> Do **not** use `just pulumi-init-and-deploy` here — it deploys many projects in bulk. Run one project at a time.

---

## 6. First Apply — StorageClass

Deploy once per cluster, before any database stack — ArangoDB, CNPG, Redis, and MinIO all request these classes.
→ [StorageClass detail](reference/pulumi/storage-class.md)

```bash
just gcp-pulumi ensure-stack --folder storage_class
just gcp-pulumi preview --folder storage_class
just gcp-pulumi create-resource --folder storage_class
just gcp-pulumi check-storageclass --classes dictycr-balanced,dictycr-ssd
```

`Pulumi.prod.yaml` declares **both** `dictycr-balanced` and `dictycr-ssd`, so name both when verifying a production cluster — the bare `check-storageclass` default only requires `dictycr-balanced` and would pass while `dictycr-ssd` is missing.

Expect provisioner `pd.csi.storage.gke.io`. Lab and local stacks differ — see the [StorageClass detail](reference/pulumi/storage-class.md#deploy--lab-stacks).

Sizing and class choice: [`kops-gcp-architecture.md` §6](kops-gcp-architecture.md#-6-database-storage--retrieval).

---

## 7. Setup Complete

| Item | Where |
|------|--------|
| Cluster env with `PULUMI_*` + `KUBECONFIG` | `.env.<env>.<cluster>` |
| `pulumi-manager` key | `credentials/<project-id>/pulumi-manager.json` |
| Pulumi state | `gs://pulumi-state-<project-id>` |
| Stack name | `$PULUMI_STACK` (defaults to the cluster name) |
| StorageClass | `dictycr-balanced` (and `dictycr-ssd` for prod) |

Next: production ArangoDB in [`arangodb-deploy.md`](arangodb-deploy.md).

Tearing down only the StorageClass is destructive if PVCs still reference it — see [StorageClass detail](reference/pulumi/storage-class.md#teardown).

---

## 8. Related Documents

**Reference details for this guide:**
- [Prerequisites](reference/pulumi/prerequisites.md)
- [Cluster environment file](reference/pulumi/cluster-env.md)
- [Backend bootstrap](reference/pulumi/backend-bootstrap.md)
- [Switching between clusters](reference/pulumi/switching-clusters.md)
- [Stack names](reference/pulumi/stack-names.md)
- [Stack configuration](reference/pulumi/stack-config.md)
- [Recipe reference](reference/pulumi/recipes.md)
- [StorageClass](reference/pulumi/storage-class.md)

**Other documentation:**

| Document | Use it for |
|----------|------------|
| [`README.md`](../README.md) | Cluster bootstrap, [env files](../README.md#13-environment-variables--the-cluster-env-file), [day-2](../README.md#4-day-2-operations-git-first-workflow), [teardown](../README.md#6-disposable-cluster-lifecycle) |
| [`kops-gcp-architecture.md`](kops-gcp-architecture.md) | HA vs cost, [stateful-db pool](kops-gcp-architecture.md#-4-worker-node-capacity-disk-sizing--elasticity), [database storage](kops-gcp-architecture.md#-6-database-storage--retrieval) |
| [`arangodb-deploy.md`](arangodb-deploy.md) | Production ArangoDB Cluster after StorageClass |
| [`plans/arangodb-production.md`](plans/arangodb-production.md) | Why Cluster mode, frozen lab stacks, remaining gaps |
