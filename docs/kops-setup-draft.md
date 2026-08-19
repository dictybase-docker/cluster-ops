# Kops Cluster Creation Guide

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Prerequisites & Tooling](#1-prerequisites--tooling)
  - [1.1 The GCP Project](#11-the-gcp-project)
  - [1.2 Core System Tools](#12-core-system-tools)
  - [1.3 Environment Variables — The Cluster Config File](#13-environment-variables--the-cluster-config-file)
    - [1.3.1 Create Your Cluster Env File](#131-create-your-cluster-env-file)
    - [1.3.2 Variables Overview](#132-variables-overview)
    - [1.3.3 Activate Your Cluster Env File](#133-activate-your-cluster-env-file)
  - [1.4 asdf-Managed Tools](#14-asdf-managed-tools)
    - [1.4.1 Tool Version Files](#141-tool-version-files)
  - [1.5 Per-Project File Isolation](#15-per-project-file-isolation)
  - [1.6 SSH Keypair and Local Repo](#16-ssh-keypair-and-local-repo)
- [2. Authentication Setup](#2-authentication-setup)
- [3. Cluster Bootstrap](#3-cluster-bootstrap)
  - [Phase 1a — Enable APIs](#phase-1a)
  - [Phase 1b — Disable unused APIs](#phase-1b)
  - [Phase 2 — Create kops-cluster-creator SA](#phase-2)
  - [Phase 3 — Rotate credential](#phase-3)
  - [Phase 4 — HA cluster manifest](#phase-4)
  - [Phase 4a — Create state bucket](#phase-4a)
  - [Phase 4b — Generate cluster manifest](#phase-4b)
  - [Phase 4c — Edit manifest for security](#phase-4c)
  - [Phase 4d — Configure InstanceGroups](#phase-4d)
  - [Changing Shape After Generation](#changing-shape-after-generation)
  - [Phase 5 — Apply (version-aware)](#phase-5)
  - [Phase 6 — Validate HA topology](#phase-6)
  - [Phase 7 — Post-Provisioning Hardening](#phase-7)
- [4. Exploring the Cluster](#4-exploring-the-cluster)
- [5. Disposable Cluster Lifecycle](#5-disposable-cluster-lifecycle)
  - [5.1 Mindset](#51-mindset)
  - [5.2 What Survives Teardown](#52-what-survives-teardown)
  - [5.3 Teardown — Destroy the Cluster](#53-teardown--destroy-the-cluster)
  - [5.4 Optional: Delete the State Bucket](#54-optional-delete-the-state-bucket)
  - [5.5 Re-Creation — Bring It Back](#55-re-creation--bring-it-back)
  - [5.6 Summary](#56-summary)
  - [5.7 Caveats](#57-caveats)
- [6. Next Steps](#6-next-steps)

**Last tested**: 2026-08-18

> **Document type:** This is a **provisioning guide** — a sequential
> walkthrough for setting up a kops-managed Kubernetes cluster from scratch.
> For incident-response procedures (diagnostic decision trees, alert-linked
> runbooks), see the [kops architecture doc](kops-gcp-architecture.md) technical
> risks section, which maps failure modes to mitigations.

## Overview

This guide walks you through bootstrapping a kops-managed Kubernetes cluster on GCP from a fresh clone of the repo, all the way to a running cluster with a Pulumi-managed application stack on top.

- **Who it's for**: Platform operators who have access to a GCP project and the `sa-manager` service account key.
- **What you'll end up with**: A fully provisioned Kubernetes cluster (GCE instances, networking, IAM, state storage) plus the Pulumi scaffolding for deploying applications.
- **How long it takes**: ~30–45 minutes for a clean run (most of that is waiting for GCE instances to boot and pass health checks).
- **Prerequisites in one sentence**: An existing GCP project with billing enabled, `sa-manager.json` key, and the tools listed in Section 1.
- **Reusable across environments**: This guide is written to be generic — dev, staging, prod, whatever. Each environment gets its own GCP project (one cluster per project). Change the project ID, pick a cluster name, adjust a few optional knobs (node count, machine size), and the same steps produce a cluster in a different GCP project. The only per-environment artifact you create is a small env file (Section 1.3).
- **Pulumi post-provisioning**: Once the cluster is up, deploying the application stack is covered separately in [`docs/pulumi-setup.md`](pulumi-setup.md).

## Quick Setup

1. [Install the system tools.](#12-core-system-tools) Install `go`, `just`, `gcloud`, `direnv`, and `jq` through your system package manager.
2. [Create the cluster env file.](#13-environment-variables--the-cluster-config-file) Copy `.env.dev.dcr-experiments` to `.env.<env>.<cluster>` and set `PROJECT_ID`; the remaining variables are added at their later steps.
3. [Install the asdf-managed tools.](#14-asdf-managed-tools) Create the dev `.tool-versions` (or a per-cluster file — [§1.4.1](#141-tool-version-files)) and run `just install-tools`. All tool version files are gitignored.
4. [Set up per-project file isolation and SSH.](#15-per-project-file-isolation) Add `SSH_KEY` and `KUBECONFIG` paths under the project directory, then generate the Ed25519 keypair.
5. [Set up authentication.](#2-authentication-setup) Create or obtain `sa-manager.json`, add `GOOGLE_APPLICATION_CREDENTIALS` to the cluster env file, and configure the required named gcloud configuration.
6. [Bootstrap the cluster.](#3-cluster-bootstrap) Verify the earlier variables, set the bootstrap variables (`BUCKET_NAME`, `KOPS_CLUSTER_NAME`, `KOPS_STATE_STORE`, `KUBERNETES_VERSION`), exit the current shell, re-enter with `just cluster-env`, and run the phased HA workflow.
7. [Verify and explore.](#4-exploring-the-cluster) Run `just gcp-cluster validate-kops-ha`, then `just gcp-cluster k9s`.

<a id="1-prerequisites--tooling"></a>

## 1. Prerequisites & Tooling


### 1.1 The GCP Project

You'll need a **GCP project that already exists** with billing enabled. This isn't something the repo's tooling creates — an org admin needs to set it up in the GCP console beforehand. Make a note of the `PROJECT_ID`; you'll need it for nearly every command that follows.

### 1.2 Core System Tools

These aren't managed by `asdf` — install them through your system package manager:

| Tool | Why You Need It | Check Command | Install |
|------|-----------------|--------------|---------|
| **Go** (≥1.26) | Builds the `cluster-ops` binary (`cmd/cluster-ops/main.go`) that all Justfile recipes shell out to | `go version` | [go.dev/dl](https://go.dev/dl/) |
| **just** | The task runner — every recipe in this guide (install tools, create cluster, deploy) is a `just` command | `just --version` | [github.com/casey/just](https://github.com/casey/just#installation) |
| **gcloud** | Google Cloud CLI — used by several recipes behind the scenes and handy for manual debugging | `gcloud version` | [cloud.google.com/sdk](https://cloud.google.com/sdk/docs/install) |
| **direnv** | Auto-loads environment variables from `.envrc` when you enter the project directory — no manual `export` needed | `direnv version` | [direnv.net](https://direnv.net/docs/installation.html) |
| **jq** | JSON processor used by several Justfile recipes behind the scenes | `jq --version` | [jqlang.github.io/jq](https://jqlang.github.io/jq/download/) |

<a id="13-environment-variables--the-cluster-config-file"></a>

### 1.3 Environment Variables — The Cluster Config File

Everything the tooling knows about a cluster — its name, state location, tool versions, file paths, credentials, and shape — lives in one **per-cluster env file**: `.env.<env>.<cluster>` (e.g. `.env.dev.my-cluster`). Create the file, set `PROJECT_ID` ([Section 1.3.2](#132-variables-overview)), then add the remaining variables at the step each is needed. The file does nothing until you activate it with `just cluster-env --env <env> --cluster <cluster>` ([Section 1.3.3](#133-activate-your-cluster-env-file)).

#### 1.3.1 Create Your Cluster Env File

**Option A — copy the example and edit**:
```bash
cp .env.dev.dcr-experiments .env.<env>.<cluster>
# edit .env.<env>.<cluster> with your values
```
**Option B — create from scratch** using the [variables overview](#132-variables-overview) as your checklist. The file is just `VAR=value` lines — nothing fancy (no `export` prefix; `just cluster-env` auto-exports them via `set -a`).

#### 1.3.2 Variables Overview

The cluster env file grows as you work through the guide — do not fill it all at once. Each variable is set up at the step that needs it. This table lists every variable the file will eventually contain and where to set it up:

| Variable | Set up in |
|----------|-----------|
| `PROJECT_ID` | this section — needed from the start |
| `BUCKET_NAME` | [Section 3 — Cluster Bootstrap](#3-cluster-bootstrap) |
| `KOPS_CLUSTER_NAME` | [Section 3 — Cluster Bootstrap](#3-cluster-bootstrap) |
| `KOPS_STATE_STORE` | [Section 3 — Cluster Bootstrap](#3-cluster-bootstrap) |
| `KUBERNETES_VERSION` | [Section 3 — Cluster Bootstrap](#3-cluster-bootstrap) |
| `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` | [Section 1.4.1](#141-tool-version-files) |
| `KUBECONFIG` | [Section 1.5](#15-per-project-file-isolation) |
| `SSH_KEY` | [Section 1.6](#16-ssh-keypair-and-local-repo) |
| `SA_MANAGER_KEY` | [Section 2](#2-authentication-setup) |
| `GOOGLE_APPLICATION_CREDENTIALS` | [Section 2](#2-authentication-setup) |
| Phase 4b cluster-shape vars (`TOTAL_NODES`, `TOPOLOGY`, …) | [Phase 4b](#phase-4b) |
| Phase 4d InstanceGroup vars (`STATELESS_*`, `BATCH_*`, …) | [Phase 4d](#phase-4d) |

Set `PROJECT_ID` now — every later step references it:

```bash
PROJECT_ID="<project-id>"
```

> **Why `.k8s.local`?** `KOPS_CLUSTER_NAME` must end in `.k8s.local` so kops uses gossip-based DNS — cluster nodes discover each other directly instead of through a managed DNS zone. You set this value in [Section 3](#3-cluster-bootstrap).

#### 1.3.3 Activate Your Cluster Env File

`just cluster-env --env <env> --cluster <cluster>` sources the env file into a sub-shell — one command, no `.envrc` mutation:

```bash
just cluster-env --env <env> --cluster <cluster>
# Example: just cluster-env --env dev --cluster my-cluster
```
The sub-shell reads the file once when it starts, so all cluster variables are loaded for every `just` command inside it. To switch clusters — or after editing the file — `exit` the sub-shell and re-run `just cluster-env`.

### 1.4 asdf-Managed Tools

We use `asdf` to pin exact versions of these tools:

| Tool | Purpose | Docs |
|------|---------|------|
| `kubectl` | Talk to the Kubernetes API | [kubernetes.io](https://kubernetes.io/docs/reference/kubectl/kubectl/) |
| `kops` | Provision and manage the cluster | [kops.sigs.k8s.io](https://kops.sigs.k8s.io/) |
| `pulumi` | Deploy the application stack | [pulumi.com](https://www.pulumi.com/docs/) |
| `velero` | Backup and restore | [velero.io](https://velero.io/docs/) |
| `mc` | MinIO client | [min.io](https://min.io/docs/minio/linux/reference/minio-mc.html) |
| `krew` | kubectl plugin manager | [krew.sigs.k8s.io](https://krew.sigs.k8s.io/) |
| `helm` | Kubernetes package manager | [helm.sh](https://helm.sh/docs/) |
| `k9s` | Terminal cluster UI | [k9scli.io](https://k9scli.io/) |
| `k3d` | Local Kubernetes for testing | [k3d.io](https://k3d.io/) |

Versions are declared in a tool versions file — see [§1.4.1](#141-tool-version-files).

<a id="141-tool-version-files"></a>

#### 1.4.1 Tool Version Files

Versions live in a **tool versions file** — an asdf manifest of `<tool> <version>` lines. `just install-tools` reads it, adds every asdf plugin, and installs every pinned binary — the single source of truth for *what* gets installed. Both the root and per-cluster files are **gitignored**.

**Which file is read** is controlled by the `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` env var:

- **Unset** → `.tool-versions` at the repo root (the dev default).
- **Set** → the named file (a per-cluster file, e.g. `.tool-versions.<env>.<cluster>`), loaded via the cluster env file so `just cluster-env --env <env> --cluster <cluster>` selects it automatically.

**Dev default** — create the root file, then install:

```bash
cat > .tool-versions <<'EOF'
kubectl 1.28.8
kops  v1.29.2
pulumi 3.108.0
mc 2024-02-09T22-18-24Z
krew 0.4.5
velero 1.14.0
helm 3.15.2
k9s 0.50.18
k3d 5.8.3
EOF
just install-tools
```

Use `just install-tool --name <tool> --version <version>` to add or re-pin a single tool later — it edits only that tool's line in the active file.

**Per-cluster file** — use one when a cluster needs different kops/kubectl versions than the dev default:

> **Order matters:** create the env file ([Section 1.3.1](#131-create-your-cluster-env-file)) first.

1. **Create** the file as a complete copy of the root:
   ```bash
   cp .tool-versions .tool-versions.<env>.<cluster>
   ```
   Every tool must appear in it — a missing tool fails its shim with `No version is set`. (A `<project-id>`-scoped name also works — one cluster maps to one project, [Section 1.5](#15-per-project-file-isolation).)

2. **Set the env var** in the cluster env file:
   ```bash
   ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=".tool-versions.<env>.<cluster>"
   ```

3. **Activate, then install**:
   ```bash
   just cluster-env --env <env> --cluster <cluster>
   just install-tools
   ```

4. **Exit** the sub-shell — the dev cluster is unaffected (its env file omits the var, so root `.tool-versions` applies).

**Version pairing rules:**

- **kops ↔ Kubernetes:** match the kops minor to the cluster's Kubernetes minor (kops v1.29.x → 1.28/1.29; v1.31.x → 1.31+).
- **kubectl:** keep within one minor of the cluster server.
- **kops ≥ 1.31:** updates use `kops reconcile cluster` instead of `kops update cluster` — see [Phase 5](#phase-5). Do not mix the two workflows.

### 1.5 Per-Project File Isolation

One GCP project hosts exactly one cluster. Scope every cluster-specific file to its project ID: SSH keys and service-account JSON keys go in `credentials/${PROJECT_ID}/`, and the kubeconfig goes in `clusters/${PROJECT_ID}/`. These paths are relative to the **cluster-ops repo root** — the directory you run `just` from, where the env file lives. Anchor them with `${PWD}` (your shell's working directory at that point):

```bash
SSH_KEY="${PWD}/credentials/${PROJECT_ID}/k8sVM.pub"
KUBECONFIG="${PWD}/clusters/${PROJECT_ID}/kubeconfig"
```

The credential path is added with `GOOGLE_APPLICATION_CREDENTIALS` in [Section 2](#2-authentication-setup), after the service-account key exists. This isolation prevents one project's cluster files from replacing another project's files.

### 1.6 SSH Keypair and Local Repo

Preferred — generates an **Ed25519** keypair (RSA-4096 via `--type rsa`) and refuses to overwrite an existing key:

```bash
just gcp-cluster generate-ssh-key
# RSA-4096 fallback:
just gcp-cluster generate-ssh-key --type rsa
```

Key path precedence: the `SSH_KEY` env var if set (from [Section 1.5](#15-per-project-file-isolation)); otherwise `--project <project-id>` (defaults to `PROJECT_ID`) builds `credentials/${PROJECT_ID}/k8sVM`. Both key files are gitignored — a fresh clone always needs the keypair (or a secure copy from another operator).

## 2. Authentication Setup

You need the **Service Account Manager** (`sa-manager`) key — broad permissions (creating SAs, enabling APIs, managing IAM) so the rest of the process runs least-privilege.

Record both key paths in your cluster env file **first** — they drive the commands below, and the recipe writes the key where `SA_MANAGER_KEY` points:

```bash
SA_MANAGER_KEY="${PWD}/credentials/${PROJECT_ID}/sa-manager.json"
GOOGLE_APPLICATION_CREDENTIALS="${PWD}/credentials/${PROJECT_ID}/sa-manager.json"
```

- `SA_MANAGER_KEY` — where the key lives.
- `GOOGLE_APPLICATION_CREDENTIALS` — the active credential that `just cluster-env` loads.

Then activate the env — run every command below inside this sub-shell:

```bash
just cluster-env --env <env> --cluster <cluster>
```

**If you are the project owner**, create the SA and download its key (skips an existing SA; binds all 13 predefined manager roles — service-account, IAM, API, storage, and KMS administration):

```bash
just gcp-sa setup-sa-manager
```

Recipe options: `-p`/`--project-id <project-id>` — optional, defaults to `PROJECT_ID` from the activated env; `-k`/`--key-file <path>` — output path, used only when `SA_MANAGER_KEY` is unset (defaults to `credentials/${PROJECT_ID}/sa-manager.json`).

**Otherwise**, ask the project owner for the `sa-manager` JSON key and save it to `./credentials/${PROJECT_ID}/sa-manager.json`.

Next, set up the **gcloud named configuration**. App credentials and gcloud configuration are separate: `GOOGLE_APPLICATION_CREDENTIALS` authenticates recipes and libraries, while the named configuration authenticates direct `gcloud` commands. The activated env provides `PROJECT_ID` and `SA_MANAGER_KEY`:

```bash
gcloud config configurations create sa-manager
gcloud auth activate-service-account \
  sa-manager@${PROJECT_ID}.iam.gserviceaccount.com \
  --key-file="${SA_MANAGER_KEY}"
gcloud config set project ${PROJECT_ID}
gcloud config set compute/zone us-central1-c
gcloud config configurations activate sa-manager
```

The last command makes `sa-manager` the active gcloud configuration for bootstrap — an isolated slot; your personal login stays untouched in the `default` configuration. Switch back when done:

```bash
gcloud config configurations activate default   # personal login
# back to sa-manager:
gcloud config configurations activate sa-manager
```

Check who you are at any moment:

```bash
gcloud auth list                     # which account is active?
gcloud config configurations list    # all named identities
```

> **Add more SA identities later the same way** (e.g. a `kops-creator` configuration for the `kops-cluster-creator` key). Each gets its own named slot: `gcloud config configurations create <name>`, activate the SA with `gcloud auth activate-service-account`, switch with `gcloud config configurations activate <name>`.

> Phases 2–3 of the bootstrap ([Section 3](#3-cluster-bootstrap)) mint a narrower `kops-cluster-creator` key and point `GOOGLE_APPLICATION_CREDENTIALS` at it; `sa-manager` then goes dormant.

## 3. Cluster Bootstrap

Before starting, verify the variables you set in earlier sections and add the remaining bootstrap variables to `.env.<env>.<cluster>`. The cluster-shape variables (Phase 4b) and InstanceGroup variables (Phase 4d) are set in their own phases — not here.

**Verify these are already set** (from earlier sections):

| Variable | Set up in |
|----------|-----------|
| `PROJECT_ID` | [Section 1.3.2](#132-variables-overview) |
| `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` | [Section 1.4.1](#141-tool-version-files) |
| `KUBECONFIG` | [Section 1.5](#15-per-project-file-isolation) |
| `SSH_KEY` | [Section 1.6](#16-ssh-keypair-and-local-repo) |
| `SA_MANAGER_KEY` | [Section 2](#2-authentication-setup) |
| `GOOGLE_APPLICATION_CREDENTIALS` | [Section 2](#2-authentication-setup) |

`GOOGLE_APPLICATION_CREDENTIALS` and the active gcloud named configuration must both be ready. The former authenticates recipes and libraries; the latter authenticates direct `gcloud` commands.

**Set these now** — they identify the cluster and its kops state:

| Variable | What It Tells the Tooling | Example |
|----------|---------------------------|--------|
| `BUCKET_NAME` | Name of the GCS bucket where kops stores its state. Must be globally unique. | `kops-state-my-cluster` |
| `KOPS_CLUSTER_NAME` | The full DNS name of your cluster. **Must end in `.k8s.local`** — uses kops' gossip DNS, so no Cloud DNS zone needed | `my-cluster.k8s.local` |
| `KOPS_STATE_STORE` | Where kops keeps its state (a GCS bucket path) | `gs://kops-state-my-cluster/` |
| `KUBERNETES_VERSION` | Which Kubernetes version to deploy | `1.28.8` |

```bash
BUCKET_NAME="kops-state-<cluster>"
KOPS_CLUSTER_NAME="<cluster>.k8s.local"
KOPS_STATE_STORE="gs://${BUCKET_NAME}/"
KUBERNETES_VERSION="1.28.8"
```

Cluster-shape and InstanceGroup variables are set later, at [Phase 4b](#phase-4b) and [Phase 4d](#phase-4d).

After editing the file, reload it before running any recipe:

```bash
exit
just cluster-env --env <env> --cluster <cluster>
```

> All commands below assume `${PROJECT_ID}` and `${BUCKET_NAME}` are loaded from the active cluster env file.

Phases 1a–2 run with the `sa-manager` credential ([Section 2](#2-authentication-setup)); [Phase 3](#phase-3) rotates to the narrower `kops-cluster-creator` key.

<a id="phase-1a"></a>

### Phase 1a — Enable APIs
```bash
just gcp-api enable-apis --api-file gcs-files/apis/enabled_apis.txt
```
<a id="phase-1b"></a>

### Phase 1b — Disable unused APIs
```bash
just gcp-api disable-apis --api-file gcs-files/apis/disable_enabled_apis.txt
```
<a id="phase-2"></a>

### Phase 2 — Create kops-cluster-creator SA
```bash
just gcp-sa create-sa --sa-name kops-cluster-creator \
  --roles-file gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  --output-file credentials/${PROJECT_ID}/kops-cluster-creator.json
```
A dedicated identity with just the 8 roles it needs (compute admin, network admin, storage admin, IAM, logging). This is least privilege in action — rather than using the all-powerful `sa-manager` forever, we mint a narrower key specifically for cluster provisioning.

<a id="phase-3"></a>

### Phase 3 — Rotate credential
```bash
just cluster-cred --env <env> --cluster <cluster> --key credentials/${PROJECT_ID}/kops-cluster-creator.json
```
This updates the `GOOGLE_APPLICATION_CREDENTIALS` line in your cluster env file to point at the new `kops-cluster-creator` key. To pick it up, exit the sub-shell and re-enter:
```bash
exit
just cluster-env --env <env> --cluster <cluster>
```
From this point on, the narrower `kops-cluster-creator` key is what's used for everything. The `sa-manager` key goes dormant — and `.envrc` was never touched.

<a id="phase-4"></a>

### Phase 4 — HA cluster manifest

> These steps build the cluster blueprint and InstanceGroups. Defaults are a single control plane and public topology unless you set production overrides in the cluster-shape table in [Phase 4b](#phase-4b) (e.g. `TOPOLOGY=private`, `TOTAL_MASTER=3`, `CONTROL_PLANE_ZONES`). Multi-pool workers are applied in Phase 4d.

<a id="phase-4a"></a>

### Phase 4a — Create state bucket

The state bucket holds your entire cluster configuration (uses `PROJECT_ID` and `BUCKET_NAME` from your env file — [Section 1.3.2](#132-variables-overview)). Harden it before writing anything:
```bash
just gcp-cluster create-state-bucket
```
**What it does:** Creates the bucket (skips if it already exists), then enables versioning, uniform bucket-level access, and public access prevention. If the bucket name is already taken globally, pick a different bucket name.

<a id="phase-4b"></a>

### Phase 4b — Generate cluster manifest

This command writes the manifest to the state bucket but provisions **nothing** — it is a blueprint, not a building.

```bash
just gcp-cluster create-cluster-config
```
Every variable below is read from your cluster env file. The **Default** column is only a fallback — it applies when the variable is unset. To change a value for your cluster, set it in `.env.<env>.<cluster>`:

```bash
TOTAL_NODES=6
TOPOLOGY=private
NETWORKING=cilium
```

Then `exit` and re-run `just cluster-env` before `create-cluster-config`:

| Variable | Controls | Default | Example (dev) | Example (prod) |
|----------|----------|---------|---------------|----------------|
| `TOTAL_NODES` | How many worker nodes | `4` | `2` | `6` |
| `NODE_MACHINE` | Worker VM type | `n1-custom-2-4096` | `n1-custom-1-2048` | `n1-custom-4-8192` |
| `NODE_DISK_SIZE` | Worker boot disk (GB) | `100` | `50` | `200` |
| `TOTAL_MASTER` | Control plane nodes | `1` | `1` | `1` |
| `MASTER_MACHINE` | Control plane VM type | `n1-custom-4-8192` | `n1-custom-2-4096` | `n1-custom-8-16384` |
| `MASTER_DISK_SIZE` | Control plane boot disk (GB) | `75` | `50` | `150` |
| `COMPUTE_IMAGE` | OS image for all nodes | `ubuntu-os-cloud/ubuntu-2204-jammy-v20240829` | *(leave default)* | *(leave default)* |
| `TOPOLOGY` | Cluster network topology | `public` | `public` | `private` |
| `NETWORKING` | CNI plugin | `cilium-etcd` | `cilium-etcd` | `cilium` |
| `CONTROL_PLANE_ZONES` | Control plane zones (comma-separated) | *(none)* | `us-central1-a` | `us-central1-a,us-central1-b,us-central1-c` |
| `NODE_ZONES` | Worker node zones (comma-separated) | `us-central1-c` | `us-central1-c` | `us-central1-a,us-central1-b,us-central1-c` |
| `API_ACCESS_CIDR` | Administrative CIDR for API access | *(none — API open)* | *(leave unset)* | `"<your-ip>/32"` |

> **Production HA tip:** set `TOPOLOGY=private`, `NETWORKING=cilium`, `TOTAL_MASTER=3`, and `CONTROL_PLANE_ZONES` for a production, multi-zone control plane.

> **What this creates:** Base cluster + one default worker InstanceGroup sized by `TOTAL_NODES` / `NODE_MACHINE` / `NODE_DISK_SIZE`. Extra pools (stateful DB, batch/Spot) come from templates in Phase 4d — not from `create-cluster-config`.

<a id="phase-4c"></a>

### Phase 4c — Edit manifest for security
```bash
kops edit cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE}
```
Adjust these fields for defense-in-depth:

| Field Path | Recommended Value | Why |
|------------|-------------------|-----|
| `spec.cloudProvider.gce.serviceAccount` | `"kops-nodes-sa@<project-id>.iam.gserviceaccount.com"` | Least-privilege node identity |
| `spec.kubernetesApiAccess` | `["<your-ip>/32"]` | Restrict API access — never leave `0.0.0.0/0` in production |
| `spec.cloudProvider.gce.pdCSIDriver.enabled` | `true` | GCE Persistent Disk CSI driver for PVs |
| `spec.etcdClusters[0].etcdMembers[*].volumeType` | `pd-ssd` | Dedicated SSD for etcd-main (fsync latency critical) |
| `spec.etcdClusters[1].etcdMembers[*].volumeType` | `pd-ssd` | Dedicated SSD for etcd-events |
| `spec.clusterAutoscaler.enabled` | `true` | Enable Cluster Autoscaler — detects pending pods and scales worker pools via GCE MIGs. Required for elastic InstanceGroups (stateless-web, batch-spot). The node SA automatically gets the MIG update permission |
| `spec.nodeProblemDetector.enabled` | `true` | Enable Node Problem Detector — monitors kernel logs (`dmesg`, `journald`) for hardware failures and kernel deadlocks, taints unhealthy nodes to prevent scheduling. Critical for detecting zombie nodes before they cascade |
| `spec.certManager.enabled` | `true` | Enable cert-manager — handles x509 certificate provisioning and rotation for cluster services. Required by many addons (e.g., admission webhooks). One installation per cluster; set `spec.certManager.managed: false` if you run cert-manager externally |
| `spec.kubeDNS.nodeLocalDNS.enabled` | `true` | Enable node-local DNS cache — runs a caching DNS agent on every node as a DaemonSet, reducing DNS latency and CoreDNS load. Low overhead (5Mi memory, 25m CPU per node). Prevents DNS timeouts during CoreDNS restarts |

After editing, save the manifest — this is **required**, not optional. It is the diff reference every re-creation depends on; skip it and you re-type these security edits from memory after teardown:

```bash
just gcp-cluster save-cluster-manifest
```

Writes `config/kops/${PROJECT_ID}/cluster-manifest.yaml` (per-cluster, scoped by your env file's `PROJECT_ID`) and prints the commit command. Check it in. On re-creation, `just gcp-cluster diff-cluster-manifest` shows which security edits to re-apply.

<a id="phase-4d"></a>

### Phase 4d — Configure InstanceGroups

kOps uses InstanceGroups to define node pools. Production needs three pools — each is defined as a checked-in YAML template in `config/kops/instancegroups/`. Apply them all in one command:

```bash
just gcp-cluster apply-instancegroups
```
Applies the checked-in templates under `config/kops/instancegroups/`. Like Phase 4b, the **Default** column below is only a fallback — override any value per cluster by setting the variable in `.env.<env>.<cluster>`:

| Template | Env Vars | Purpose |
|----------|----------|---------|
| `stateless-web.yaml.tmpl` | `STATELESS_MACHINE`, `STATELESS_MIN`, `STATELESS_MAX`, `NODE_ZONE_A/B/C` | Elastic web/API pool |
| `stateful-db.yaml.tmpl` | `STATEFUL_MACHINE`, `STATEFUL_MIN`, `STATEFUL_MAX`, `NODE_ZONE_A/B/C` | Static database pool |
| `batch-spot.yaml.tmpl` | `BATCH_MACHINE`, `BATCH_MIN`, `BATCH_MAX`, `NODE_ZONE_A/B/C` | Spot pool with taints |

| Variable | Controls | Default | Example (prod) |
|----------|----------|---------|----------------|
| `STATELESS_MACHINE` | Stateless pool VM type | `e2-standard-4` | `e2-standard-4` |
| `STATELESS_MIN` / `STATELESS_MAX` | Stateless pool scaling bounds | `3` / `6` | `3` / `6` |
| `STATEFUL_MACHINE` | Stateful DB pool VM type | `n2-standard-4` | `n2-standard-4` |
| `STATEFUL_MIN` / `STATEFUL_MAX` | Stateful pool scaling bounds | `3` / `3` | `3` / `3` |
| `BATCH_MACHINE` | Batch/Spot pool VM type | `e2-standard-4` | `e2-standard-4` |
| `BATCH_MIN` / `BATCH_MAX` | Batch pool scaling bounds | `0` / `4` | `0` / `4` |
| `NODE_ZONE_A/B/C` | Individual zones for template zone lists | `us-central1-a/b/c` | `us-central1-a/b/c` |

> If you add or change any of these variables inside an active cluster sub-shell, `exit` and re-run `just cluster-env` before `apply-instancegroups`.

> Edit templates once for shared shape; override sizes per environment via the env file. Phase 4c stays manual (`kops edit` / human security judgment).

**Idempotency:** Phases 1a–3 and 4a are safe to re-run. Phase 4b is *not* — a second `create-cluster-config` against an existing cluster name errors if the manifest already lives in the state bucket.

<a id="changing-shape-after-generation"></a>

### Changing Shape After Generation

**Env-file variables are read only at generation time.** `create-cluster-config` bakes them into the manifest and the default `nodes` InstanceGroup once. After that, changing a variable and reloading the env does *nothing* to the state bucket — there is no re-sync command.

**Before Phase 5** (manifest written, no VMs yet) — clean and regenerate. This wipes the whole cluster definition, so re-do [Phase 4c](#phase-4c) edits (+ `save-cluster-manifest`) and any [Phase 4d](#phase-4d) pools you had already applied:

```bash
kops delete cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE} --yes
just gcp-cluster create-cluster-config
```

**After Phase 5** (provisioned) — do *not* delete. Edit in place instead: `kops edit cluster` for cluster-level variables, `kops edit instancegroup nodes` for worker sizing (`TOTAL_NODES` / `NODE_MACHINE` / `NODE_DISK_SIZE`), then `just gcp-cluster update-cluster`.

> **⚠️ Env-file drift is not recommended at all.** Hand-editing the manifest (`kops edit …`) splits the truth in two — the env file says one thing, the state bucket another. Keep the env file the single source of truth: decide the shape there before Phase 4b, or regenerate from it as above.

<a id="phase-5"></a>

### Phase 5 — Apply (version-aware)

```bash
just gcp-cluster update-cluster
```

One recipe; version dispatch is automatic:

| Action | kOps ≤ 1.30.x (repo pin: v1.29.2) | kOps ≥ 1.31.0 |
|--------|-----------------------------------|---------------|
| Apply | `kops update cluster --yes --admin` | `kops reconcile cluster --yes` |
| Rolling update | separate `kops rolling-update` if needed | included in `reconcile` |
| Validate | `kops validate cluster` | same |

> This repo pins kOps in `.tool-versions` (currently v1.29.2; per-cluster pins live in `.tool-versions.<env>.<cluster>` — see [Section 1.4.1](#141-tool-version-files)). Do not mix `update` and `reconcile` workflows across versions. Prefer `just gcp-cluster update-cluster` so the binary picks the right subcommand.

<a id="phase-6"></a>

### Phase 6 — Validate HA topology

A single recipe checks everything:

```bash
just gcp-cluster validate-kops-ha
```
Run this after apply. Fix any failures it reports before moving on.

<a id="phase-7"></a>

### Phase 7 — Post-Provisioning Hardening

After Phase 6, verify the Phase 4c hardening components (Cluster Autoscaler, Node Problem Detector, cert-manager, node-local DNS, Metrics Server):

```bash
just gcp-cluster validate-hardening
```

## 4. Exploring the Cluster

Once the cluster is provisioned and validated ([Phase 6](#phase-6) & [Phase 7](#phase-7)), export the kubeconfig and explore:

```bash
just gcp-cluster export-kubeconfig
just gcp-cluster k9s
```
For a kubeconfig with custom name and duration:
```bash
just gcp-cluster export-named-kubeconfig --name <name> --duration-hours <hours>
```

## 5. Disposable Cluster Lifecycle

Spin up when work begins, tear down when done, repeat as needed. No idle compute costs, no stale state.

### 5.1 Mindset

Destroy and recreate from the same blueprint. The blueprint — env file, InstanceGroup templates, SA keys, state bucket — is durable. GCE instances are transient. kOps stores the cluster spec in the GCS state bucket. As long as that bucket (with versioning enabled) and your env file survive, recreation is possible from the same starting point.

> **⚠️ Reproducibility gap — Phase 4c is manual.** The `create-cluster-config` recipe generates the manifest from your env file, but Phase 4c requires human judgment. See [Phase 4c](#phase-4c) for how to capture edits for reproducibility.

You pay for GCE instances only while the cluster is up. The GCS state bucket costs very little per month. The main trade-off is re-creation time (instance boot + manifest review) vs. the cost of keeping instances running 24/7.

### 5.2 What Survives Teardown

| Artifact | Survives? | Why |
|----------|-----------|-----|
| GCS state bucket (`gs://${BUCKET_NAME}`) | ✅ Yes | kOps delete does **not** touch the state bucket. The versioned object history is preserved. |
| Per-cluster env file (`.env.<env>.<cluster>`) | ✅ Yes | A local file in the repo — never touched by teardown. |
| Per-cluster tool version file (`.tool-versions.<env>.<cluster>`) | ✅ Yes | A local file in the repo — part of the cluster blueprint. See [Section 1.4.1](#141-tool-version-files). |
| SSH keypair (`credentials/${PROJECT_ID}/k8sVM*`) | ✅ Yes | Local files. Reused across create/destroy cycles. |
| Service account keys (`credentials/${PROJECT_ID}/sa-manager.json`, `credentials/${PROJECT_ID}/kops-cluster-creator.json`, etc.) | ✅ Yes | Local files — one JSON key per SA per project. The SAs themselves live in the GCP project and are never deleted. |
| GCP project + enabled APIs | ✅ Yes | Teardown only removes cluster resources, not the project or its API enablement. |
| InstanceGroup templates (`config/kops/instancegroups/*.yaml.tmpl`) | ✅ Yes | Checked into the repo. |
| **GCE instances** (control-plane + workers) | ❌ Destroyed | These are the cluster. |
| **Boot disks + kOps-managed etcd volumes** | ❌ Destroyed | Deleted with the instances. |
| **Workload PersistentVolumes** (PVCs) | ⚠️ Depends | If the StorageClass reclaim policy is `Delete`, the PV is destroyed with the cluster. If `Retain`, the backing disk survives as an orphaned resource — it continues to accrue charges until manually deleted. Verify reclaim policies before teardown: `kubectl get sc` and `kubectl get pv`. |
| **Load balancers, forwarding rules, target pools** | ❌ Destroyed | kOps-created networking resources. |
| **Firewall rules** (kOps-managed) | ❌ Destroyed | Only the rules kOps created; project-wide rules are untouched. |
| **VPC network / subnets** | ⚠️ Depends | If kOps created the VPC (e.g., you used `--topology=private` with no pre-existing network), it is destroyed. If you pointed kOps at a pre-existing shared VPC, that network survives and is not modified. |

<a id="53-teardown--destroy-the-cluster"></a>

### 5.3 Teardown — Destroy the Cluster

```bash
# Dry-run (safe — preview what will be destroyed):
just gcp-cluster delete-cluster

# Full teardown (with confirmation prompt):
just gcp-cluster delete-cluster --confirm yes
```
**What the recipe does:** Preflight validation → dry-run → confirmation prompt → destroy → cleanup verification.

> **What survives teardown** is covered in [Section 5.2](#52-what-survives-teardown). The kubeconfig will point at a dead cluster — delete it or let re-creation overwrite it.

### 5.4 Optional: Delete the State Bucket

> **⚠️ This permanently deletes all versioned history.** For routine maintenance, use [GCS lifecycle rules](https://cloud.google.com/storage/docs/lifecycle) instead. Only run the command below when you are done permanently or troubleshooting corrupted state.

For a completely clean slate:
```bash
gcloud storage rm --recursive gs://${BUCKET_NAME}
```

Keeping the bucket costs very little and preserves recreate ability.

If deleted, re-run Phase 4a before Phase 4b.

<a id="55-re-creation--bring-it-back"></a>

### 5.5 Re-Creation — Bring It Back

After teardown, bringing the cluster back skips the heavy one-time setup. The GCP project already exists, APIs are enabled, service accounts and their keys are in place, the SSH keypair is still in `credentials/${PROJECT_ID}/`, and your per-cluster env file is untouched.

> **Shortcut:** `just gcp-cluster recreate-cluster` automates the checklist below (requiring only that the env is activated first). Walk through it manually when you want to review each phase.

**Re-creation checklist** (assuming the state bucket was **not** deleted):

1. **Activate the cluster env** (you're back in a fresh terminal):
   ```bash
   just cluster-env --env <env> --cluster <cluster>
   ```

2. **Regenerate the cluster manifest** (Phase 4b):
   ```bash
   just gcp-cluster create-cluster-config
   ```
   > ⚠️ If you get `Error: cluster "..." already exists`, the old cluster definition is still in the state bucket. Clear it manually: `kops delete cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE} --yes`.

3. **Edit the manifest** (Phase 4c):
   ```bash
   just gcp-cluster edit-cluster
   ```
   Re-apply the same security settings: `kubernetesApiAccess`, etcd volume types (`pd-ssd`), node service account, Cluster Autoscaler, Node Problem Detector, cert-manager, and node-local DNS. If you saved and checked in the manifest (see [Phase 4c](#phase-4c)), diff against your saved copy first:
   ```bash
   just gcp-cluster diff-cluster-manifest
   ```

4. **Apply InstanceGroups** (Phase 4d):
   ```bash
   just gcp-cluster apply-instancegroups
   ```

5. **Provision the cluster** (Phase 5):
   ```bash
   just gcp-cluster update-cluster
   ```

6. **Validate** (Phase 6):
   ```bash
   just gcp-cluster validate-kops-ha
   ```

7. **Verify hardening** (Phase 7):
   ```bash
   just gcp-cluster validate-hardening
   ```

**Re-creation time:** Primarily GCE instance boot and health checks, plus manifest review in Phase 4c.

### 5.6 Summary

- **First time:** Sections 1–4
- **Teardown:** `just gcp-cluster delete-cluster --confirm yes`
- **Recreate:** `just gcp-cluster recreate-cluster`
- **Repeat as needed**

### 5.7 Caveats

**kOps version drift.** If you upgrade kOps between cycles, check [release notes](https://kops.sigs.k8s.io/releases/) for breaking changes. The active versions file pins the version — for the dev default that is `.tool-versions` (run `just install-tool --name <tool> --version <version>` per tool to sync); per-cluster pins live in `.tool-versions.<env>.<cluster>` ([Section 1.4.1](#141-tool-version-files)).

**GCP quotas.** Frequent cycles may trigger GCE API rate-limiting. Quota is released on teardown, but rapid create/delete loops can throttle.

## 6. Next Steps

Your cluster is up and validated. Where to go from here:

- **Deploy applications** — Follow [`docs/pulumi-setup.md`](pulumi-setup.md) to deploy the Pulumi-managed application stack on top of your new cluster.
- **Understand the architecture** — Read [`docs/kops-gcp-architecture.md`](kops-gcp-architecture.md) for HA topology, InstanceGroup layout, and networking decisions.
- **Explore the cluster** — `just gcp-cluster k9s`, or `kubectl get all --all-namespaces`.
- **Tear it down when done** — [Section 5: Disposable Cluster Lifecycle](#5-disposable-cluster-lifecycle).
