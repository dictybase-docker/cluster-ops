# kOps Cluster Setup Guide

Bootstraps a kops-managed Kubernetes cluster on GCP from a fresh clone, using declarative Git-first manifests. Ends with a running, validated HA cluster ready for [Pulumi](pulumi-setup.md).

Takes ~30–45 minutes for a clean run — most of it waiting for GCE instances to boot and pass health checks.

**Status**:
- **Tooling & Infrastructure recipes**: Tested 2026-08-20.
- **Declarative Git bundle (`_starter`) schema**: Verified cross-version compatible on kops v1.29.2 (dev) and v1.36.1 (prod) — both `cluster.yaml` and `instancegroups.yaml` pass `kops replace` validation on both binaries. Full end-to-end re-creation still pending first live-target run.

> **Two stores, one handoff.** Before [§3](#3-cluster-bootstrap-git-native-flow), the env file `.env.<env>.<cluster>` holds `PROJECT_ID`, credentials, local paths, and the per-cluster tool-manifest selector. From §3 onward, cluster identity and shape live **only** in Git under `config/kops/<cluster>/`.
>
> **Once inside `just cluster-env`, stop typing `--cluster` and `--project`.** The shell exports `CLUSTER_NAME` and `PROJECT_ID`; every recipe below reads them automatically. Pass a flag only to override, or when a command runs *before* you've entered the shell.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Prerequisites & Execution Context](#1-prerequisites--execution-context)
- [2. Service Accounts & Authentication](#2-service-accounts--authentication)
- [3. Cluster Bootstrap (Git-Native Flow)](#3-cluster-bootstrap-git-native-flow)
- [4. Day-2 Operations (Git-First Workflow)](#4-day-2-operations-git-first-workflow)
- [5. Exploring the Cluster](#5-exploring-the-cluster)
- [6. Disposable Cluster Lifecycle](#6-disposable-cluster-lifecycle)
  - [6.1 Declarative Re-Creation](#61-declarative-re-creation)
- [7. Next Steps](#7-next-steps)
- [8. Related Documents](#8-related-documents)

---

## Quick Reference

For experienced users. Each composite recipe folds several lower-level ones — see [§8](#8-related-documents) for what each one does internally. Comments mark the points that genuinely need a human: a shell login/logout, or a review decision.

```bash
# 1. Verify tools, then create and enter the cluster shell
just check-tools --skip-asdf yes
just create-cluster-env --env <env> --cluster <cluster-name> --project <project-id>
just cluster-env --env <env> --cluster <cluster-name>
```

**Everything below runs inside that shell** — `CLUSTER_NAME`/`PROJECT_ID` are set:

```bash
# 2. Install pinned tools and verify, generate the node SSH keypair
just prepare-tools
just gcp-cluster generate-ssh-key

# 3. Bootstrap both identities (sa-manager + kops-cluster-creator), then rotate
just gcp-cluster bootstrap-identities
just gcp-cluster rotate-to-creator
# >>> shell boundary: re-enter so kops/kubectl pick up the new credential
exit
just cluster-env --env <env> --cluster <cluster-name>

# 4. Find your API-access IP, then bootstrap the local Git bundle (zero cloud calls)
just gcp-cluster show-public-ip
just gcp-cluster bootstrap-bundle --api-access-cidr "<ip>/32"

# >>> human boundary: review the generated YAML before it touches the cloud
$EDITOR config/kops/${CLUSTER_NAME}/cluster.yaml
$EDITOR config/kops/${CLUSTER_NAME}/instancegroups.yaml
git add config/kops/${CLUSTER_NAME}/ && git commit -m "${CLUSTER_NAME}: initial cluster manifest bundle"

# 5. Verify pre-flight checks, then create cluster
just gcp-cluster preflight-create
just gcp-cluster create-cluster

# 6. Explore
just gcp-cluster export-kubeconfig
just gcp-cluster k9s
```

Then continue with [`pulumi-setup.md`](pulumi-setup.md).

---

## 1. Prerequisites & Execution Context

You need a GCP project with billing enabled and the core system tools installed. One GCP project hosts exactly one cluster.
Stay inside the `cluster-env` sub-shell from here to the end of the guide.
→ [Prerequisites](reference/kops/prerequisites.md) · [Cluster env](reference/kops/cluster-env.md) · [Tool versions](reference/kops/tool-versions.md) · [File isolation](reference/kops/file-isolation.md)

```bash
just check-tools --skip-asdf yes

just create-cluster-env --env <env> --cluster <cluster-name> --project <project-id>
just cluster-env --env <env> --cluster <cluster-name>

just prepare-tools
just gcp-cluster generate-ssh-key
```

---

## 2. Service Accounts & Authentication

Start with the broad `sa-manager` identity, use it to create the least-privilege `kops-cluster-creator`, then rotate to that narrower key. Two composite recipes do the whole chain; one shell re-entry at the end is still required before `show-public-ip`.
→ [Service accounts](reference/kops/service-accounts.md)

```bash
# Bootstrap both identities: sa-manager + gcloud config + kops-cluster-creator
just gcp-cluster bootstrap-identities

# Rotate credential + gcloud identity to the least-privilege kops-cluster-creator
just gcp-cluster rotate-to-creator

# >>> shell boundary: re-enter so kops/kubectl pick up the new credential
exit
just cluster-env --env <env> --cluster <cluster-name>
```

---

## 3. Cluster Bootstrap (Git-Native Flow)

`bootstrap-bundle` renders the manifest bundle locally — zero cloud calls — and is the handoff point after which **Git owns cluster identity and shape**.
**Not idempotent.** Every change after generation is made directly in the YAML, reviewed, and committed before `create-cluster` touches the cloud.
→ [Bootstrap detail](reference/kops/bootstrap.md)

```bash
# Step 3.1 — find your API-access IP, then render the local bundle
just gcp-cluster show-public-ip
just gcp-cluster bootstrap-bundle --api-access-cidr "<your-ip>/32"

# Step 3.2 — review the generated YAML, then commit it
$EDITOR config/kops/${CLUSTER_NAME}/cluster.yaml
$EDITOR config/kops/${CLUSTER_NAME}/instancegroups.yaml
git add config/kops/${CLUSTER_NAME}/
git commit -m "${CLUSTER_NAME}: initial cluster manifest bundle"

# Step 3.3 — verify pre-flight checks before touching the cloud
just gcp-cluster preflight-create

# Steps 3.4–3.8 — bucket, push manifests, SSH secret, apply, validate
just gcp-cluster create-cluster
```

---

## 4. Day-2 Operations (Git-First Workflow)

Every configuration and scaling change is edit YAML → commit → apply → verify no drift.
→ [Day-2 operations](reference/kops/day2-operations.md) · [Change trigger matrix](reference/kops/day2-operations.md#change-trigger-matrix) · [CI drift detection](reference/kops/day2-operations.md#drift-detection-ci) · [Break-glass edits](reference/kops/day2-operations.md#break-glass-emergency-edits)

```bash
$EDITOR config/kops/${CLUSTER_NAME}/instancegroups.yaml
git commit -am "${CLUSTER_NAME}: scale stateless-web pool to 8"

just gcp-cluster apply-cluster

# Replace VMs after a machineType, disk, image, or kubernetesVersion change
just gcp-cluster rolling-update            # dry-run inspection
just gcp-cluster rolling-update --yes yes  # execute
```

---

## 5. Exploring the Cluster

Export the admin kubeconfig to `$KUBECONFIG`, then open the terminal UI. Re-export whenever the admin credential expires.
→ [Cluster access](reference/kops/cluster-access.md)

```bash
just gcp-cluster export-kubeconfig
just gcp-cluster k9s
```

---

## 6. Disposable Cluster Lifecycle

**Destructive. Deletes every VM, boot disk, and etcd volume in the cluster.**
VMs are transient; the blueprint in Git plus local cluster configuration are durable. Teardown destroys compute only — the state bucket, SSH keypair, SA keys, env file, and per-cluster tool manifest all survive.
→ [Teardown](reference/kops/teardown.md) · [What survives](reference/kops/teardown.md#what-survives-teardown)

```bash
# Destroy the cluster (omit --confirm for a dry-run preview)
just gcp-cluster delete-cluster --confirm yes

# Optional: also delete the state bucket and every object version
just gcp-cluster delete-state-bucket --confirm yes

# Optional: clean up machine-local gcloud configurations
just gcp-cluster cleanup-gcloud-config --confirm yes
```

### 6.1 Declarative Re-Creation

Rebuild from the Git bundle with the same `create-cluster` recipe used at [initial bootstrap](#3-cluster-bootstrap-git-native-flow) — the preconditions are identical (no live cluster, SSH secret absent). Only step 0 differs: you must re-enter the operator shell first.
→ [Recreation detail](reference/kops/recreation.md)

```bash
# 0. Re-enter the operator env (recreate the file first if it is gone)
just cluster-env --env <env> --cluster <cluster-name>

# 1–5. Bucket, manifests, SSH secret, apply, validate — one recipe
just gcp-cluster create-cluster
```

---

## 7. Next Steps

| Document | Use it for |
|----------|------------|
| [`pulumi-setup.md`](pulumi-setup.md) | Pulumi backend, KMS, state bucket, StorageClass |
| [`arangodb-deploy.md`](arangodb-deploy.md) | Production ArangoDB Cluster on `stateful-db` |
| [`kops-gcp-architecture.md`](kops-gcp-architecture.md) | HA vs cost, node capacity, database storage |

---

## 8. Related Documents

**Reference details for this guide** — including exactly which low-level recipes each composite command folds together:
- [Prerequisites & execution context](reference/kops/prerequisites.md)
- [The cluster env file](reference/kops/cluster-env.md)
- [asdf-managed tool versions](reference/kops/tool-versions.md)
- [Per-project file isolation & SSH](reference/kops/file-isolation.md)
- [Service accounts & authentication](reference/kops/service-accounts.md)
- [Cluster bootstrap detail](reference/kops/bootstrap.md)
- [Day-2 operations detail](reference/kops/day2-operations.md)
- [Cluster access — kubeconfig, k9s, status](reference/kops/cluster-access.md)
- [Teardown & disposable lifecycle](reference/kops/teardown.md)
- [Declarative re-creation](reference/kops/recreation.md)

**Other documentation:**
- Repo overview: [`README.md`](../README.md)
- Architecture deep dive: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Production ArangoDB: [`arangodb-deploy.md`](arangodb-deploy.md)
- ArangoDB implementation plan: [`plans/arangodb-production.md`](plans/arangodb-production.md)
