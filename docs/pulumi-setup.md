# Pulumi Setup Guide

Wires Pulumi to a healthy kOps cluster and applies the first data-plane resources (StorageClass, then the shared namespaces).

Starts where [`kops-setup.md`](kops-setup.md) ends (cluster up and validated). You arrive on the least-privilege `kops-cluster-creator` identity — §3 rotates to `sa-manager` because the backend bootstrap needs admin roles the creator lacks. Stops after the shared namespaces — ArangoDB install, import, and teardown live in [`arangodb-deploy.md`](arangodb-deploy.md).

**Status**:
- **Pulumi backend recipes** (`just gcp-pulumi`, KMS, `pulumi-manager`): aligned with this repo as of 2026-08-24.
- **Live `pulumi up` of a new backend**: not re-run in the session that rewrote this file.

**You end up with**: `pulumi-manager` key, KMS secrets provider, versioned GCS state bucket, cluster env file with `PULUMI_*` vars and the per-cluster tool-manifest selector, plus the selected `.tool-versions.<env>.<cluster>` manifest, StorageClass `dictycr-balanced`, shared namespaces `operators` + app namespace via the `namespace-bootstrap` stack. Takes ~10–20 minutes once the cluster is Ready.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Prerequisites](#1-prerequisites)
- [2. Cluster Environment](#2-cluster-environment)
- [3. Backend Bootstrap](#3-backend-bootstrap)
- [4. Switching Between Clusters](#4-switching-between-clusters)
- [5. First Apply — StorageClass and Namespaces](#5-first-apply--storageclass-and-namespaces)
- [6. Setup Complete](#6-setup-complete)
- [7. Related Documents](#7-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# 1. Verify toolchain
just gcp-pulumi check-tools

# 2. Generate the cluster env file and select its tool manifest
just create-cluster-env --env <env> --cluster <cluster-name> --force yes

# 3. Enter the cluster shell — stay here for everything below
just cluster-env --env <env> --cluster <cluster-name>

# 4. Rotate to the admin identity, then re-enter the shell
just gcp-cluster rotate-to-manager
exit
just cluster-env --env <env> --cluster <cluster-name>

# 5. Bootstrap the backend (once per GCP project)
just gcp-pulumi bootstrap-backend

# 6. Apply and verify StorageClass, then the shared namespaces
just gcp-pulumi apply-storageclass
just gcp-pulumi apply-namespaces

# 7. Continue with arangodb-deploy.md
```

Switching to another cluster later:
```bash
exit
just cluster-env --env <env> --cluster <other-cluster>
just gcp-pulumi check-backend
```

Stack names, per-stack configuration, and the full recipe list: [Stack names](reference/pulumi/stack-names.md) · [Stack config](reference/pulumi/stack-config.md) · [Recipe reference](reference/pulumi/recipes.md).

---

## 1. Prerequisites

Cluster must be up and validated through [`kops-setup.md` §3](kops-setup.md#3-cluster-bootstrap-git-native-flow), with the PD CSI driver enabled — without it PVCs stay Pending.
→ [Prerequisites detail](reference/pulumi/prerequisites.md)

```bash
just gcp-pulumi check-tools
```

---

## 2. Cluster Environment

`create-cluster-env` writes the gitignored `.env.<env>.<cluster-name>` holding every `PULUMI_*` variable and the per-cluster asdf manifest selector, creating `.tool-versions.<env>.<cluster>` from the repo manifest when that file is missing.
Everything below runs inside the sub-shell `cluster-env` opens — leave it with `exit`.
→ [Cluster env detail](reference/pulumi/cluster-env.md)

```bash
just create-cluster-env --env <env> --cluster <cluster-name> --force yes
just cluster-env --env <env> --cluster <cluster-name>
```

---

## 3. Backend Bootstrap

Creates the `pulumi-manager` key, the KMS keyring and crypto key, and the versioned GCS state bucket, then verifies the wiring. Once per GCP project.
→ [Backend bootstrap detail](reference/pulumi/backend-bootstrap.md) · [stages](reference/pulumi/backend-bootstrap.md#stages)

**Rotate to `sa-manager` first — skip if this shell already carries it:**

```bash
just gcp-cluster rotate-to-manager
exit
just cluster-env --env <env> --cluster <cluster-name>
```

**Then bootstrap:**

```bash
just gcp-pulumi bootstrap-backend
```

---

## 4. Switching Between Clusters

Each env file carries its own kubeconfig, credentials, KMS URI, backend URL, and tool-manifest selector. Leave one sub-shell and enter another — there is no in-place switch. `pulumi login` is machine-wide, so verify the wiring after every switch.
→ [Switching detail](reference/pulumi/switching-clusters.md) · [Backend verification](reference/pulumi/check-backend.md)

```bash
exit
just cluster-env --env dev --cluster cluster-b
just gcp-pulumi check-backend
```

---

## 5. First Apply — StorageClass and Namespaces

Deploy once per cluster, before any database stack. StorageClass first — ArangoDB, CNPG, Redis, and MinIO all request these classes, and PVCs referencing a missing class stay Pending indefinitely.
→ [StorageClass detail](reference/pulumi/storage-class.md)

```bash
just gcp-pulumi apply-storageclass
```

Then the shared namespaces — the `operators` namespace every operator Helm release targets, and the app namespace (`prod` on production clusters). The `namespace-bootstrap` stack is the only writer of both; operator programs and secret stacks probe it instead of creating namespaces themselves.
→ [Namespaces detail](reference/pulumi/namespaces.md)

```bash
just gcp-pulumi apply-namespaces
```

---

## 6. Setup Complete

| Item | Where |
|------|--------|
| Cluster env with `PULUMI_*` + `KUBECONFIG` | `.env.<env>.<cluster>` |
| `pulumi-manager` key | `credentials/<project-id>/pulumi-manager.json` |
| Pulumi state | `gs://pulumi-state-<project-id>` |
| Stack name | `$PULUMI_STACK` (defaults to the cluster name) |
| StorageClass | `dictycr-balanced` (and `dictycr-ssd` for prod) |
| Shared namespaces | `operators` + `prod` (prod cluster) via the `namespace-bootstrap` stack |

Next: production ArangoDB in [`arangodb-deploy.md`](arangodb-deploy.md).

Tearing down only the StorageClass is destructive if PVCs still reference it — see [StorageClass detail](reference/pulumi/storage-class.md#teardown).

---

## 7. Related Documents

**Reference details for this guide:**
- [Prerequisites](reference/pulumi/prerequisites.md)
- [Cluster environment file](reference/pulumi/cluster-env.md)
- [Backend bootstrap](reference/pulumi/backend-bootstrap.md)
- [Backend verification](reference/pulumi/check-backend.md)
- [Switching between clusters](reference/pulumi/switching-clusters.md)
- [Stack names](reference/pulumi/stack-names.md)
- [Stack configuration](reference/pulumi/stack-config.md)
- [Recipe reference](reference/pulumi/recipes.md)
- [StorageClass](reference/pulumi/storage-class.md)
- [Shared namespaces](reference/pulumi/namespaces.md)

**Other documentation:**

| Document | Use it for |
|----------|------------|
| [`kops-setup.md`](kops-setup.md) | Cluster bootstrap, [env files](kops-setup.md#1-prerequisites--execution-context), [day-2](kops-setup.md#4-day-2-operations-git-first-workflow), [teardown](kops-setup.md#6-disposable-cluster-lifecycle) |
| [`kops-gcp-architecture.md`](kops-gcp-architecture.md) | HA vs cost, [stateful-db pool](kops-gcp-architecture.md#4-worker-node-capacity-disk-sizing--elasticity), [database storage](kops-gcp-architecture.md#6-database-storage--retrieval) |
| [`arangodb-deploy.md`](arangodb-deploy.md) | Production ArangoDB Cluster after StorageClass |
| [`plans/arangodb-production.md`](plans/arangodb-production.md) | Why Cluster mode, frozen lab stacks, remaining gaps |
