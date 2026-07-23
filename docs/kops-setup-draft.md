# Kops Cluster Creation Guide

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Prerequisites & Tooling](#1-prerequisites--tooling)
  - [1.1 The GCP Project](#11-the-gcp-project)
  - [1.2 Core System Tools](#12-core-system-tools)
  - [1.3 asdf-Managed Tools](#13-asdf-managed-tools)
  - [1.4 SSH Keypair and Local Repo](#14-ssh-keypair-and-local-repo)
- [2. Authentication Setup](#2-authentication-setup)
- [3. Environment Variables — The Cluster Config File](#3-environment-variables--the-cluster-config-file)
  - [3.1 Create Your Cluster Env File](#31-create-your-cluster-env-file)
  - [3.2 Required Variables](#32-required-variables)
  - [3.2.1 Per-Project File Isolation](#321-per-project-file-isolation)
  - [3.3 Activate Your Cluster Env File](#33-activate-your-cluster-env-file)
  - [3.4 Optional Tuning Knobs](#34-optional-tuning-knobs)
- [4. Cluster Bootstrap](#4-cluster-bootstrap)
  - [Phase 0 — Credential & cluster-env](#phase-0)
  - [Phase 1a — Enable APIs](#phase-1a)
  - [Phase 1b — Disable unused APIs](#phase-1b)
  - [Phase 2 — Create kops-cluster-creator SA](#phase-2)
  - [Phase 3 — Rotate credential](#phase-3)
  - [Phase 4 — HA cluster manifest](#phase-4)
  - [Phase 4a — Create state bucket](#phase-4a)
  - [Phase 4b — Generate cluster manifest](#phase-4b)
  - [Phase 4c — Edit manifest for security](#phase-4c)
  - [Phase 4d — Configure InstanceGroups](#phase-4d)
  - [Phase 5 — Apply (version-aware)](#phase-5)
  - [Phase 6 — Validate HA topology](#phase-6)
  - [Phase 7 — Post-Provisioning Hardening](#phase-7)
- [5. Exploring the Cluster](#5-exploring-the-cluster)
- [6. Disposable Cluster Lifecycle](#6-disposable-cluster-lifecycle)
  - [6.1 Mindset](#61-mindset)
  - [6.2 What Survives Teardown](#62-what-survives-teardown)
  - [6.3 Teardown — Destroy the Cluster](#63-teardown--destroy-the-cluster)
  - [6.4 Optional: Delete the State Bucket](#64-optional-delete-the-state-bucket)
  - [6.5 Re-Creation — Bring It Back](#65-re-creation--bring-it-back)
  - [6.6 Summary](#66-summary)
  - [6.7 Caveats](#67-caveats)
- [7. Next Steps](#7-next-steps)

**Last tested**: 2026-07-16

> **Document type:** This is a **provisioning guide** — a sequential
> walkthrough for setting up a kops-managed Kubernetes cluster from scratch.
> For incident-response procedures (diagnostic decision trees, alert-linked
> runbooks), see the [kops architecture doc](kops-architecture.md) technical
> risks section, which maps failure modes to mitigations.

## Overview

This guide walks you through bootstrapping a kops-managed Kubernetes cluster on GCP from a fresh clone of the repo, all the way to a running cluster with a Pulumi-managed application stack on top.

- **Who it's for**: Platform operators who have access to a GCP project and the `sa-manager` service account key.
- **What you'll end up with**: A fully provisioned Kubernetes cluster (GCE instances, networking, IAM, state storage) plus the Pulumi scaffolding for deploying applications.
- **How long it takes**: ~30–45 minutes for a clean run (most of that is waiting for GCE instances to boot and pass health checks).
- **Prerequisites in one sentence**: An existing GCP project with billing enabled, `sa-manager.json` key, and the tools listed in Section 1.
- **Reusable across environments**: This guide is written to be generic — dev, staging, prod, whatever. Each environment gets its own GCP project (one cluster per project). Change the project ID, pick a cluster name, adjust a few optional knobs (node count, machine size), and the same steps produce a cluster in a different GCP project. The only per-environment artifact you create is a small env file (Section 3).
- **Pulumi post-provisioning**: Once the cluster is up, deploying the application stack is covered separately in [`docs/pulumi-setup.md`](pulumi-setup.md).

## Quick Setup

1. [Install the tools.](#1-prerequisites--tooling) `go`, `just`, `gcloud`, `direnv`, `jq` at system level, then `just install-asdf-plugins` for asdf-managed tools.
2. [Generate the SSH keypair and prepare the repo.](#14-ssh-keypair-and-local-repo) `just gcp-cluster generate-ssh-key <project-id>` (Ed25519 under `credentials/<project-id>/`).
3. [Set up authentication.](#2-authentication-setup) Place `sa-manager.json` in `credentials/<project-id>/`.
4. [Create your cluster env file.](#3-environment-variables--the-cluster-config-file) Copy `.env.dev.dcr-experiments`, then set the eight required variables (including `GOOGLE_APPLICATION_CREDENTIALS`).
5. [Bootstrap the cluster.](#4-cluster-bootstrap) Activate your env file (`just cluster-env`), then run the phased HA workflow.
6. [Verify and explore.](#5-exploring-the-cluster) Run `just gcp-cluster validate-kops-ha`, then `just gcp-cluster k9s`.

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

### 1.3 asdf-Managed Tools

We use `asdf` to pin exact versions of the remaining tools. With `just` already installed, run:

```bash
just install-asdf-plugins
```
This installs: [kubectl](https://kubernetes.io/docs/reference/kubectl/kubectl/), [kops](https://kops.sigs.k8s.io/), [pulumi](https://www.pulumi.com/docs/), [velero](https://velero.io/docs/), [mc](https://min.io/docs/minio/linux/reference/minio-mc.html).

To upgrade a specific tool version (must be defined in `.tool-versions`):
```bash
just install-tool <tool_name> <version>
# Example: just install-tool kubectl 1.28.8
```
### 1.4 SSH Keypair and Local Repo

Preferred — generates an **Ed25519** keypair under `credentials/<project-id>/` and refuses to overwrite an existing key:

```bash
just gcp-cluster generate-ssh-key <project-id>
# RSA-4096 fallback if you need it:
just gcp-cluster generate-ssh-key <project-id> rsa
```

What the recipe runs (Ed25519 default):

```bash
mkdir -p credentials/<project-id>
ssh-keygen -t ed25519 -f credentials/<project-id>/k8sVM -N "" -C "kops-cluster-nodes"
```

Both key files are gitignored. A fresh clone always needs the keypair (or a secure copy from another operator). Then set `SSH_KEY` in your per-cluster env file to the `.pub` path ([Section 3.2](#32-required-variables)).

> **Per-project isolation**: One cluster per GCP project. The `<project-id>` subdirectory scopes SSH keys to the project. See [Section 3.2.1](#321-per-project-file-isolation) for the full naming convention.

## 2. Authentication Setup

You need the **Service Account Manager** key. It has broad permissions (creating other service accounts, enabling APIs, managing IAM) so the rest of the process can run with tighter, least-privilege credentials.

1.  **Obtain Key**: Request the `sa-manager` JSON key from the project owner.

    **If you *are* the project owner**, create the SA and its key with one command:

    ```bash
    just gcp-sa setup-sa-manager <PROJECT_ID> credentials/<project-id>/sa-manager.json
    ```

    This creates the SA (skips if it already exists), binds all 13 manager roles, and downloads the JSON key. Always pass the second argument so the key lands under `credentials/<project-id>/` — never omit it.

    **If you're creating this SA manually in the GCP Console**, bind these 13 roles (all predefined, no custom roles needed):

    | Role | Purpose |
    |---|---|
    | `roles/iam.serviceAccountAdmin` | Create and delete child service accounts |
    | `roles/iam.serviceAccountCreator` | Create service accounts |
    | `roles/iam.serviceAccountKeyAdmin` | Create and rotate SA keys |
    | `roles/iam.serviceAccountUser` | Impersonate and attach SAs to resources |
    | `roles/iam.roleAdmin` | Create and manage custom IAM roles |
    | `roles/resourcemanager.projectIamAdmin` | Grant and revoke IAM roles on the project |
    | `roles/serviceusage.serviceUsageAdmin` | Enable and disable GCP APIs |
    | `roles/storage.admin` | Full control over Cloud Storage |
    | `roles/storage.hmacKeyAdmin` | Manage HMAC keys for storage |
    | `roles/cloudkms.admin` | Create and manage KMS keyrings and keys |
    | `roles/cloudkms.cryptoOperator` | Encrypt/decrypt with existing KMS keys |
    | `roles/compute.instanceAdmin.v1` | Full control over GCE instances |
    | `roles/aiplatform.user` | Vertex AI platform access |

    2.  **Save Key**: Place it in `./credentials/<project-id>/sa-manager.json`.

    The credential path goes into your **per-cluster env file** next — see [Section 3.2](#32-required-variables). Phases 2–3 of the bootstrap create a narrower `kops-cluster-creator` key and update that path. By the time the cluster is up, `sa-manager` is no longer in active use.

3.  **(Optional but recommended) Set up a gcloud named configuration**: If you also want `gcloud` CLI commands (`gcloud projects get-iam-policy`, `gcloud storage`, etc.) to run as `sa-manager`, create a dedicated named configuration for it:

    ```bash
    export PROJECT_ID="<your-gcp-project-id>"

    gcloud config configurations create sa-manager
    gcloud auth activate-service-account \
      sa-manager@${PROJECT_ID}.iam.gserviceaccount.com \
      --key-file=credentials/<project-id>/sa-manager.json
    gcloud config set project ${PROJECT_ID}
    gcloud config set compute/zone us-central1-c
    ```

    This creates an isolated identity slot — your personal login stays untouched in the `default` configuration. Switch back to it anytime:
    ```bash
    gcloud config configurations activate default
    ```
    Or flip back to sa-manager:
    ```bash
    gcloud config configurations activate sa-manager
    ```

    See who you are at any moment:
    ```bash
    gcloud auth list                     # which account is active?
    gcloud config configurations list    # all your named identities
    ```

    > **You can add more SA identities later the same way** (e.g., a `kops-creator` configuration for the `kops-cluster-creator` key). Each gets its own named slot — create a new gcloud configuration with `gcloud config configurations create <name>`, activate the SA with `gcloud auth activate-service-account`, and switch between them with `gcloud config configurations activate <name>`.

<a id="3-environment-variables--the-cluster-config-file"></a>

## 3. Environment Variables — The Cluster Config File

Before the bootstrap will work, it needs to know a handful of things about your cluster: its name, where to store its state, which SSH key to use, and so on. These are defined in a **per-cluster env file** — one file per environment, following the naming convention `.env.<env>.<cluster>` (e.g., `.env.dev.my-cluster`, `.env.staging.my-cluster`, `.env.prod.my-cluster`).

The repo ships with `.env.dev.dcr-experiments` as a concrete example. You can copy it as a starting point, or create your own from scratch — either way, the eight variables below are what matter (two project-level: `PROJECT_ID` and `BUCKET_NAME`; six cluster-level: the rest, including the credential path).

### 3.1 Create Your Cluster Env File

**Option A — copy the example and edit**:
```bash
cp .env.dev.dcr-experiments .env.<env>.<cluster-name>
# edit .env.<env>.<cluster-name> with your values
```
**Option B — create from scratch** using the table below as your checklist. The file is just `export VAR=value` lines — nothing fancy.

### 3.2 Required Variables

| Variable | What It Tells the Tooling | Example |
|----------|--------------------------|--------|
| `PROJECT_ID` | The GCP project where the cluster lives | `my-gcp-project` |
| `BUCKET_NAME` | Name of the GCS bucket where kops stores its state. Must be globally unique. | `kops-state-my-cluster` |
| `KOPS_CLUSTER_NAME` | The full DNS name of your cluster. **Must end in `.k8s.local`** — this uses kops' built-in gossip DNS so you don't need a Cloud DNS zone | `my-cluster.k8s.local` |
| `KOPS_STATE_STORE` | Where kops keeps its state (a GCS bucket path) | `gs://kops-state-my-cluster/` |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to the active service account JSON key for this cluster. Start with the `sa-manager` key from [Section 2](#2-authentication-setup); Phase 3 later rotates it to `kops-cluster-creator` | `${PWD}/credentials/<project-id>/sa-manager.json` |
| `KUBECONFIG` | Path to the kubeconfig file for this cluster | `${CLUSTER_OPS_PATH}/clusters/<project-id>/kubeconfig` |
| `SSH_KEY` | Path to the public SSH key injected into cluster nodes | `${CLUSTER_OPS_PATH}/credentials/<project-id>/k8sVM.pub` |
| `KUBERNETES_VERSION` | Which Kubernetes version to deploy | `1.28.8` |

> **Why `.k8s.local`?** That suffix tells kops to use gossip-based DNS — cluster nodes discover each other by chatting directly rather than through a managed DNS zone. It's simpler to set up and avoids needing a Cloud DNS zone as a prerequisite.

> **Credential lives in the env file, not `.envrc`.** Set `GOOGLE_APPLICATION_CREDENTIALS` in this per-cluster env file. `just cluster-env` loads it into the sub-shell ([Section 3.3](#33-activate-your-cluster-env-file)). Do not put it in `.envrc`.

#### 3.2.1 Per-Project File Isolation

Several required variables point at files on disk — SSH keys, kubeconfig, and service account keys in `credentials/`. **One GCP project hosts exactly one cluster.** This invariant simplifies naming: scope everything to the project ID.

**Recommended convention: project-id subdirectory.** Create a dedicated subdirectory under `credentials/` and `clusters/` named after your GCP project:

```
credentials/
├── <project-id-1>/
│   ├── k8sVM
│   ├── k8sVM.pub
│   ├── sa-manager.json
│   ├── kops-cluster-creator.json
│   └── pulumi-manager.json
├── <project-id-2>/
│   ├── k8sVM
│   ├── k8sVM.pub
│   └── ...

clusters/
├── <project-id-1>/
│   └── kubeconfig
└── <project-id-2>/
    └── kubeconfig
```

Each project subdirectory holds all files for that project's cluster — SSH keypair, all service account JSON keys, and any other per-project secrets. Consistent filenames (`k8sVM`, `sa-manager.json`, `kubeconfig`) are reused inside each subdirectory.

**Service account keys** follow the same convention: one JSON key per SA per project, stored under `credentials/<project-id>/<sa-name>.json`. The project ID uniquely identifies the project (and therefore the cluster, since they're 1:1).

> **Per-cluster env file = per-project paths.** Each cluster gets its own `.env.<env>.<cluster>` file (Section 3.1). Set the file-path variables (`KUBECONFIG`, `SSH_KEY`) and credential paths inside that file to point at the correct project subdirectory. Switching clusters with `just cluster-env` loads the right paths automatically.

### 3.3 Activate Your Cluster Env File

You switch between clusters by sourcing their env file into a sub-shell — one command, no `.envrc` mutation:

```bash
just cluster-env <env> <cluster-name>
# Example: just cluster-env dev my-cluster
```
You're now in a sub-shell with all the cluster variables loaded. Every `just` command targets that cluster. To switch clusters, `exit` back to the parent shell and run `just cluster-env` with a different env file.

### 3.4 Optional Tuning Knobs

These variables control the shape of your cluster. **Add them to the same per-cluster env file you created in Section 3.1**. If unset, the tooling uses the defaults below. You'll typically want smaller values for dev and larger ones for prod.

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

**InstanceGroup-specific variables** (read by `apply-instancegroups` for template processing):

| Variable | Controls | Default | Example (prod) |
|----------|----------|---------|----------------|
| `STATELESS_MACHINE` | Stateless pool VM type | `e2-standard-4` | `e2-standard-4` |
| `STATELESS_MIN` / `STATELESS_MAX` | Stateless pool scaling bounds | `3` / `6` | `3` / `6` |
| `STATEFUL_MACHINE` | Stateful DB pool VM type | `n2-standard-4` | `n2-standard-4` |
| `STATEFUL_MIN` / `STATEFUL_MAX` | Stateful pool scaling bounds | `3` / `3` | `3` / `3` |
| `BATCH_MACHINE` | Batch/Spot pool VM type | `e2-standard-4` | `e2-standard-4` |
| `BATCH_MIN` / `BATCH_MAX` | Batch pool scaling bounds | `0` / `4` | `0` / `4` |
| `NODE_ZONE_A/B/C` | Individual zones for template zone lists | `us-central1-a/b/c` | `us-central1-a/b/c` |

## 4. Cluster Bootstrap

With your per-cluster env file ready (it has `PROJECT_ID`, `BUCKET_NAME`, the six cluster-level vars from Section 3.2 including `GOOGLE_APPLICATION_CREDENTIALS`, plus any optional tuning knobs from 3.4), start by setting the credential and activating the cluster:

> All commands assume `${PROJECT_ID}` and `${BUCKET_NAME}` are already in your cluster env file (Section 3.2). The credential is managed through the env file too — `cluster-cred` writes it, `cluster-env` loads it.

<a id="phase-0"></a>

### Phase 0 — Credential & cluster-env
```bash
just cluster-cred <env> <cluster-name> credentials/<project-id>/sa-manager.json
just cluster-env <env> <cluster-name>
```
Phases 1a through 2 run with `sa-manager`'s broad permissions.

<a id="phase-1a"></a>

### Phase 1a — Enable APIs
```bash
just gcp-api enable-apis ${PROJECT_ID} gcs-files/apis/enabled_apis.txt
```
<a id="phase-1b"></a>

### Phase 1b — Disable unused APIs
```bash
just gcp-api disable-apis ${PROJECT_ID} gcs-files/apis/disable_enabled_apis.txt
```
<a id="phase-2"></a>

### Phase 2 — Create kops-cluster-creator SA
```bash
just gcp-sa create-sa ${PROJECT_ID} kops-cluster-creator \
  gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  credentials/${PROJECT_ID}/kops-cluster-creator.json
```
A dedicated identity with just the 8 roles it needs (compute admin, network admin, storage admin, IAM, logging). This is least privilege in action — rather than using the all-powerful `sa-manager` forever, we mint a narrower key specifically for cluster provisioning.

<a id="phase-3"></a>

### Phase 3 — Rotate credential
```bash
just cluster-cred <env> <cluster-name> credentials/<project-id>/kops-cluster-creator.json
```
This updates the `GOOGLE_APPLICATION_CREDENTIALS` line in your cluster env file to point at the new `kops-cluster-creator` key. To pick it up, exit the sub-shell and re-enter:
```bash
exit
just cluster-env <env> <cluster-name>
```
From this point on, the narrower `kops-cluster-creator` key is what's used for everything. The `sa-manager` key goes dormant — and `.envrc` was never touched.

<a id="phase-4"></a>

### Phase 4 — HA cluster manifest

> These steps build the cluster blueprint and InstanceGroups. Defaults are a single control plane and public topology unless you set production overrides in [Section 3.4](#34-optional-tuning-knobs) (`TOPOLOGY=private`, `TOTAL_MASTER=3`, `CONTROL_PLANE_ZONES`, etc.). Multi-pool workers are applied in Phase 4d.

<a id="phase-4a"></a>

### Phase 4a — Create state bucket

The state bucket holds your entire cluster configuration. Harden it before writing anything:
```bash
just gcp-cluster create-state-bucket ${PROJECT_ID} ${BUCKET_NAME}
```
**What it does:** Creates the bucket (skips if it already exists), then enables versioning, uniform bucket-level access, and public access prevention. If the bucket name is already taken globally, pick a different `<BUCKET_NAME>`.

<a id="phase-4b"></a>

### Phase 4b — Generate cluster manifest

This command writes the manifest to the state bucket but provisions **nothing** — it is a blueprint, not a building.

```bash
just gcp-cluster create-cluster-config ${PROJECT_ID} ${BUCKET_NAME}
```
Reads cluster shape from your env file ([Section 3.4](#34-optional-tuning-knobs)). Unset vars use the defaults listed there (e.g. `TOTAL_NODES=4`, `TOPOLOGY=public`, `NETWORKING=cilium-etcd`).

> **Production HA tip:** Set `TOPOLOGY=private`, `NETWORKING=cilium`, `TOTAL_MASTER=3`, and `CONTROL_PLANE_ZONES` in your cluster env file. See the production examples in [Section 3.4](#34-optional-tuning-knobs).

> **What this creates:** Base cluster + one default worker InstanceGroup sized by `TOTAL_NODES` / `NODE_MACHINE` / `NODE_DISK_SIZE`. Extra pools (stateful DB, batch/Spot) come from templates in Phase 4d — not from `create-cluster-config`.

<a id="phase-4c"></a>

### Phase 4c — Edit manifest for security
```bash
kops edit cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE}
```
Adjust these fields for defense-in-depth:

| Field Path | Recommended Value | Why |
|------------|-------------------|-----|
| `spec.cloudProvider.gce.serviceAccount` | `"kops-nodes-sa@<PROJECT_ID>.iam.gserviceaccount.com"` | Least-privilege node identity |
| `spec.kubernetesApiAccess` | `["<your-admin-cidr>/32"]` | Restrict API access — never leave `0.0.0.0/0` in production |
| `spec.cloudProvider.gce.pdCSIDriver.enabled` | `true` | GCE Persistent Disk CSI driver for PVs |
| `spec.etcdClusters[0].etcdMembers[*].volumeType` | `pd-ssd` | Dedicated SSD for etcd-main (fsync latency critical) |
| `spec.etcdClusters[1].etcdMembers[*].volumeType` | `pd-ssd` | Dedicated SSD for etcd-events |
| `spec.clusterAutoscaler.enabled` | `true` | Enable Cluster Autoscaler — detects pending pods and scales worker pools via GCE MIGs. Required for elastic InstanceGroups (stateless-web, batch-spot). The node SA automatically gets the MIG update permission |
| `spec.nodeProblemDetector.enabled` | `true` | Enable Node Problem Detector — monitors kernel logs (`dmesg`, `journald`) for hardware failures and kernel deadlocks, taints unhealthy nodes to prevent scheduling. Critical for detecting zombie nodes before they cascade |
| `spec.certManager.enabled` | `true` | Enable cert-manager — handles x509 certificate provisioning and rotation for cluster services. Required by many addons (e.g., admission webhooks). One installation per cluster; set `spec.certManager.managed: false` if you run cert-manager externally |
| `spec.kubeDNS.nodeLocalDNS.enabled` | `true` | Enable node-local DNS cache — runs a caching DNS agent on every node as a DaemonSet, reducing DNS latency and CoreDNS load. Low overhead (5Mi memory, 25m CPU per node). Prevents DNS timeouts during CoreDNS restarts |

> **Tip — Capture your edits for reproducibility.** After completing Phase 4c, export the edited manifest:
> ```bash
> kops get cluster -o yaml > config/kops/cluster-manifest.yaml
> ```
> Check this into the repo. On re-creation, diff against the fresh manifest to re-apply the same security edits.

<a id="phase-4d"></a>

### Phase 4d — Configure InstanceGroups

kOps uses InstanceGroups to define node pools. Production needs three pools — each is defined as a checked-in YAML template in `config/kops/instancegroups/`. Apply them all in one command:

```bash
just gcp-cluster apply-instancegroups
```
Applies the checked-in templates under `config/kops/instancegroups/`, substituting env vars from [Section 3.4](#34-optional-tuning-knobs):

| Template | Env Vars | Purpose |
|----------|----------|---------|
| `stateless-web.yaml.tmpl` | `STATELESS_MACHINE`, `STATELESS_MIN`, `STATELESS_MAX`, `NODE_ZONE_A/B/C` | Elastic web/API pool |
| `stateful-db.yaml.tmpl` | `STATEFUL_MACHINE`, `STATEFUL_MIN`, `STATEFUL_MAX`, `NODE_ZONE_A/B/C` | Static database pool |
| `batch-spot.yaml.tmpl` | `BATCH_MACHINE`, `BATCH_MIN`, `BATCH_MAX`, `NODE_ZONE_A/B/C` | Spot pool with taints |

> Edit templates once for shared shape; override sizes per environment via the env file. Phase 4c stays manual (`kops edit` / human security judgment).

**Idempotency:** Phases 0–3 and 4a are safe to re-run. Phase 4b is *not* — a second `create-cluster-config` against an existing cluster name errors if the manifest already lives in the state bucket.

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

> This repo pins kOps in `.tool-versions` (currently v1.29.2). Do not mix `update` and `reconcile` workflows across versions. Prefer `just gcp-cluster update-cluster` so the binary picks the right subcommand.

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

## 5. Exploring the Cluster

Once the cluster is provisioned and validated ([Phase 6](#phase-6) & [Phase 7](#phase-7)), export the kubeconfig and explore:

```bash
just gcp-cluster export-kubeconfig
just gcp-cluster k9s
```
For a kubeconfig with custom name and duration:
```bash
just gcp-cluster export-named-kubeconfig <NAME> <HOURS>
```

## 6. Disposable Cluster Lifecycle

Spin up when work begins, tear down when done, repeat as needed. No idle compute costs, no stale state.

### 6.1 Mindset

Destroy and recreate from the same blueprint. The blueprint — env file, InstanceGroup templates, SA keys, state bucket — is durable. GCE instances are transient. kOps stores the cluster spec in the GCS state bucket. As long as that bucket (with versioning enabled) and your env file survive, recreation is possible from the same starting point.

> **⚠️ Reproducibility gap — Phase 4c is manual.** The `create-cluster-config` recipe generates the manifest from your env file, but Phase 4c requires human judgment. See [Phase 4c](#phase-4c) for how to capture edits for reproducibility.

You pay for GCE instances only while the cluster is up. The GCS state bucket costs very little per month. The main trade-off is re-creation time (instance boot + manifest review) vs. the cost of keeping instances running 24/7.

### 6.2 What Survives Teardown

| Artifact | Survives? | Why |
|----------|-----------|-----|
| GCS state bucket (`gs://${BUCKET_NAME}`) | ✅ Yes | kOps delete does **not** touch the state bucket. The versioned object history is preserved. |
| Per-cluster env file (`.env.<env>.<cluster-name>`) | ✅ Yes | A local file in the repo — never touched by teardown. |
| SSH keypair (`credentials/<project-id>/k8sVM*`) | ✅ Yes | Local files. Reused across create/destroy cycles. |
| Service account keys (`credentials/<project-id>/sa-manager.json`, `credentials/<project-id>/kops-cluster-creator.json`, etc.) | ✅ Yes | Local files — one JSON key per SA per project. The SAs themselves live in the GCP project and are never deleted. |
| GCP project + enabled APIs | ✅ Yes | Teardown only removes cluster resources, not the project or its API enablement. |
| InstanceGroup templates (`config/kops/instancegroups/*.yaml.tmpl`) | ✅ Yes | Checked into the repo. |
| **GCE instances** (control-plane + workers) | ❌ Destroyed | These are the cluster. |
| **Boot disks + kOps-managed etcd volumes** | ❌ Destroyed | Deleted with the instances. |
| **Workload PersistentVolumes** (PVCs) | ⚠️ Depends | If the StorageClass reclaim policy is `Delete`, the PV is destroyed with the cluster. If `Retain`, the backing disk survives as an orphaned resource — it continues to accrue charges until manually deleted. Verify reclaim policies before teardown: `kubectl get sc` and `kubectl get pv`. |
| **Load balancers, forwarding rules, target pools** | ❌ Destroyed | kOps-created networking resources. |
| **Firewall rules** (kOps-managed) | ❌ Destroyed | Only the rules kOps created; project-wide rules are untouched. |
| **VPC network / subnets** | ⚠️ Depends | If kOps created the VPC (e.g., you used `--topology=private` with no pre-existing network), it is destroyed. If you pointed kOps at a pre-existing shared VPC, that network survives and is not modified. |

<a id="63-teardown--destroy-the-cluster"></a>

### 6.3 Teardown — Destroy the Cluster

```bash
# Dry-run (safe — preview what will be destroyed):
just gcp-cluster delete-cluster

# Full teardown (with confirmation prompt):
just gcp-cluster delete-cluster yes
```
**What the recipe does:** Preflight validation → dry-run → confirmation prompt → destroy → cleanup verification.

> **What survives teardown** is covered in [Section 6.2](#62-what-survives-teardown). The kubeconfig will point at a dead cluster — delete it or let re-creation overwrite it.

### 6.4 Optional: Delete the State Bucket

> **⚠️ This permanently deletes all versioned history.** For routine maintenance, use [GCS lifecycle rules](https://cloud.google.com/storage/docs/lifecycle) instead. Only run the command below when you are done permanently or troubleshooting corrupted state.

For a completely clean slate:
```bash
gcloud storage rm --recursive gs://${BUCKET_NAME}
```

Keeping the bucket costs very little and preserves recreate ability.

If deleted, re-run Phase 4a before Phase 4b.

<a id="65-re-creation--bring-it-back"></a>

### 6.5 Re-Creation — Bring It Back

After teardown, bringing the cluster back skips the heavy one-time setup. The GCP project already exists, APIs are enabled, service accounts and their keys are in place, the SSH keypair is still in `credentials/<project-id>/`, and your per-cluster env file is untouched.

**Re-creation checklist** (assuming the state bucket was **not** deleted):

1. **Activate the cluster env** (you're back in a fresh terminal):
   ```bash
   just cluster-env <env> <cluster-name>
   ```

2. **Regenerate the cluster manifest** (Phase 4b):
   ```bash
   just gcp-cluster create-cluster-config ${PROJECT_ID} ${BUCKET_NAME}
   ```
   > ⚠️ If you get `Error: cluster "..." already exists`, the old cluster definition is still in the state bucket. Clear it manually: `kops delete cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE} --yes`.

3. **Edit the manifest** (Phase 4c):
   ```bash
   just gcp-cluster edit-cluster
   ```
   Re-apply the same security settings: `kubernetesApiAccess`, etcd volume types (`pd-ssd`), node service account, Cluster Autoscaler, Node Problem Detector, cert-manager, and node-local DNS. If you saved and checked in the manifest (see [Phase 4c](#phase-4c)), diff against your saved copy:
   ```bash
   kops get cluster -o yaml > /tmp/fresh-manifest.yaml
   diff config/kops/cluster-manifest.yaml /tmp/fresh-manifest.yaml
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

### 6.6 Summary

- **First time:** Sections 1–4
- **Teardown:** `just gcp-cluster delete-cluster yes`
- **Recreate:** `just gcp-cluster recreate-cluster`
- **Repeat as needed**

### 6.7 Caveats

**kOps version drift.** If you upgrade kOps between cycles, check [release notes](https://kops.sigs.k8s.io/releases/) for breaking changes. The `.tool-versions` file pins the version — run `just install-asdf-plugins` to sync.

**GCP quotas.** Frequent cycles may trigger GCE API rate-limiting. Quota is released on teardown, but rapid create/delete loops can throttle.

## 7. Next Steps

Your cluster is up and validated. Where to go from here:

- **Deploy applications** — Follow [`docs/pulumi-setup.md`](pulumi-setup.md) to deploy the Pulumi-managed application stack on top of your new cluster.
- **Understand the architecture** — Read [`docs/kops-architecture.md`](kops-architecture.md) for HA topology, InstanceGroup layout, and networking decisions.
- **Explore the cluster** — `just gcp-cluster k9s`, or `kubectl get all --all-namespaces`.
- **Tear it down when done** — [Section 6: Disposable Cluster Lifecycle](#6-disposable-cluster-lifecycle).
