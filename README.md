# Kops Cluster Creation Guide

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Prerequisites & Execution Context](#1-prerequisites--execution-context)
  - [1.1 The GCP Project](#11-the-gcp-project)
  - [1.2 Core System Tools](#12-core-system-tools)
  - [1.3 Environment Variables — The Cluster Env File](#13-environment-variables--the-cluster-env-file)
    - [1.3.1 Create and activate](#131-create-and-activate)
    - [1.3.2 Variables overview](#132-variables-overview)
    - [1.3.3 After bootstrap — Git takes identity](#133-after-bootstrap--git-takes-identity)
  - [1.4 asdf-Managed Tools](#14-asdf-managed-tools)
    - [1.4.1 Tool version files](#141-tool-version-files)
  - [1.5 Per-Project File Isolation](#15-per-project-file-isolation)
  - [1.6 SSH Keypair](#16-ssh-keypair)
- [2. Service Accounts & Authentication](#2-service-accounts--authentication)
  - [Phase 1a — Enable APIs](#phase-1a)
  - [Phase 1b — Disable unused APIs](#phase-1b)
  - [Phase 2 — Create kops-cluster-creator SA](#phase-2)
- [3. Cluster Bootstrap (Git-Native Flow)](#3-cluster-bootstrap-git-native-flow)
  - [Step 3.1 — Bootstrap local manifest bundle](#step-31--bootstrap-local-manifest-bundle)
  - [Step 3.2 — Customize YAML in Git](#step-32--customize-yaml-in-git)
  - [Step 3.3 — Create state bucket](#step-33--create-state-bucket)
  - [Step 3.4 — Push manifest bundle to state store](#step-34--push-manifest-bundle-to-state-store)
  - [Step 3.5 — Upload SSH secret](#step-35--upload-ssh-secret)
  - [Step 3.6 — Preview & Apply (version-aware)](#step-36--preview--apply-version-aware)
  - [Step 3.7 — Validate HA topology & Hardening](#step-37--validate-ha-topology--hardening)
- [4. Day-2 Operations (Git-First Workflow)](#4-day-2-operations-git-first-workflow)
  - [4.1 Drift Detection (CI)](#41-drift-detection-ci)
  - [4.2 When to Run a Rolling Update](#42-when-to-run-a-rolling-update)
- [5. Exploring the Cluster](#5-exploring-the-cluster)
- [6. Disposable Cluster Lifecycle](#6-disposable-cluster-lifecycle)
  - [6.1 Mindset & Durable Blueprint](#61-mindset--durable-blueprint)
  - [6.2 What Survives Teardown](#62-what-survives-teardown)
  - [6.3 Teardown — Destroy the Cluster](#63-teardown--destroy-the-cluster)
  - [6.4 Optional: Delete the State Bucket](#64-optional-delete-the-state-bucket)
  - [6.5 Declarative Re-Creation](#65-declarative-re-creation)
  - [6.6 Summary](#66-summary)
  - [6.7 Caveats](#67-caveats)
- [7. Next Steps](#7-next-steps)

**Status**:
- **Tooling & Infrastructure recipes**: Tested 2026-08-20.
- **Declarative Git bundle (`_starter`) schema**: Verified cross-version compatible on kops v1.29.2 (dev) and v1.36.1 (prod) — both `cluster.yaml` and `instancegroups.yaml` pass `kops replace` validation on both binaries. Full end-to-end re-creation still pending first live-target run.

> **Document type:** This is a **provisioning guide** — a sequential
> walkthrough for setting up a kops-managed Kubernetes cluster from scratch using
> declarative Git-first manifests.

## Overview

This guide walks you through bootstrapping a kops-managed Kubernetes cluster on GCP from a fresh clone of the repo, all the way to a running cluster with a Pulumi-managed application stack on top.

- **Who it's for**: Platform operators who have access to a GCP project and the `sa-manager` service account key.
- **What you'll end up with**: A fully provisioned Kubernetes cluster (GCE instances, networking, IAM, state storage) plus the Pulumi scaffolding for deploying applications.
- **How long it takes**: ~30–45 minutes for a clean run (most of that is waiting for GCE instances to boot and pass health checks).
- **Two stores, one handoff**: Until the first YAML exists, the operator env file (`.env.<env>.<cluster>`) holds `PROJECT_ID` plus credentials and local paths. From [Section 3](#3-cluster-bootstrap-git-native-flow) onward, cluster identity and shape live **only** in Git under `config/kops/<cluster>/`. Do not copy kops name, state store, bucket name, or Kubernetes version back into the env file.
- **Pulumi post-provisioning**: Once the cluster is up, deploying the application stack is covered separately in [`docs/pulumi-setup.md`](docs/pulumi-setup.md).

## Quick Setup

1. [Install the system tools.](#12-core-system-tools) Install `go`, `just`, `gcloud`, `jq`, and `envsubst` through your system package manager.
2. [Create the cluster env file.](#131-create-and-activate) Run `just create-cluster-env --env <env> --cluster <name> --project <project-id>`, then `just cluster-env --env <env> --cluster <name>`.
3. [Install asdf tools.](#14-asdf-managed-tools) Run `just install-tools` (optionally via a per-cluster `.tool-versions.<env>.<cluster>` file).
4. [Generate the SSH keypair.](#16-ssh-keypair) Run `just gcp-cluster generate-ssh-key`, then persist `SSH_KEY` in the env file.
5. [Obtain sa-manager and authenticate.](#2-service-accounts--authentication) Create or receive `sa-manager.json`, run `just cluster-cred`, then configure the named gcloud configuration.
6. [Enable APIs and create the creator SA.](#phase-1a) Run Phase 1a and Phase 2, then rotate with `just cluster-cred`.
7. [Bootstrap the local bundle.](#step-31--bootstrap-local-manifest-bundle) Run `just gcp-cluster bootstrap-bundle --cluster <name> --project <id> --api-access-cidr <cidr>`. After this step Git owns identity.
8. [Review and commit YAML.](#step-32--customize-yaml-in-git) Customize `config/kops/<cluster>/cluster.yaml` and `instancegroups.yaml`, then commit to Git.
9. [Provision cluster.](#step-33--create-state-bucket) Create state bucket, push bundle, upload SSH key, preview, and apply.
10. [Validate & explore.](#step-37--validate-ha-topology--hardening) Run `just gcp-cluster validate-kops-ha --cluster <name>`, then `just gcp-cluster k9s`.

<a id="1-prerequisites--execution-context"></a>

## 1. Prerequisites & Execution Context

### 1.1 The GCP Project

You'll need an existing **GCP project with billing enabled**. An org admin creates it in the GCP console. Make a note of the `PROJECT_ID` — you put it in the env file in [Section 1.3](#13-environment-variables--the-cluster-env-file) so later recipes can default `--project` from the activated shell.

### 1.2 Core System Tools

Install these through your system package manager:

| Tool | Why You Need It | Check Command | Install |
|------|-----------------|--------------|---------|
| **Go** (≥1.26) | Builds the `cluster-ops` binary (`cmd/cluster-ops/main.go`) | `go version` | [go.dev/dl](https://go.dev/dl/) |
| **just** | The task runner — all operations are `just` commands | `just --version` | [github.com/casey/just](https://github.com/casey/just#installation) |
| **gcloud** | Google Cloud CLI for authentication and GCP API interactions | `gcloud version` | [cloud.google.com/sdk](https://cloud.google.com/sdk/docs/install) |
| **jq** | JSON processor used behind the scenes | `jq --version` | [jqlang.github.io/jq](https://jqlang.github.io/jq/download/) |
| **envsubst** | Required by `just gcp-cluster bootstrap-bundle` to render `config/kops/_starter/*.tmpl` | `envsubst --version` | `brew install gettext` / `apt install gettext-base` |

<a id="13-environment-variables--the-cluster-env-file"></a>

### 1.3 Environment Variables — The Cluster Env File

Until Git YAML exists, the operator environment lives in one **per-cluster env file**: `.env.<env>.<cluster>` (example: `.env.dev.my-cluster`). The file is gitignored. It is just `VAR=value` lines — no `export` prefix. `just cluster-env` auto-exports them via `set -a`.

Use this file for what you need **before** [Section 3](#3-cluster-bootstrap-git-native-flow): `PROJECT_ID`, credential paths, kubeconfig path, SSH public key, optional asdf pin file, later Pulumi paths. Do **not** put kops DNS name, state store, bucket name, Kubernetes version, topology, or instance-group sizing here. Those become Git fields at bootstrap.

Do **not** put cluster credentials in `.envrc`. `.envrc` is not the per-cluster credential store.

<a id="131-create-and-activate"></a>

#### 1.3.1 Create and activate

Create the env file as soon as you know the environment name, short cluster name, and GCP project. The `sa-manager` JSON is **not** required yet — you obtain or create it in [Section 2](#2-service-accounts--authentication).

```bash
just create-cluster-env \
  --env <env> \
  --cluster <cluster-name> \
  --project <project-id>
```

`--cluster` is only the env **filename** suffix. It is not written as `KOPS_CLUSTER_NAME`. `--project` writes `PROJECT_ID` so later `--project` flags can default from the activated shell. Optional `--credentials` / `--ssh-key` only if those files already exist.

Activate:

```bash
just cluster-env --env <env> --cluster <cluster-name>
```

The sub-shell reads the file once at start. After you edit the file — or after `just cluster-cred` — `exit` and run `just cluster-env` again.

<a id="132-variables-overview"></a>

#### 1.3.2 Variables overview

**Env-owned (set these as you go; they never move to Git):**

| Variable | Set up in | Purpose |
|----------|-----------|---------|
| `PROJECT_ID` | this section — needed before bootstrap | GCP project for SA, SSH, and `bootstrap-bundle --project` |
| `GOOGLE_APPLICATION_CREDENTIALS` | [Section 2](#2-service-accounts--authentication) | Active GCP service-account JSON — added after the key exists |
| `SA_MANAGER_KEY` | [Section 2](#2-service-accounts--authentication) | Path to `sa-manager.json` (optional; same file as GAC at first) |
| `KUBECONFIG` | [Section 1.5](#15-per-project-file-isolation) | Exported kubeconfig path |
| `SSH_KEY` | [Section 1.6](#16-ssh-keypair) | Node SSH **public** key |
| `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` | [Section 1.4.1](#141-tool-version-files) | Optional per-cluster asdf pin file |
| `PULUMI_GCP_CREDENTIALS` | [`docs/pulumi-setup.md`](docs/pulumi-setup.md) | Path to `pulumi-manager.json` |
| `PULUMI_SECRET_PROVIDER` | [`docs/pulumi-setup.md`](docs/pulumi-setup.md) | `gcpkms://…` URI |
| `PULUMI_BACKEND_URL` | [`docs/pulumi-setup.md`](docs/pulumi-setup.md) | Pulumi state bucket |

**Git-owned after bootstrap (do not write these into the env file):**

| Field | Where it lives after Step 3.1 |
|-------|-------------------------------|
| Short cluster name | directory `config/kops/<cluster>/` |
| Full kops DNS name | `cluster.yaml` → `metadata.name` |
| State store | `cluster.yaml` → `spec.configBase` |
| GCP project | `cluster.yaml` → `spec.project` |
| Kubernetes version | `cluster.yaml` → `spec.kubernetesVersion` |
| Topology, CIDRs, addons, instance groups | `cluster.yaml` / `instancegroups.yaml` |

Day-2 recipes still accept `--cluster` / `--project` / `--state` on the command line. After bootstrap, pass `--cluster` (the Git directory name). Do not keep a second copy of name/state/version in `.env.<env>.<cluster>`.

<a id="133-after-bootstrap--git-takes-identity"></a>

#### 1.3.3 After bootstrap — Git takes identity

`just gcp-cluster bootstrap-bundle` is the handoff. It writes `metadata.name`, `spec.project`, and `spec.configBase` into `config/kops/<cluster>/cluster.yaml`. From that point:

- Edit identity and shape in YAML, then commit.
- Leave the env file as credentials + local paths + `PROJECT_ID` (still useful as a `--project` default).
- If `PROJECT_ID` and `spec.project` ever disagree, **Git wins**. Fix the env file or pass `--project` explicitly.

<a id="14-asdf-managed-tools"></a>

### 1.4 asdf-Managed Tools

We use `asdf` to pin exact versions of these tools:

| Tool | Purpose | Docs |
|------|---------|------|
| `kubectl` | Talk to the Kubernetes API | [kubernetes.io](https://kubernetes.io/docs/reference/kubectl/kubectl/) |
| `kops` | Provision and manage the cluster | [kops.sigs.k8s.io](https://kops.sigs.k8s.io/) |
| `pulumi` | Deploy the application stack | [pulumi.com](https://www.pulumi.com/docs/) |
| `velero` | Backup and restore | [velero.io](https://velero.io/docs/) |
| `helm` | Kubernetes package manager | [helm.sh](https://helm.sh/docs/) |
| `k9s` | Terminal cluster UI | [k9scli.io](https://k9scli.io/) |

Install all pinned tools in `.tool-versions`:
```bash
just install-tools
```

<a id="141-tool-version-files"></a>

#### 1.4.1 Tool version files

`just install-tools` reads the asdf manifest named by `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME`:

- **Unset** → `.tool-versions` at the repo root (dev default).
- **Set** → a per-cluster file, e.g. `.tool-versions.<env>.<cluster>`.

Both files are gitignored. This pin file is **not** `spec.kubernetesVersion`. Keep cluster Kubernetes version in Git after bootstrap. Use a per-cluster pin file only when that cluster needs a different kops/kubectl binary than the repo default.

```bash
cp .tool-versions .tool-versions.<env>.<cluster>
# add to .env.<env>.<cluster>:
# ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=.tool-versions.<env>.<cluster>
exit
just cluster-env --env <env> --cluster <cluster>
just install-tools
```

<a id="15-per-project-file-isolation"></a>

### 1.5 Per-Project File Isolation

One GCP project hosts exactly one cluster. Scope cluster-specific files to the project ID: SSH keys and SA JSON under `credentials/${PROJECT_ID}/`, kubeconfig under `clusters/${PROJECT_ID}/`. Paths are relative to the repo root. `create-cluster-env --project <id>` defaults `KUBECONFIG` to `clusters/<project-id>/kubeconfig`.

```bash
SSH_KEY="${PWD}/credentials/${PROJECT_ID}/k8sVM.pub"
KUBECONFIG="${PWD}/clusters/${PROJECT_ID}/kubeconfig"
```

`just gcp-cluster export-kubeconfig` writes `$KUBECONFIG` when that variable is set.

<a id="16-ssh-keypair"></a>

### 1.6 SSH Keypair

Generate a dedicated Ed25519 keypair (RSA-4096 via `--type rsa`). The recipe refuses to overwrite an existing key:

```bash
just gcp-cluster generate-ssh-key --project <project-id>
```

Creates:
- Private: `credentials/<project-id>/k8sVM`
- Public: `credentials/<project-id>/k8sVM.pub`

If `SSH_KEY` is already in the activated env, that path wins. Otherwise `--project` (or `PROJECT_ID`) builds `credentials/${PROJECT_ID}/k8sVM`. Persist the public path in the env file (`--ssh-key` on `create-cluster-env`, or edit the file and re-enter `just cluster-env`).

<a id="2-service-accounts--authentication"></a>

## 2. Service Accounts & Authentication

You need the **Service Account Manager** (`sa-manager`) key — broad permissions so the rest of bootstrap can run least-privilege. Work inside the env shell from [Section 1.3.1](#131-create-and-activate) so `${PROJECT_ID}` is set.

**If you are the project owner**, create the SA and download its key:

```bash
just gcp-sa setup-sa-manager --project-id ${PROJECT_ID}
```

**Otherwise**, ask the owner for the JSON key and save it as `credentials/${PROJECT_ID}/sa-manager.json`.

Then write the path into the env file and re-enter the shell:

```bash
just cluster-cred --env <env> --cluster <cluster-name> \
  --key credentials/${PROJECT_ID}/sa-manager.json
exit
just cluster-env --env <env> --cluster <cluster-name>
```

Optional extra line in the same file (not written by `cluster-cred`):

```bash
SA_MANAGER_KEY="${PWD}/credentials/${PROJECT_ID}/sa-manager.json"
```

Configure a named gcloud configuration. `GOOGLE_APPLICATION_CREDENTIALS` authenticates recipes and libraries; the named configuration authenticates direct `gcloud` commands:

```bash
gcloud config configurations create sa-manager
gcloud auth activate-service-account \
  sa-manager@${PROJECT_ID}.iam.gserviceaccount.com \
  --key-file="${GOOGLE_APPLICATION_CREDENTIALS}"
gcloud config set project ${PROJECT_ID}
gcloud config set compute/zone us-central1-c
gcloud config configurations activate sa-manager
```

<a id="phase-1a"></a>

### Phase 1a — Enable APIs
```bash
just gcp-api enable-apis --project ${PROJECT_ID} \
  --api-file gcs-files/apis/enabled_apis.txt
```

<a id="phase-1b"></a>

### Phase 1b — Disable unused APIs
```bash
just gcp-api disable-apis --project ${PROJECT_ID} \
  --api-file gcs-files/apis/disable_enabled_apis.txt
```

<a id="phase-2"></a>

### Phase 2 — Create kops-cluster-creator SA
```bash
just gcp-sa create-sa --project ${PROJECT_ID} --sa-name kops-cluster-creator \
  --roles-file gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  --output-file credentials/${PROJECT_ID}/kops-cluster-creator.json
```

Rotate `GOOGLE_APPLICATION_CREDENTIALS` in the env file, then re-enter the shell:
```bash
just cluster-cred --env <env> --cluster <cluster-name> \
  --key credentials/${PROJECT_ID}/kops-cluster-creator.json
exit
just cluster-env --env <env> --cluster <cluster-name>
```

From here the narrower key is used. Do not add `KOPS_CLUSTER_NAME`, `KOPS_STATE_STORE`, `BUCKET_NAME`, or `KUBERNETES_VERSION` to the env file — Section 3 writes those into Git.

---

<a id="3-cluster-bootstrap-git-native-flow"></a>

## 3. Cluster Bootstrap (Git-Native Flow)

This is the handoff. Pass `--cluster` and `--project` (the latter may default from `PROJECT_ID` in the activated env). The recipe writes identity into YAML. After this section, Git is the source of truth for name, state store, project, and shape.

<a id="step-31--bootstrap-local-manifest-bundle"></a>

### Step 3.1 — Bootstrap local manifest bundle

Generate the local canonical manifest bundle from the repository starter template (`config/kops/_starter/`):

```bash
just gcp-cluster bootstrap-bundle \
  --cluster <cluster-name> \
  --project <project-id> \
  --api-access-cidr "<your-ip>/32"
```

**What this does:**
- Renders `config/kops/<cluster-name>/cluster.yaml` and `config/kops/<cluster-name>/instancegroups.yaml`.
- Pre-configures repository defaults (CSI driver, pd-ssd etcd volumes, cluster autoscaler, cert-manager, node-local DNS, and 3 worker pools: `stateless-web`, `stateful-db`, `batch-spot`).
- **Pure local operation:** Zero cloud API calls, zero state store mutations.

<a id="step-32--customize-yaml-in-git"></a>

### Step 3.2 — Customize YAML in Git

Review and tune the generated YAML files in your editor:

```bash
$EDITOR config/kops/<cluster-name>/cluster.yaml
$EDITOR config/kops/<cluster-name>/instancegroups.yaml
```

**Cluster defaults in `cluster.yaml`:**
- `spec.kubernetesVersion`: `1.28.8`
- `spec.api.access`: `["<your-ip>/32"]`
- `spec.networking.cilium`: enabled
- `spec.etcdClusters[*].volumeType`: `pd-ssd`
- `spec.cloudProvider.gce.pdCSIDriver`: `enabled: true`
- Hardening addons: `clusterAutoscaler`, `nodeProblemDetector`, `certManager`, `nodeLocalDNS` enabled

**InstanceGroup pools in `instancegroups.yaml`:**
- `master-us-central1-c`: Control plane (machine type `n1-custom-4-8192`, size 1, 75GB disk)
- `nodes`: Default worker pool (machine type `n1-custom-2-4096`, size 4, 100GB disk)
- `stateless-web`: Web/API pool (machine type `e2-standard-4`, min 3 / max 6, 100GB disk)
- `stateful-db`: Database pool (machine type `n2-standard-4`, min 3 / max 3, 100GB disk)
- `batch-spot`: Spot compute pool (machine type `e2-standard-4`, min 0 / max 4, preemptible)

Commit the bundle to Git:
```bash
git add config/kops/<cluster-name>/
git commit -m "<cluster-name>: initial cluster manifest bundle"
```

<a id="step-33--create-state-bucket"></a>

### Step 3.3 — Create state bucket

Create the hardened GCS state bucket (enables versioning and uniform access):

```bash
just gcp-cluster create-state-bucket \
  --project <project-id> \
  --bucket-name kops-state-<cluster-name>
```

<a id="step-34--push-manifest-bundle-to-state-store"></a>

### Step 3.4 — Push manifest bundle to state store

Push the committed Git YAML into the state storage:

```bash
just gcp-cluster replace-manifests --cluster <cluster-name> --force yes
```

<a id="step-35--upload-ssh-secret"></a>

### Step 3.5 — Upload SSH secret

Upload the public SSH key into kops state:

```bash
just gcp-cluster upload-ssh-secret \
  --cluster <cluster-name> \
  --ssh-key credentials/<project-id>/k8sVM.pub
```

<a id="step-36--preview--apply-version-aware"></a>

### Step 3.6 — Preview & Apply (version-aware)

Preview the infrastructure changes (dry-run), then apply:

```bash
just gcp-cluster plan-cluster --cluster <cluster-name>   # preview cloud plan
just gcp-cluster update-cluster --cluster <cluster-name> # apply to GCP
```

| Action | kOps ≤ 1.30.x (repo pin: v1.29.2) | kOps ≥ 1.31.0 |
|--------|-----------------------------------|---------------|
| Preview (dry-run) | `kops update cluster --admin` | `kops reconcile cluster` |
| Apply | `kops update cluster --yes --admin` | `kops reconcile cluster --yes` |
| Rolling update | separate `kops rolling-update` if needed | included in `reconcile` |
| Validate | `kops validate cluster` | same |

<a id="step-37--validate-ha-topology--hardening"></a>

### Step 3.7 — Validate HA topology & Hardening

```bash
# Validate kops cluster health
just gcp-cluster validate-cluster

# Validate HA topology
just gcp-cluster validate-kops-ha

# Validate hardening components (Autoscaler, NPD, Cert-Manager, NodeLocalDNS, Metrics Server)
just gcp-cluster validate-hardening
```

---

<a id="4-day-2-operations-git-first-workflow"></a>

## 4. Day-2 Operations (Git-First Workflow)

After bootstrap, **all configuration and scaling changes follow the declarative Git-first workflow**:

```bash
# 1. Edit the canonical local YAML
$EDITOR config/kops/<cluster-name>/cluster.yaml        # cluster settings, security, CIDRs
# or: $EDITOR config/kops/<cluster-name>/instancegroups.yaml # pool sizing, machine types

# 2. Commit the change to Git
git commit -am "<cluster-name>: scale stateless-web pool to 8"

# 3. Push to kops state store
just gcp-cluster replace-manifests --cluster <cluster-name>

# 4. Preview pending cloud changes (dry-run review)
just gcp-cluster plan-cluster --cluster <cluster-name>

# 5. Apply changes to GCP
just gcp-cluster update-cluster --cluster <cluster-name>

# 6. Verify zero drift between Git and state store
just gcp-cluster drift-manifests --cluster <cluster-name>

# 7. If VM sizes or boot disks changed, roll the instances:
just gcp-cluster rolling-update \
  --cluster <cluster-name> \
  --instance-group stateless-web \
  --yes yes
```

> **Break-Glass Emergency Edits:**
> If an operator must run `kops edit` during a live outage, reconcile Git immediately after:
> ```bash
> just gcp-cluster export-bundle --cluster <cluster-name>
> git diff
> git commit -am "HOTFIX: reconcile emergency live edits"
> ```

<a id="41-drift-detection-ci"></a>

### 4.1 Drift Detection (CI)

Two checks to be wired into CI on PR + nightly cron:

1. **Manifest spec drift (blocking failure):** `just gcp-cluster drift-manifests` — fails with non-zero exit code if canonical `cluster.yaml` or `instancegroups.yaml` differs from live state storage.
2. **State bucket vs cloud plan (review output):** `just gcp-cluster plan-cluster` — runs version-aware preview `kops update/reconcile` without `--yes` to generate the plan for PR comments.

```yaml
# Example CI step:
- name: Check Manifest Drift
  run: just gcp-cluster drift-manifests

- name: Preview Cloud Plan
  run: just gcp-cluster plan-cluster
```

| Check | Role / Catches |
|---|---|
| `just gcp-cluster drift-manifests` | Fails CI if someone ran `kops edit` without backfilling |
| `just gcp-cluster plan-cluster` | Previews pending infrastructure changes in PR review |

CI runs with a read-only service account (`roles/compute.viewer` +
bucket read). Nightly failure on `drift-manifests` indicates an uncommitted live edit.
On PRs, `drift-manifests` verifies that base state is clean, while `plan-cluster`
renders the pending cloud diff into the PR comments for human review.

<a id="42-when-to-run-a-rolling-update"></a>

### 4.2 When to Run a Rolling Update

`update-cluster` updates the cloud infrastructure templates in GCP, but does not necessarily restart live running VMs. To know whether running VMs need replacement:

#### How to Check (Dry-Run Inspection)

Run `rolling-update` without `--yes` (it defaults to dry-run inspection):

```bash
just gcp-cluster rolling-update --cluster <cluster-name>
```

- If output reports `No cluster updates required.` $\rightarrow$ **Done.** No VMs need replacement.
- If output lists `NEEDUPDATE > 0` $\rightarrow$ Running VMs are on old templates. Execute with `--yes yes`:
  ```bash
  just gcp-cluster rolling-update --cluster <cluster-name> --yes yes
  ```

#### Change Trigger Matrix

| Change Made in YAML | Rolling Update Needed? | Why |
|---|:---:|---|
| **`machineType`** (CPU / RAM change) | **Yes** | Cannot resize running GCE VM on the fly |
| **`rootVolumeSize`** / disk type | **Yes** | Requires boot disk recreation |
| **`image`** (OS security upgrade) | **Yes** | New base OS image requires fresh VM |
| **`kubernetesVersion`** (k8s upgrade) | **Yes** | Node requires kubelet / containerd binary update |
| **`minSize` / `maxSize`** (pool scaling) | **No** | GCE MIG scales VM count up/down automatically |
| **`kubernetesApiAccess`** (API CIDRs) | **No** | GCP Load Balancer / firewall rule update only |
| **Addon configs** (Autoscaler, Cert-Manager) | **No** | In-cluster Kubernetes pod/DaemonSet update |

> **kOps Version Behavior:**
> - **kOps ≤ 1.30 (`dev` pin: v1.29.2):** `update-cluster` and `rolling-update` are separate commands. Running `rolling-update` after VM template changes is required.
> - **kOps ≥ 1.31 (`prod` pin: v1.36.1):** `update-cluster` (which runs `kops reconcile cluster --yes`) reconciles cloud resources and rolls nodes sequentially in one pass. Use `just gcp-cluster rolling-update` on ≥1.31 only for targeted pool rolls (`--instance-group <name>`) or forced restarts (`--force yes`).

---

<a id="5-exploring-the-cluster"></a>

## 5. Exploring the Cluster

Export kubeconfig and launch management UI. If `KUBECONFIG` is set (from `just cluster-env`), export writes that path:

```bash
just gcp-cluster export-kubeconfig --cluster <cluster-name>
just gcp-cluster k9s
```

---

<a id="6-disposable-cluster-lifecycle"></a>

## 6. Disposable Cluster Lifecycle

<a id="61-mindset--durable-blueprint"></a>

### 6.1 Mindset & Durable Blueprint

The blueprint — canonical manifest bundle (`config/kops/<cluster>/*.yaml`) — is durable in Git. SA keys and the per-cluster env file stay on the operator machine (gitignored). GCE compute instances are transient. Recreation is fully declarative and reproducible.

> **Note on single-file `instancegroups.yaml` replace:** Manifest schema (`v1alpha2`) is verified cross-version compatible — `kops replace -f` accepts both files on kops v1.29.2 and v1.36.1. Full end-to-end re-creation from Git still pending a first live-target run.

### 6.2 What Survives Teardown

| Artifact | Survives? | Why |
|----------|-----------|-----|
| Canonical manifest bundle (`config/kops/<cluster>/*.yaml`) | ✅ Yes | Checked into Git — single source of truth. |
| GCS state bucket (`gs://kops-state-<cluster>`) | ✅ Yes | `kops delete` does not delete the bucket. |
| SSH keypair (`credentials/<project>/k8sVM*`) | ✅ Yes | Local files reused across cycles. |
| Service account keys (`credentials/<project>/*.json`) | ✅ Yes | GCP service accounts and JSON keys persist. |
| Per-cluster env file (`.env.<env>.<cluster>`) | ✅ Yes | Gitignored local credentials/paths; recreate with `just create-cluster-env` if missing. |
| **GCE compute instances** (control-plane + workers) | ❌ Destroyed | These are transient VMs. |
| **Boot disks & etcd volumes** | ❌ Destroyed | Deleted with instances. |

<a id="63-teardown--destroy-the-cluster"></a>

### 6.3 Teardown — Destroy the Cluster

```bash
# Dry-run preview:
just gcp-cluster delete-cluster

# Full destroy:
just gcp-cluster delete-cluster --confirm yes
```

<a id="64-optional-delete-the-state-bucket"></a>

### 6.4 Optional: Delete the State Bucket

For complete deletion (bucket + all objects + all versions):
```bash
# Dry-run preview (lists objects, nothing deleted):
just gcp-cluster delete-state-bucket --cluster <cluster-name>

# Permanent deletion:
just gcp-cluster delete-state-bucket --cluster <cluster-name> --confirm yes
```

Bucket name defaults to `kops-state-<cluster-name>` derived from `--cluster`. Override with `--bucket-name <bucket|gs://bucket>` or the `BUCKET_NAME` env var.

<a id="65-declarative-re-creation"></a>

### 6.5 Declarative Re-Creation

To bring the cluster back after teardown:

0. **Re-enter the operator env** (credentials + kubeconfig path):
   ```bash
   just cluster-env --env <env> --cluster <cluster-name>
   ```
   Recreate the file first if it is gone:
   ```bash
   just create-cluster-env --env <env> --cluster <cluster-name> \
     --project <project-id> \
     --credentials credentials/<project-id>/kops-cluster-creator.json \
     --ssh-key credentials/<project-id>/k8sVM.pub
   ```

1. **Recreate state bucket** (only if deleted in step 6.4):
   ```bash
   just gcp-cluster create-state-bucket \
     --project <project-id> \
     --bucket-name kops-state-<cluster-name>
   ```

2. **Push canonical Git bundle to state store**:
   ```bash
   just gcp-cluster replace-manifests --cluster <cluster-name> --force yes
   ```

3. **Restore SSH secret** (mandatory after teardown):
   ```bash
   just gcp-cluster upload-ssh-secret \
     --cluster <cluster-name> \
     --ssh-key credentials/<project-id>/k8sVM.pub
   ```

4. **Preview and provision**:
   ```bash
   just gcp-cluster plan-cluster --cluster <cluster-name>
   just gcp-cluster update-cluster --cluster <cluster-name>
   ```

5. **Validate**:
   ```bash
   just gcp-cluster validate-cluster
   just gcp-cluster validate-kops-ha
   just gcp-cluster validate-hardening
   ```

### 6.6 Summary

- **Initial Setup:** Steps 1–3
- **Day-2 Changes:** Section 4 (Git-First Workflow)
- **Teardown:** `just gcp-cluster delete-cluster --confirm yes`
- **Recreate:** Section 6.5 (Declarative Re-Creation)

### 6.7 Caveats

- **kOps version pinning:** Pins live in `.tool-versions`. Upgrade kOps through dedicated review PRs.
- **GCP Quotas:** Frequent create/delete cycles can trigger API rate-limits.

---

<a id="7-next-steps"></a>

## 7. Next Steps

- **Deploy applications:** Follow [`docs/pulumi-setup.md`](docs/pulumi-setup.md).
- **Production ArangoDB:** Follow [`docs/arangodb-deploy.md`](docs/arangodb-deploy.md). Implementation plan: [`docs/plans/arangodb-production.md`](docs/plans/arangodb-production.md).
- **Architecture deep dive:** Read [`docs/kops-gcp-architecture.md`](docs/kops-gcp-architecture.md).
