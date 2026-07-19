# Kops Cluster Creation Guide

**Last tested**: 2026-07-16

> **Document type:** This is a **provisioning guide** — a sequential
> walkthrough for setting up a kops-managed Kubernetes cluster from scratch.
> For incident-response procedures (diagnostic decision trees, alert-linked
> runbooks), see the [kops architecture doc](kops-architecture.md) technical
> risks section, which maps failure modes to mitigations.

## Table of Contents
- [Overview](#overview)
- [Quick Setup](#quick-setup)
- [1. Prerequisites & Tooling](#1-prerequisites--tooling)
  - [1.1 The GCP Project](#11-the-gcp-project)
  - [1.2 Core System Tools](#12-core-system-tools)
  - [1.3 asdf-Managed Tools](#13-asdf-managed-tools)
  - [1.4 SSH Keypair for Cluster Nodes](#14-ssh-keypair-for-cluster-nodes)
  - [1.5 Prepare the Local Repo](#15-prepare-the-local-repo)
- [2. Authentication Setup](#2-authentication-setup)
- [3. Environment Variables — The Cluster Config File](#3-environment-variables--the-cluster-config-file)
  - [3.1 Create Your Cluster Env File](#31-create-your-cluster-env-file)
  - [3.2 Required Variables](#32-required-variables)
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
- [5. Production HA Deployment Reference](#5-production-ha-deployment-reference)
  - [5.1 Which Setting Controls Which Pool](#51-which-setting-controls-which-pool)
  - [5.2 Version Command Matrix](#52-version-command-matrix)
  - [5.3 Rollback](#53-rollback)
  - [5.4 Automation Summary](#54-automation-summary)
- [6. Verification & Access](#6-verification--access)
  - [6.1 Sanity Checks (Per Phase)](#61-sanity-checks-per-phase)
  - [6.2 Cluster Health](#62-cluster-health)
  - [6.3 Getting Inside the Cluster](#63-getting-inside-the-cluster)
- [7. Common Pitfalls](#7-common-pitfalls)
  - [7.1 ".envrc Changes Don't Take Effect" (~35% of issues)](#71-envrc-changes-dont-take-effect-35-of-issues)
  - [7.2 "Missing KOPS_CLUSTER_NAME / SSH_KEY / KUBERNETES_VERSION" (~25% of issues)](#72-missing-kops_cluster_name--ssh_key--kubernetes_version-25-of-issues)
  - [7.3 Re-running Against an Existing Cluster (~15% of issues)](#73-re-running-against-an-existing-cluster-15-of-issues)
  - [7.4 Forgot the SSH Keypair (~10% of issues)](#74-forgot-the-ssh-keypair-10-of-issues)
  - [7.5 Wrong KOPS_CLUSTER_NAME Suffix (~8% of issues)](#75-wrong-kops_cluster_name-suffix-8-of-issues)
  - [7.6 "Service Not Enabled" During SA Creation (~7% of issues)](#76-service-not-enabled-during-sa-creation-7-of-issues)
- [8. Escalation — When to Call for Help](#8-escalation--when-to-call-for-help)
- [9. Disposable Cluster Lifecycle](#9-disposable-cluster-lifecycle)
  - [9.1 Mindset: Cluster as Cattle](#91-mindset-cluster-as-cattle)
  - [9.2 What Survives Teardown](#92-what-survives-teardown)
  - [9.3 Teardown — Destroy the Cluster](#93-teardown--destroy-the-cluster)
  - [9.4 Optional: Delete the State Bucket](#94-optional-delete-the-state-bucket)
  - [9.5 Re-Creation — Bring It Back](#95-re-creation--bring-it-back)
  - [9.6 The Full Cycle (Recipe)](#96-the-full-cycle-recipe)
  - [9.7 Caveats](#97-caveats)
- [10. Next Steps](#10-next-steps)

## Overview

This guide walks you through bootstrapping a kops-managed Kubernetes cluster on GCP from a fresh clone of the repo, all the way to a running cluster with a Pulumi-managed application stack on top.

- **Who it's for**: Platform operators who have access to a GCP project and the `sa-manager` service account key.
- **What you'll end up with**: A fully provisioned Kubernetes cluster (GCE instances, networking, IAM, state storage) plus the Pulumi scaffolding for deploying applications.
- **How long it takes**: ~30–45 minutes for a clean run (most of that is waiting for GCE instances to boot and pass health checks).
- **Prerequisites in one sentence**: An existing GCP project with billing enabled, `sa-manager.json` key, and the tools listed in Section 1.
- **Reusable across environments**: This guide is written to be generic — dev, staging, prod, whatever. Change the project ID, pick a cluster name, adjust a few optional knobs (node count, machine size), and the same steps produce a cluster in a different GCP project. The only per-environment artifact you create is a small env file (Section 3).
- **Pulumi post-provisioning**: Once the cluster is up, deploying the application stack is covered separately in [`docs/pulumi-setup.md`](pulumi-setup.md).

## Quick Setup

This is the short path to a working cluster. It is an orientation, not a replacement for the detailed instructions — some steps require tool installations, credential provisioning, or env-file choices before they can succeed. Follow the linked section whenever you need the full commands or rationale.

1. **Install the tools.** Make sure `go`, `just`, `gcloud`, `direnv`, and `jq` are installed at the system level, then run `just install-asdf-plugins` for the asdf-managed tools. See [1. Prerequisites & Tooling](#1-prerequisites--tooling).
2. **Generate the SSH keypair.** Run `ssh-keygen -t rsa -b 4096 -f credentials/k8sVM -N "" -C "kops-cluster-nodes"`. No Justfile recipe does this for you — it must be done by hand. See [1.4 SSH Keypair for Cluster Nodes](#14-ssh-keypair-for-cluster-nodes).
3. **Build the repo.** Run `go build ./...` — it should succeed silently (no output = no errors). See [1.5 Prepare the Local Repo](#15-prepare-the-local-repo).
4. **Set up authentication.** Place `sa-manager.json` in `credentials/` and configure `GOOGLE_APPLICATION_CREDENTIALS` with `just set-env-var`. See [2. Authentication Setup](#2-authentication-setup).
5. **Create your cluster env file.** Copy `.env.dev.dcr-experiments`, then edit the seven required variables: `PROJECT_ID`, `BUCKET_NAME`, `KOPS_CLUSTER_NAME`, `KOPS_STATE_STORE`, `KUBECONFIG`, `SSH_KEY`, and `KUBERNETES_VERSION`. See [3. Environment Variables](#3-environment-variables--the-cluster-config-file).
6. **Bootstrap the cluster (HA production).** Activate your env file with `just cluster-env <env> <cluster>`, then run Phases 0–3 (credential → APIs → SA → credential rotation). For the HA production cluster, use the dedicated recipes: `just gcp-cluster create-state-bucket` → `just gcp-cluster create-cluster-config` → `just gcp-cluster edit-cluster` (manual step — opens $EDITOR) → `just gcp-cluster apply-instancegroups` → `just gcp-cluster update-cluster` (auto-detects kOps version) → `just gcp-cluster validate-kops-ha`. See [4. Cluster Bootstrap](#4-cluster-bootstrap) and [5. Production HA Deployment Reference](#5-production-ha-deployment-reference).

   > **Note:** The `cluster-ops` binary replaced the legacy `create-kops-cluster` recipe. For single-pool dev clusters, use `just gcp-cluster create-cluster-config` and `just gcp-cluster update-cluster` directly. For HA production, follow the full phased workflow.
7. **Verify everything is healthy.** Run `just gcp-cluster validate-kops-ha` — checks cluster health, InstanceGroups, zone distribution, and taints. Then explore with `just gcp-cluster k9s`. See [6. Verification & Access](#6-verification--access).

If a short step fails, do not guess at the fix: use the detailed section linked from that step, then check the [Common Pitfalls](#7-common-pitfalls) section before escalating per [Escalation](#8-escalation--when-to-call-for-help).

## 1. Prerequisites & Tooling

Before diving in, let's make sure you have everything you need. Think of this as packing your bags before a trip — missing one item can grind things to a halt later.

### 1.1 The GCP Project

You'll need a **GCP project that already exists** with billing enabled. This isn't something the repo's tooling creates — an org admin needs to set it up in the GCP console beforehand. Make a note of the `PROJECT_ID`; you'll need it for nearly every command that follows.

### 1.2 Core System Tools

These aren't managed by `asdf` — install them through your system package manager:

| Tool | Why You Need It | Check Command | Install |
|------|-----------------|--------------|---------|
| **Go** (≥1.21) | Builds the `cluster-ops` binary (`cmd/cluster-ops/main.go`) that all Justfile recipes shell out to | `go version` | [go.dev/dl](https://go.dev/dl/) |
| **just** | The task runner — every recipe in this guide (install tools, create cluster, deploy) is a `just` command | `just --version` | [github.com/casey/just](https://github.com/casey/just#installation) |
| **gcloud** | Google Cloud CLI — used by several recipes behind the scenes and handy for manual debugging | `gcloud version` | [cloud.google.com/sdk](https://cloud.google.com/sdk/docs/install) |
| **direnv** | Auto-loads environment variables from `.envrc` when you enter the project directory — no manual `export` needed | `direnv version` | [direnv.net](https://direnv.net/docs/installation.html) |
| **jq** | JSON processor used by several Justfile recipes behind the scenes | `jq --version` | [jqlang.github.io/jq](https://jqlang.github.io/jq/download/) |

### 1.3 asdf-Managed Tools

We use `asdf` to pin exact versions of the remaining tools. With `just` already installed at the system level (see above), you can use it to auto-configure everything else.

1.  **Install `asdf`**: [Installation Guide](https://asdf-vm.com/guide/getting-started.html)
2.  **Install Remaining Tools**:
    This automated target adds the required `asdf` plugins and installs the pinned versions. It includes:
    - [kubectl](https://kubernetes.io/docs/reference/kubectl/kubectl/) — CLI for cluster management.
    - [kops](https://kops.sigs.k8s.io/) — Kubernetes Operations for cluster provisioning.
    - [pulumi](https://www.pulumi.com/docs/) — Infrastructure as Code tool.
    - [velero](https://velero.io/docs/) — Disaster recovery tool.
    - [mc](https://min.io/docs/minio/linux/reference/minio-mc.html) — MinIO Client.

    ```bash
    just install-asdf-plugins
    ```

    To upgrade a specific tool version (the tool must be defined in `.tool-versions`):
    ```bash
    just install-tool <tool_name> <version>
    # Example: just install-tool kubectl 1.28.8
    ```

### 1.4 SSH Keypair for Cluster Nodes

This is easy to overlook but critical — the cluster nodes need an SSH key so you can hop onto them if something goes sideways. **No Justfile recipe generates this for you**; you must create it by hand:

```bash
ssh-keygen -t rsa -b 4096 -f credentials/k8sVM -N "" -C "kops-cluster-nodes"
```
**Expected output**:
```
Your identification has been saved in credentials/k8sVM
Your public key has been saved in credentials/k8sVM.pub
```

This creates two files: `credentials/k8sVM` (the private key — guard it carefully) and `credentials/k8sVM.pub` (the public key that gets baked into your cluster VMs). Both are gitignored, so a fresh clone of the repo always needs this step (or a secure copy from another operator).

### 1.5 Prepare the Local Repo

```bash
# Create the credentials directory if it doesn't exist (gitignored — safe for secrets)
mkdir -p credentials

# The repo should build cleanly:
go build ./...
```
**Expected output**: `go build ./...` succeeds silently (no output = no errors). If you see `go: command not found`, Go isn't installed — revisit [Section 1.2](#12-core-system-tools).

## 2. Authentication Setup

You need the **Service Account Manager** key to open the first door. Think of `sa-manager` as the master key — it has broad permissions (creating other service accounts, enabling APIs, managing IAM) so that the rest of the process can run with tighter, least-privilege credentials.

1.  **Obtain Key**: Request the `sa-manager` JSON key from the project owner.

    **If you *are* the project owner**, you can create the SA and its key yourself with one command — no browser login needed, just your active gcloud identity:

    ```bash
    just gcp-sa setup-sa-manager <PROJECT_ID>
    # key saved to credentials/sa-manager.json by default
    # optional: specify a custom path as a second argument
    ```

    This recipe does three things: creates the SA (skips if it already exists), binds all 13 manager roles, and downloads the JSON key. It works with whatever gcloud identity is active — your personal login, a named configuration, or an activated service account — so long as that identity has IAM admin privileges on the project.

    > The repo also has an older recipe (`just gcp-sa create-sa-manager`) that uses Application Default Credentials (ADC) and the Go binary, but `setup-sa-manager` is the recommended path since it stays entirely in gcloud-land.

    **If you're creating this SA manually in the GCP Console** instead of using the Justfile recipe, bind these 13 roles (all predefined, no custom roles needed):

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

    *(Source: live IAM policy for a working project, 2026-07-16.)*

2.  **Save Key**: Place it in `./credentials/sa-manager.json`.

3.  **Configure Environment**:
    ```bash
    just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/sa-manager.json"
    ```
    **Expected output**: `direnv: loading ...` followed by `direnv: export ~PATH` (exact vars vary). If you see `direnv: command not found`, install direnv — [Section 1.2](#12-core-system-tools).

    Behind the scenes, this writes `export GOOGLE_APPLICATION_CREDENTIALS=...` into `.envrc` and runs `direnv allow`. If you have `direnv`'s shell hook set up, the variable loads automatically whenever you `cd` into the project. If not, you'll need to manually `eval "$(direnv export bash)"` (or restart your shell) for it to take effect.

> **What happens next**: During cluster bootstrap, the tooling will automatically create a narrower `kops-cluster-creator` service account and rotate `GOOGLE_APPLICATION_CREDENTIALS` to point at *that* key instead. You don't need to do this yourself — the bootstrap phases (Phases 0–4 in [Section 4](#4-cluster-bootstrap)) handle both credential rotations. Just know that by the time the cluster is up, `sa-manager` is no longer in active use.

4.  **(Optional but recommended) Set up a gcloud named configuration**: Step 3 above tells the *SDKs and Go binaries* which key to use. If you also want `gcloud` CLI commands (`gcloud projects get-iam-policy`, `gsutil`, etc.) to run as `sa-manager`, create a dedicated named configuration for it:

    ```bash
    export PROJECT_ID="<your-gcp-project-id>"

    gcloud config configurations create sa-manager
    gcloud auth activate-service-account \
      sa-manager@${PROJECT_ID}.iam.gserviceaccount.com \
      --key-file=credentials/sa-manager.json
    gcloud config set project ${PROJECT_ID}
    gcloud config set compute/zone us-central1-c
    ```
    **Expected output**: `Activated service account credentials for [...]` followed by `Updated property [core/project]` and `Updated property [compute/zone]`.

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

## 3. Environment Variables — The Cluster Config File

Before the bootstrap will work, it needs to know a handful of things about your cluster: its name, where to store its state, which SSH key to use, and so on. These are defined in a **per-cluster env file** — one file per environment, following the naming convention `.env.<env>.<cluster>` (e.g., `.env.dev.my-cluster`, `.env.staging.my-cluster`, `.env.prod.my-cluster`).

The repo ships with `.env.dev.dcr-experiments` as a concrete example. You can copy it as a starting point, or create your own from scratch — either way, the seven variables below are what matter (two project-level: `PROJECT_ID` and `BUCKET_NAME`; five cluster-level: the rest).

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
| `KUBECONFIG` | Path to the kubeconfig file for this cluster | `${CLUSTER_OPS_PATH}/clusters/my-cluster/kubeconfig` |
| `SSH_KEY` | Path to the public SSH key injected into cluster nodes | `${CLUSTER_OPS_PATH}/credentials/k8sVM.pub` |
| `KUBERNETES_VERSION` | Which Kubernetes version to deploy | `1.28.8` |

> **Why `.k8s.local`?** That suffix tells kops to use gossip-based DNS — cluster nodes discover each other by chatting directly rather than through a managed DNS zone. It's simpler to set up and avoids needing a Cloud DNS zone as a prerequisite.

### 3.3 Activate Your Cluster Env File

You switch between clusters by sourcing their env file into a sub-shell — one command, no `.envrc` mutation:

```bash
just cluster-env <env> <cluster-name>
# Example: just cluster-env dev my-cluster
```
**Expected output**:
```
Cluster: my-cluster.k8s.local
State  : gs://kops-state-my-cluster/
Type 'exit' or Ctrl-D to leave this environment.
```

You're now in a sub-shell with all the cluster variables loaded. Every `just` command in this shell targets that cluster. To switch to a different cluster, `exit` back to the parent shell and run `just cluster-env` with a different env file.

```bash
just cluster-env dev my-cluster      # enter dev sub-shell
just gcp-cluster cluster-status       # hits dev
exit                                   # leave dev

just cluster-env staging my-cluster  # enter staging sub-shell
just gcp-cluster cluster-status       # hits staging
exit                                   # leave staging
```

Two terminal tabs can each activate a different cluster — no file conflicts, no stale state.

> **Legacy note**: The project also has a `CLUSTER_ENV_FILE` variable in `.envrc` that acts as a fallback default when no `cluster-env` sub-shell is active. It's kept for backward compatibility — you don't need to touch it if you're using `just cluster-env`.

### 3.4 Optional Tuning Knobs

These variables control the shape of your cluster. **Add them to the same per-cluster env file you created in Section 3.1** — the `cluster-ops` binary reads them directly from the environment via CLI flag `EnvVars` bindings in `cmd/cluster-ops/main.go`. If unset, the hardcoded defaults in that file kick in. You'll typically want smaller values for dev and larger ones for prod.

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

Now the fun begins. With your per-cluster env file ready (it has `PROJECT_ID`, `BUCKET_NAME`, the five cluster-level vars from Section 3.2, plus any optional tuning knobs from 3.4), start by setting the credential and activating the cluster:

> All commands assume `${PROJECT_ID}` and `${BUCKET_NAME}` are already in your cluster env file (Section 3.2). The credential is managed through the env file too — `cluster-cred` writes it, `cluster-env` loads it.

<a id="phase-0"></a>

### Phase 0 — Credential & cluster-env
```bash
just cluster-cred <env> <cluster-name> credentials/sa-manager.json
just cluster-env <env> <cluster-name>
```
**Expected output**: `Wrote GOOGLE_APPLICATION_CREDENTIALS=... to .env.<env>.<cluster-name>` followed by the sub-shell banner printed by `cluster-env` (see [Section 3.3](#33-activate-your-cluster-env-file)). Phases 1a through 2 run with `sa-manager`'s broad permissions.

<a id="phase-1a"></a>

### Phase 1a — Enable APIs
```bash
just gcp-api enable-apis ${PROJECT_ID} gcs-files/apis/enabled_apis.txt
```
**Expected output**: `Operation finished successfully. All required APIs are enabled for project <PROJECT_ID>.` (interspersed with recipe status messages).

<a id="phase-1b"></a>

### Phase 1b — Disable unused APIs
```bash
just gcp-api disable-apis ${PROJECT_ID} gcs-files/apis/disable_enabled_apis.txt
```
**Expected output**: `Operation finished successfully. 8 unused services have been disabled.` (interspersed with recipe status messages).

<a id="phase-2"></a>

### Phase 2 — Create kops-cluster-creator SA
```bash
just gcp-sa create-sa ${PROJECT_ID} kops-cluster-creator \
  gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  credentials/kops-cluster-creator.json
```
**Expected output**: `Key created successfully. Service account kops-cluster-creator is now ready for use.` A dedicated identity with just the 8 roles it needs (compute admin, network admin, storage admin, IAM, logging). This is least privilege in action — rather than using the all-powerful `sa-manager` forever, we mint a narrower key specifically for cluster provisioning.

<a id="phase-3"></a>

### Phase 3 — Rotate credential
```bash
just cluster-cred <env> <cluster-name> credentials/kops-cluster-creator.json
```
This updates the `GOOGLE_APPLICATION_CREDENTIALS` line in your cluster env file to point at the new `kops-cluster-creator` key. To pick it up, exit the sub-shell and re-enter:
```bash
exit
just cluster-env <env> <cluster-name>
```
From this point on, the narrower `kops-cluster-creator` key is what's used for everything. The `sa-manager` key goes dormant — and `.envrc` was never touched.

<a id="phase-4"></a>

### Phase 4 — HA cluster manifest

> **This is an HA production deployment.** The steps below provision a multi-pool, private-topology cluster with 3 control-plane nodes, dedicated etcd volumes, and three worker InstanceGroups. Every step is a single Justfile recipe backed by the `cluster-ops` binary.

<a id="phase-4a"></a>

### Phase 4a — Create state bucket

The state bucket holds your entire cluster configuration. Harden it before writing anything:
```bash
just gcp-cluster create-state-bucket ${PROJECT_ID} ${BUCKET_NAME}
```
**What it does:** Creates the bucket (skips if it already exists), then enables versioning, uniform bucket-level access, and public access prevention. **Expected output:** confirmation messages for each hardening step. If the bucket name is already taken globally, pick a different `<BUCKET_NAME>`.

<a id="phase-4b"></a>

### Phase 4b — Generate cluster manifest

This command writes the manifest to the state bucket but provisions **nothing** — it is a blueprint, not a building.

```bash
just gcp-cluster create-cluster-config ${PROJECT_ID} ${BUCKET_NAME}
```

**Under the hood**, the recipe is a thin wrapper that builds and runs `./bin/cluster-ops kops create-config --project-id <project>`. The binary reads all values from environment variables via `cmd/cluster-ops/main.go` `kopsCLIFlags()` bindings — there is no shell variable expansion in the recipe. If a variable is unset, the binary uses the hardcoded default listed in [Section 3.4](#34-optional-tuning-knobs) (e.g., `TOTAL_NODES=4`, `TOPOLOGY=public`, `NETWORKING=cilium-etcd`). For illustration, the equivalent `kops create cluster` invocation looks like:

```bash
# Illustrative — the binary constructs and executes this internally.
# Defaults shown are cmd/cluster-ops/main.go (kopsCLIFlags()) hardcoded values, NOT shell expansions.
kops create cluster \
    --name=${KOPS_CLUSTER_NAME} \
    --state=${KOPS_STATE_STORE} \
    --project=${PROJECT_ID} \
    --zones=${NODE_ZONES:-us-central1-c} \
    ${CONTROL_PLANE_ZONES:+--control-plane-zones=${CONTROL_PLANE_ZONES}} \
    --node-count=${TOTAL_NODES:-4} \
    --node-size=${NODE_MACHINE:-n1-custom-2-4096} \
    --control-plane-count=${TOTAL_MASTER:-1} \
    --control-plane-size=${MASTER_MACHINE:-n1-custom-4-8192} \
    --control-plane-volume-size=${MASTER_DISK_SIZE:-75} \
    --node-volume-size=${NODE_DISK_SIZE:-100} \
    --topology=${TOPOLOGY:-public} \
    --networking=${NETWORKING:-cilium-etcd} \
    --kubernetes-version=${KUBERNETES_VERSION} \
    --ssh-public-key=${SSH_KEY} \
    --cloud=gce \
    ${API_ACCESS_CIDR:+--admin-access=${API_ACCESS_CIDR}}
```

> **Production HA tip:** Set `TOPOLOGY=private`, `NETWORKING=cilium`, `TOTAL_MASTER=3`, and `CONTROL_PLANE_ZONES` in your cluster env file to override the binary defaults. See the production examples in [Section 3.4](#34-optional-tuning-knobs).

**Expected output:** `Created cluster "<KOPS_CLUSTER_NAME>"` — this confirms the manifest was written to GCS. No VMs exist yet.

> **What this creates:** A single default worker InstanceGroup (`nodes-<zone>`) with the flags from the env file variables `TOTAL_NODES`, `NODE_MACHINE`, and `NODE_DISK_SIZE`. The additional InstanceGroups (stateful database, batch/Spot) are configured in Phase 4d.

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

<a id="phase-4d"></a>

### Phase 4d — Configure InstanceGroups

kOps uses InstanceGroups to define node pools. Production needs three pools — each is defined as a checked-in YAML template in `config/kops/instancegroups/`. Apply them all in one command:

```bash
just gcp-cluster apply-instancegroups
```

**What it does:** Processes each `*.yaml.tmpl` template with `envsubst` (substituting values from your cluster env file), then applies them with `kops replace`. The templates and the env vars that drive them:

| Template | Env Vars | Purpose |
|----------|----------|---------|
| `config/kops/instancegroups/stateless-web.yaml.tmpl` | `STATELESS_MACHINE`, `STATELESS_MIN`, `STATELESS_MAX`, `NODE_ZONE_A/B/C` | Elastic web/API pool, 3–6 nodes across zones |
| `config/kops/instancegroups/stateful-db.yaml.tmpl` | `STATEFUL_MACHINE`, `STATEFUL_MIN`, `STATEFUL_MAX`, `NODE_ZONE_A/B/C` | Static database host pool, 3 nodes across zones |
| `config/kops/instancegroups/batch-spot.yaml.tmpl` | `BATCH_MACHINE`, `BATCH_MIN`, `BATCH_MAX`, `NODE_ZONE_A/B/C` | Elastic Spot pool with taints, 0–4 nodes |

> **The templates are checked into the repo** — edit them once to tune machine types, disk sizes, or labels, commit the changes, and every cluster provisioned from that commit gets the same configuration. The env file variables let you override sizes per environment without touching the templates.

---

**A note on idempotency**: Phases 0–3 are safe to re-run (they check if the thing already exists before creating it). Phase 4a is safe (bucket creation is idempotent). Phase 4b (`kops create cluster`) is *not* — running it against an existing cluster name will error out because a manifest with that name already lives in the state bucket.

<a id="phase-5"></a>

### Phase 5 — Apply (version-aware)

The `just gcp-cluster update-cluster` recipe delegates to `./bin/cluster-ops kops update`, which detects your installed kOps version and uses the correct command:

```bash
just gcp-cluster update-cluster
```

**What it does under the hood:**
- **kOps ≥ 1.31.0** → `kops reconcile cluster --yes` (unified reconciliation)
- **kOps ≤ 1.30.x** → `kops update cluster --yes --admin` (legacy two-step)

If you know you're on kOps ≥ 1.31, you can also use the explicit recipe:
```bash
just gcp-cluster reconcile-cluster
```

**Expected output (final lines):**
```
kOps <version> detected — using reconcile (or legacy update)
Cluster "<KOPS_CLUSTER_NAME>" is ready
```

> **This repo currently pins kOps v1.29.2** (see `.tool-versions`). All `kops` commands are dispatched through `cluster-ops`, which auto-detects the installed kOps version and uses the correct subcommand.

<a id="phase-6"></a>

### Phase 6 — Validate HA topology

A single recipe checks everything:

```bash
just gcp-cluster validate-kops-ha
```

**What it checks:** cluster health (`kops validate cluster`), InstanceGroup listing, node zone distribution (per pool), taints on batch nodes, and control-plane zone spread.

**Expected output:** All nodes `Ready`, all InstanceGroups showing correct machine types and min/max, batch nodes carrying `spot=true:NoSchedule`, and control-plane nodes spread across 3 zones.

| If you see | This indicates | Go to |
|-----------|---------------|------|
| `Error: bucket "..." already exists in a different project` | The bucket name is taken globally. Pick a different `<BUCKET_NAME>` and re-run from Phase 4a | Phase 4a |
| `Error: could not find the specified project` | The `<PROJECT_ID>` is wrong or the project doesn't exist. Double-check in the GCP console, then re-run the command | Phase 4b |
| `Error: SERVICE_ACCOUNT_NOT_FOUND` | The credential rotation didn't take effect in your sub-shell. Exit and re-enter `cluster-env`, then verify with `echo $GOOGLE_APPLICATION_CREDENTIALS` | [Section 7.1](#71-envrc-changes-dont-take-effect-35-of-issues) |
| Validation timeout (>10 minutes) | Nodes may be stuck in a boot loop. Check GCP console for instance startup errors, verify the OS image is COS or Ubuntu (not RHEL/Rocky) | [kops architecture doc: OS Image Compatibility](kops-architecture.md#33-os-image-compatibility) |
| Nodes missing from expected zones | The InstanceGroup `spec.zones` list may not match your intent. Run `kops get instancegroups` and verify | Phase 4d |
| `kops reconcile` returns `unknown command` | You are on kOps ≤ 1.30.x — use the legacy `kops update cluster --yes --admin` path instead | [Section 5.2](#52-version-command-matrix) |

<a id="phase-7"></a>

### Phase 7 — Post-Provisioning Hardening

After the cluster is provisioned and validated, verify that the reliability components enabled in Phase 4c are running correctly:

- **Cluster Autoscaler** (`spec.clusterAutoscaler.enabled: true` in Phase 4c) — auto-scales elastic InstanceGroups
- **Node Problem Detector** (`spec.nodeProblemDetector.enabled: true` in Phase 4c) — monitors kernel logs, taints unhealthy nodes
- **cert-manager** (`spec.certManager.enabled: true` in Phase 4c) — handles x509 certificate provisioning and rotation
- **Node local DNS cache** (`spec.kubeDNS.nodeLocalDNS.enabled: true` in Phase 4c) — per-node DNS caching DaemonSet, reduces DNS latency and CoreDNS load
- **Metrics Server** — automatically enabled by kOps when Cluster Autoscaler is active

**Verify all three are running:**

```bash
# Cluster Autoscaler should be running as a pod in kube-system
kubectl get pods -n kube-system -l app=cluster-autoscaler

# Node Problem Detector should be running as a DaemonSet
kubectl get daemonset node-problem-detector -n kube-system

# Metrics Server should be running and reporting
kubectl top nodes

# cert-manager should be running
kubectl get pods -n cert-manager

# Node local DNS cache should be running on every node
kubectl get pods -n kube-system -l k8s-app=node-local-dns
```

**Expected output:**
- Cluster Autoscaler: 1 pod `Running` (it's a single-replica deployment, not a DaemonSet)
- Node Problem Detector: DaemonSet with 1 pod per node, all `Running`
- Metrics Server: `kubectl top nodes` shows CPU and memory for all nodes (no `error: metrics not available yet`)
- cert-manager: pods `cert-manager`, `cert-manager-cainjector`, and `cert-manager-webhook` all `Running` in the `cert-manager` namespace
- Node local DNS cache: one `node-local-dns` pod per node, all `Running` in `kube-system`

**Verify autoscaler surge capacity:**

Elastic InstanceGroups should show maxSize > minSize, giving the autoscaler room to provision replacement nodes:
```bash
just gcp-cluster list-instancegroups
```
**Expected:** `stateless-web` shows `Min: 3, Max: 6` and `batch-spot` shows `Min: 0, Max: 4`. The autoscaler will scale these pools when pods are pending due to resource pressure — including when a node fails and its pods need a new home.

| If you see | This indicates | Go to |
|-----------|---------------|------|
| `kubectl top nodes` returns `error: metrics not available yet` | Metrics Server pod is not running or hasn't collected initial metrics. Wait 60 seconds and retry, then check `kubectl get pods -n kube-system \| grep metrics-server` | Phase 7 |
| `kubectl get pods -n kube-system -l app=cluster-autoscaler` returns `No resources found` | Cluster Autoscaler is not deployed. Check `kops get cluster -o yaml` for `clusterAutoscaler.enabled: true` | Phase 4c |
| Node Problem Detector pods are in `CrashLoopBackOff` | Usually a kernel log path issue on the host OS image. Verify the OS is COS or Ubuntu, not RHEL/Rocky | [kops architecture doc: OS Image Compatibility](kops-architecture.md#33-os-image-compatibility) |

## 5. Production HA Deployment Reference

This section cross-references which kOps settings control which architectural component, the version-specific command matrix, and the current state of automation (what's implemented, what's still manual, and remaining gaps).

### 5.1 Which Setting Controls Which Pool

| Pool | kOps `create cluster` Flag | InstanceGroup Field(s) | Env Var Override |
|------|---------------------------|----------------------|------------------|
| **Control plane** | `--control-plane-count`, `--control-plane-size`, `--control-plane-volume-size`, `--control-plane-zones` | Managed via `spec.controlPlane.kubelet`; separate IGs auto-created per zone. Examples shown here — actual defaults in [Section 3.4](#34-optional-tuning-knobs) | `TOTAL_MASTER`, `MASTER_MACHINE`, `MASTER_DISK_SIZE`, `CONTROL_PLANE_ZONES` |
| **Stateless web (default workers)** | `--node-count=3`, `--node-size=e2-standard-4`, `--node-volume-size=100` | `spec.machineType`, `spec.minSize`, `spec.maxSize`, `spec.zones` | `TOTAL_NODES`, `NODE_MACHINE`, `NODE_DISK_SIZE` |
| **Stateful database** | *(not created by `kops create cluster`)* | Created via `kops create instancegroup`. Set `spec.machineType: n2-standard-4`, `spec.minSize: 3`, `spec.maxSize: 3`, `spec.nodeLabels.pool: database` | None — manual only |
| **Batch/Spot** | *(not created by `kops create cluster`)* | Created via `kops create instancegroup`. Set `spec.machineType: e2-standard-4`, `spec.minSize: 0`, `spec.maxSize: 4`, `spec.taints[0]: spot:true:NoSchedule`, `spec.gce.preemptible: true` | None — manual only |
| **etcd volumes** | *(not a flag)* | `spec.etcdClusters[0].etcdMembers[*].volumeType: pd-ssd`, `spec.etcdClusters[1].etcdMembers[*].volumeType: pd-ssd` | None — manifest only |
| **Private topology** | `--topology=private` | `spec.topology.masters: private`, `spec.topology.nodes: private` | None — flag/manifest only |
| **API CIDR restriction** | *(not a flag)* | `spec.kubernetesApiAccess: ["<cidr>/32"]` | None — manifest only |
| **Networking / CNI** | `--networking=cilium` | `spec.networking.cilium` | None — flag only |
| **Cluster Autoscaler** | *(not a flag)* | `spec.clusterAutoscaler.enabled: true` — auto-provisions the autoscaler pod with GCE MIG permissions. Uses InstanceGroup `minSize`/`maxSize` for scaling bounds | None — manifest only |
| **Node Problem Detector** | *(not a flag)* | `spec.nodeProblemDetector.enabled: true` — deploys as a DaemonSet, monitors kernel logs, taints unhealthy nodes | None — manifest only |
| **Metrics Server** | *(not a flag)* | `spec.metricsServer.enabled: true` — enables `kubectl top` and HPA. Automatically enabled when Cluster Autoscaler is active | None — manifest only |
| **cert-manager** | *(not a flag)* | `spec.certManager.enabled: true` — handles x509 certificates. One installation per cluster. Set `spec.certManager.managed: false` if running externally | None — manifest only |
| **Node local DNS cache** | *(not a flag)* | `spec.kubeDNS.nodeLocalDNS.enabled: true` — per-node DNS caching DaemonSet. Reduces DNS latency and CoreDNS load | None — manifest only |

### 5.2 Version Command Matrix

| Action | kOps ≤ 1.30.x (current: v1.29.2) | kOps ≥ 1.31.0 |
|--------|----------------------------------|---------------|
| Create manifest | `kops create cluster ...` | `kops create cluster ...` |
| Edit manifest | `kops edit cluster ...` | `kops edit cluster ...` |
| Apply changes | `kops update cluster --yes --admin` | `kops reconcile cluster --yes` |
| Rolling update (if needed) | `kops rolling-update cluster --yes` | *(included in `reconcile`)* |
| Validate health | `kops validate cluster --wait 10m` | `kops validate cluster --wait 10m` |
| Justfile recipe | `just gcp-cluster update-cluster` | `just gcp-cluster update-cluster` (version-aware) or `just gcp-cluster reconcile-cluster` (explicit) |

> **Warning:** Do not mix workflows. If you create a cluster with kOps 1.31+, always use `kops reconcile`. If you create with kOps 1.29/1.30, always use `kops update cluster`. The `kops reconcile` command replaces both `update` and `rolling-update` for v1.31+.

### 5.3 Rollback

> **Before taking destructive action:** Check the cluster's current role in
> your environment. If this is a production cluster serving live traffic,
> coordinate the rollback with the platform-lead. Verify that no dependent
> services (Pulumi stacks, CI/CD pipelines, monitoring) are actively using
> the cluster. For SLA-governed environments, confirm the current uptime
> budget allows for a full cluster teardown and recreate before proceeding.

If the apply step creates an unhealthy cluster, destroy it and recreate:

```bash
kops delete cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE} --yes
```
**Expected output:** `Deleted cluster: "<KOPS_CLUSTER_NAME>"` — this destroys all GCE resources. The state bucket and its versioned history remain. You can then re-run from Phase 4b.

For a full teardown and re-creation workflow — including dry-run previews, preflight checks, cleanup verification, and cost considerations — see [Section 9: Disposable Cluster Lifecycle](#9-disposable-cluster-lifecycle).

### 5.4 Automation Summary

**Implemented recipes** (available now):

| Recipe | Covers |
|--------|--------|
| `just gcp-cluster create-state-bucket <project> <bucket>` | Phase 4a — create and harden GCS bucket |
| `just gcp-cluster create-cluster-config <project> <bucket>` | Phase 4b — generate HA manifest from env vars |
| `just gcp-cluster apply-instancegroups` | Phase 4d — apply checked-in InstanceGroup templates |
| `just gcp-cluster update-cluster` | Phase 5 — version-aware apply (auto-detects kOps version) |
| `just gcp-cluster reconcile-cluster` | `cluster-ops kops update` (same version-aware dispatch as `update-cluster`) |
| `just gcp-cluster validate-kops-ha` | Phase 6 — HA topology validation |
| `just gcp-cluster list-instancegroups` | Inspection — list all InstanceGroups |
| `just gcp-cluster delete-cluster` | Section 9 — dry-run teardown preview (safe, no changes) |
| `just gcp-cluster delete-cluster yes` | Section 9 — full teardown with preflight, confirmation, and cleanup verification |
| `just gcp-cluster save-cluster-manifest` | Section 9 — export live cluster manifest as version-controlled YAML |
| `just gcp-cluster diff-cluster-manifest` | Section 9 — diff live manifest against saved copy (guides Phase 4c edits) |
| `just gcp-cluster recreate-cluster` | Section 9 — full re-creation pipeline (create-config → diff → edit → apply-ig → update → validate) |

**Still manual** (editing is unavoidable):

| Step | Why Manual |
|------|-----------|
| Phase 4c — `just gcp-cluster edit-cluster` | Opens `$EDITOR` on the cluster manifest. You must review and set `kubernetesApiAccess`, etcd volume types, and the node service account. Human judgment is required. |
| InstanceGroup template authoring | The YAML templates in `config/kops/instancegroups/` ship with sensible defaults (3 nodes, e2-standard-4, etc.). Edit them once for your environment, commit, and the recipe applies them automatically from then on. |

**Remaining gap** (if you upgrade to kOps ≥ 1.31):

| Gap | Recommendation |
|-----|---------------|
| The `cluster-ops create-config` path creates a single default worker pool | For the three-pool HA architecture, the InstanceGroup templates handle the additional pools. `cluster-ops kops create-config` creates the base cluster + default pool; `cluster-ops ig apply` adds the rest. |

> All recipes delegate to the `cluster-ops` binary (`./bin/cluster-ops`). The legacy `create-kops-cluster` recipe has been removed — use the phased HA workflow in Section 4 for production, or `create-cluster-config` + `update-cluster` for single-pool dev clusters.

## 6. Verification & Access

Once the bootstrap command completes, here's how to confirm everything is healthy and get hands-on access.

### 6.1 Sanity Checks (Per Phase)

> These commands assume you're inside a `cluster-env` sub-shell, so `${PROJECT_ID}`, `${BUCKET_NAME}`, and the other cluster vars are already available from your env file. If you exited the sub-shell and came back, just run `just cluster-env <env> <cluster-name>` again.

You can run these manually for peace of mind after each phase:

*   **After Phase 1 (APIs enabled)**: Confirm all required services are live:
    ```bash
    just gcp-api list-enabled-apis ${PROJECT_ID} /tmp/enabled-check.txt
    diff <(sort gcs-files/apis/enabled_apis.txt) <(sort /tmp/enabled-check.txt)
    ```
    **Expected**: `diff` produces no output — every required API is enabled. If APIs are missing, re-run Phase 1a (idempotent). If the gap persists beyond 2 minutes, escalate per [Section 8](#8-escalation--when-to-call-for-help).

*   **After Phase 2 (SA created)**: Confirm the key file exists with tight permissions:
    ```bash
    ls -la credentials/kops-cluster-creator.json
    ```
    **Expected**: file exists with mode `0600`. If missing, re-run Phase 2 (idempotent — it checks if the SA already exists before creating).

*   **After Phase 3 (credential rotated)**: Confirm the active credential points at the new key. Since you exited and re-entered `cluster-env`, just check your shell:
    ```bash
    echo $GOOGLE_APPLICATION_CREDENTIALS
    ```
    **Expected**: path ends with `credentials/kops-cluster-creator.json`. If it still shows `sa-manager.json`, you likely forgot to `exit` and re-run `cluster-env` after `cluster-cred`.

*   **After Phase 4 (bucket + kops create)**: Confirm the state bucket exists and the cluster manifest was written:
    ```bash
    gsutil ls -L -b gs://${BUCKET_NAME} | head -5
    kops get cluster --state $KOPS_STATE_STORE
    ```
    **Expected**: bucket shows versioning enabled and a lifecycle rule; `kops get cluster` lists your new cluster. If the cluster doesn't appear, check that `KOPS_STATE_STORE` and `KOPS_CLUSTER_NAME` are set — see [Section 7.2](#72-missing-kops_cluster_name--ssh_key--kubernetes_version-25-of-issues).

### 6.2 Cluster Health

1.  **Validate Health**:
    ```bash
    just gcp-cluster validate-cluster
    ```
    **Expected healthy output**:
    ```
    Validating cluster my-cluster.k8s.local
    ...
    Your cluster my-cluster.k8s.local is ready
    ```
    followed by a list of nodes all showing `Ready` status.

    **If validation fails**: You'll see something like `Validation failed: ...` with specifics about which component is unhealthy. Common causes: nodes still booting (wait 2–3 minutes and retry), or networking issues — check the `.k8s.local` suffix in [Section 7.5](#75-wrong-kops_cluster_name-suffix-8-of-issues) (wrong suffix breaks gossip DNS) and verify GCP firewall rules aren't blocking inter-node traffic (`gcloud compute firewall-rules list --project=${PROJECT_ID}`).

2.  **Status Overview**:
    ```bash
    just gcp-cluster cluster-status
    ```
    **Expected output**: A summary of cluster components (control plane, nodes, system pods) all reporting `Healthy` or `Ready`.

### 6.3 Getting Inside the Cluster

3.  **Interactive Dashboard** (recommended for exploration):
    ```bash
    just gcp-cluster k9s
    ```

4.  **Export Kubeconfig** (if you need the file for another tool):
    ```bash
    just gcp-cluster export-kubeconfig
    # OR for a specific duration/name
    just gcp-cluster export-named-kubeconfig <NAME> <HOURS>
    ```


## 7. Common Pitfalls

These are the stumbling blocks that catch people most often. If something isn't working, start here before digging deeper.

### 7.1 ".envrc Changes Don't Take Effect" (~35% of issues)

`just set-env-var` updates `.envrc` and runs `direnv allow`, but your *current shell* might not pick up the change. If a command fails with what looks like a missing credential error, try:
```bash
eval "$(direnv export bash)"
# then verify:
env | grep GOOGLE_APPLICATION_CREDENTIALS
```
**Expected if fixed**: The `env | grep` line shows `GOOGLE_APPLICATION_CREDENTIALS=/path/to/credentials/...`.
**If it doesn't**: You may need to restart your shell or check that direnv's hook is installed (`direnv hook bash`).

This is especially important right after the credential rotation (Phase 3 of bootstrap) — the Go code reads `GOOGLE_APPLICATION_CREDENTIALS` directly from the environment, not from the `.envrc` file.

### 7.2 "Missing KOPS_CLUSTER_NAME / SSH_KEY / KUBERNETES_VERSION" (~25% of issues)

These errors come from the bucket-creation step. They mean the per-cluster env file wasn't loaded into your shell — you're either not in a `cluster-env` sub-shell, or the env file itself is missing those variables. Double-check:
```bash
env | grep -E 'KOPS_CLUSTER_NAME|KOPS_STATE_STORE|SSH_KEY|KUBERNETES_VERSION|KUBECONFIG'
```
**Expected if correct**: All five variables show non-empty values.
**If any are empty or missing**: Run `just cluster-env <env> <cluster>` to re-activate the cluster, or if you haven't created the env file yet, revisit [Section 3.1](#31-create-your-cluster-env-file).

### 7.3 Re-running Against an Existing Cluster (~15% of issues)

`kops create cluster` (Phase 4) is not idempotent — if a cluster with the same name already exists in the state bucket, it will error with something like:
```
Error: cluster "my-cluster.k8s.local" already exists
```
If you need to re-create: either use a new cluster name, or delete the existing cluster first:
```bash
kops delete cluster --name $KOPS_CLUSTER_NAME --state $KOPS_STATE_STORE --yes
```
**Expected output**: `Deleted cluster: "my-cluster.k8s.local"` — after this you can re-run the bootstrap from Phase 4a.
**WARNING**: This destroys all GCE resources associated with the cluster. Only do this if you're sure.

### 7.4 Forgot the SSH Keypair (~10% of issues)

If `kops create cluster` complains about the SSH key, double-check you've run the manual `ssh-keygen` command from [Section 1.4](#14-ssh-keypair-for-cluster-nodes). A fresh clone of the repo won't have these files. No Justfile recipe creates them for you.

Check:
```bash
ls -la credentials/k8sVM.pub
```
**Expected if present**: Shows the file with a recent timestamp. **If missing**: Run the `ssh-keygen` command from Section 1.4.

### 7.5 Wrong KOPS_CLUSTER_NAME Suffix (~8% of issues)

If the name doesn't end in `.k8s.local`, kops will expect a real DNS zone to exist (Cloud DNS), which this repo's tooling doesn't set up.

**Check your suffix**:
```bash
echo $KOPS_CLUSTER_NAME | grep -q '\.k8s\.local$' && echo 'OK: suffix correct' || echo 'FAIL: name must end in .k8s.local'
```
**If it fails**: Update your per-cluster env file (`KOPS_CLUSTER_NAME=my-cluster.k8s.local`), then reload:
```bash
just set-env-var KOPS_CLUSTER_NAME "my-cluster.k8s.local"
eval "$(direnv export bash)"
echo $KOPS_CLUSTER_NAME   # should now end in .k8s.local
```
Stick with `.k8s.local` for gossip-based DNS unless you've explicitly configured a Cloud DNS zone.

### 7.6 "Service Not Enabled" During SA Creation (~7% of issues)

If Phase 2 (creating the kops-cluster-creator service account) fails with a message about services not being enabled, it's usually a timing issue — the API enablement from Phase 1 hasn't fully propagated yet. The error looks something like:
```
Error: Service iam.googleapis.com is not enabled for project <PROJECT_ID>
```
Wait 10–15 seconds and re-run (the recipe is safe to re-run since it checks if the SA already exists).

**Verify the APIs are now active before retrying**:
```bash
just gcp-api list-enabled-apis ${PROJECT_ID} /tmp/enabled-check.txt
diff <(sort gcs-files/apis/enabled_apis.txt) <(sort /tmp/enabled-check.txt)
```
**Expected output**: `diff` produces no output — that means every required API is enabled. If APIs are still missing from the enabled list, wait another 15 seconds and check again. If the problem persists beyond 2 minutes, escalate per [Section 8](#8-escalation--when-to-call-for-help).

## 8. Escalation — When to Call for Help

Not every problem can (or should) be solved solo. Escalate to the **platform-lead** immediately if ANY of these happen:

| Condition | Why It's an Escalation |
|-----------|----------------------|
| The cluster bootstrap fails after **3 retries** with the same error | Indicates a systemic issue, not a transient glitch |
| `just gcp-cluster validate-cluster` does **not** return healthy within **15 minutes** of `kops update cluster` | Nodes may be stuck in a boot loop or there's a networking misconfiguration |
| You see IAM or permission errors mentioning `sa-manager` | The master key may be expired, revoked, or have insufficient roles — only the project owner can fix this |
| You see billing-related errors (e.g., "billing must be enabled") | The GCP project's billing account needs attention from an org admin |
| Any error message includes "quota" or "limit exceeded" | You've hit a GCP resource quota that needs a quota increase request |

**When escalating, have this ready** (paste it, don't describe it):
- The exact command you ran (copy-pasted from your terminal)
- The full error output (copy-pasted, not summarized)
- Which phase of the bootstrap was running (see [Section 4](#4-cluster-bootstrap))
- Output of: `env | grep -E 'GOOGLE_APPLICATION_CREDENTIALS|KOPS_CLUSTER_NAME|KOPS_STATE_STORE'``
- Output of: `gcloud auth list --filter=status:ACTIVE`

## 9. Disposable Cluster Lifecycle

This guide assumes you want a long-lived cluster. But sometimes you want the opposite — spin it up when work begins, tear it down when the work is done, and repeat whenever you need it again. Think of it as an on-demand, disposable cluster: no idle compute costs, no stale state from long-running drift.

### 9.1 Mindset: Cluster as Cattle

A disposable cluster is cattle, not a pet. You don't nurse it back to health — you destroy it and create a fresh one from the same blueprint. The blueprint (your env file, InstanceGroup templates, service account keys, and state bucket) is the durable artifact. The running GCE instances are transient.

This works because kOps stores the entire cluster specification in the GCS state bucket. So long as that bucket (with versioning enabled) and your per-cluster env file survive, you can recreate the cluster from the same starting point.

> **⚠️ Reproducibility gap — Phase 4c is manual.** The `create-cluster-config` recipe (Phase 4b) generates the cluster manifest from your env file. But Phase 4c (`kops edit cluster`) is a manual step that requires human judgment — you set `kubernetesApiAccess`, etcd volume types, node service account, and addon toggles. These edits are **not** captured in the env file or templates. To close this gap and make the cluster fully reproducible, export the edited manifest after your first successful bootstrap:
>
> ```bash
> kops get cluster -o yaml > config/kops/cluster-manifest.yaml
> ```
>
> Check this file into the repo. On re-creation, review it against the freshly generated manifest (`kops get cluster -o yaml` after Phase 4b) and re-apply the same security edits. This turns a manual review into a diff-check.

### 9.2 What Survives Teardown

| Artifact | Survives? | Why |
|----------|-----------|-----|
| GCS state bucket (`gs://${BUCKET_NAME}`) | ✅ Yes | kOps delete does **not** touch the state bucket. The versioned object history is preserved. |
| Per-cluster env file (`.env.<env>.<cluster-name>`) | ✅ Yes | A local file in the repo — never touched by teardown. |
| SSH keypair (`credentials/k8sVM*`) | ✅ Yes | Local files. Reused across create/destroy cycles. |
| Service account keys (`credentials/sa-manager.json`, `credentials/kops-cluster-creator.json`) | ✅ Yes | Local files. The SAs themselves live in the GCP project and are never deleted. |
| GCP project + enabled APIs | ✅ Yes | Teardown only removes cluster resources, not the project or its API enablement. |
| InstanceGroup templates (`config/kops/instancegroups/*.yaml.tmpl`) | ✅ Yes | Checked into the repo. |
| **GCE instances** (control-plane + workers) | ❌ Destroyed | These are the cluster. |
| **Boot disks + kOps-managed etcd volumes** | ❌ Destroyed | Deleted with the instances. |
| **Workload PersistentVolumes** (PVCs) | ⚠️ Depends | If the StorageClass reclaim policy is `Delete`, the PV is destroyed with the cluster. If `Retain`, the backing disk survives as an orphaned resource — it continues to accrue charges until manually deleted. Verify reclaim policies before teardown: `kubectl get sc` and `kubectl get pv`. |
| **Load balancers, forwarding rules, target pools** | ❌ Destroyed | kOps-created networking resources. |
| **Firewall rules** (kOps-managed) | ❌ Destroyed | Only the rules kOps created; project-wide rules are untouched. |
| **VPC network / subnets** | ⚠️ Depends | If kOps created the VPC (e.g., you used `--topology=private` with no pre-existing network), it is destroyed. If you pointed kOps at a pre-existing shared VPC, that network survives and is not modified. |

### 9.3 Teardown — Destroy the Cluster

**Step 1: Preflight checks.** Before destroying anything, confirm you're targeting the right cluster and that no dependent services will break:

```bash
# Are you in a cluster-env sub-shell targeting the right cluster?
echo "About to destroy: ${KOPS_CLUSTER_NAME}"
echo "State bucket:      ${KOPS_STATE_STORE}"
echo "GCP project:       ${PROJECT_ID}"

# Any workloads still running?
kubectl get pods --all-namespaces 2>/dev/null || echo "(no cluster access — already down?)"

# Any Pulumi stacks still deployed against this cluster?
# Check your Pulumi stacks manually — there's no automated discovery for this yet.
# See Section 9.7 for the Pulumi teardown caveat.

# Any Velero backups you should take first?
# See Section 9.7 for the PV data caveat.
```

If everything looks correct, proceed.

**Step 2: Dry-run (no `--yes`).** Always preview what kOps will destroy before committing:

```bash
kops delete cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE}
```

**Expected output:** kOps lists every resource it plans to delete — GCE instances, disks, forwarding rules, firewall rules, and the cluster manifest itself. Review this list carefully. If it references resources you don't recognize or lists an unexpected count, stop and investigate.

**Step 3: Execute.** Only after confirming the dry-run output matches expectations:

```bash
kops delete cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE} --yes
```

**Expected output:**
```
Deleted cluster: "<KOPS_CLUSTER_NAME>"
```

**How long it takes:** Typically a few minutes, depending on the number of instances and networking resources. kOps tears down GCE instances first, then cleans up disks, forwarding rules, and firewall rules.

**What it leaves behind:**
- The GCS state bucket — kOps removes the active cluster definition from the bucket but leaves the bucket and its versioned object history intact.
- The kubeconfig file at `${KUBECONFIG}` — now pointing at a dead cluster. Delete it or leave it; it'll be overwritten on re-creation.

> **Justfile recipes are available.** Instead of running the raw `kops` command, use `just gcp-cluster delete-cluster` (dry-run) and `just gcp-cluster delete-cluster yes` (full teardown with preflight checks and cleanup verification). See [Section 9.6](#96-the-full-cycle-recipe) for the complete workflow.

**Step 4: Verify cleanup.** After teardown, confirm no resources from the cluster remain. Use broad inventory checks rather than name-pattern filters (kOps naming conventions can vary):

```bash
# Instances — should show none matching the cluster's project/zone
gcloud compute instances list --project=${PROJECT_ID} \
  --filter="name:${KOPS_CLUSTER_NAME}"
# Expected: Listed 0 items.

# Disks — check for orphaned PV disks (especially if reclaim policy is Retain)
gcloud compute disks list --project=${PROJECT_ID} \
  --filter="name:${KOPS_CLUSTER_NAME}"
# Expected: Listed 0 items. If disks remain, they may be orphaned PVs — delete
# them manually with `gcloud compute disks delete <name>`.

# Forwarding rules — broad check, review manually
gcloud compute forwarding-rules list --project=${PROJECT_ID}
# Confirm no rules reference the deleted cluster's name or IP.

# Addresses — check for orphaned static IPs
gcloud compute addresses list --project=${PROJECT_ID}
# Release any that belonged to the cluster with `gcloud compute addresses delete <name>`.

# Target pools / load balancer components
gcloud compute target-pools list --project=${PROJECT_ID}
gcloud compute url-maps list --project=${PROJECT_ID}
# Review manually; delete any that belonged to the cluster.
```

### 9.4 Optional: Delete the State Bucket

If you want a completely clean slate — no leftover cluster definition, no versioned history — delete the bucket as well:

```bash
gsutil rm -r gs://${BUCKET_NAME}
```

**Only do this if** you're done with this cluster permanently (you won't need its manifest history) or you're troubleshooting a corrupted state bucket. Otherwise, keeping the bucket costs very little and preserves the ability to recreate.

If you delete the bucket and later want to recreate, you must re-run Phase 4a (`just gcp-cluster create-state-bucket`) before Phase 4b.

> **⚠️ Do not use `gsutil rm -r -a` (delete all archived versions) as routine cleanup.** That command permanently deletes the versioned history of every object in the bucket — it is irreversible. The versioned history is your recovery mechanism if a bad manifest edit corrupts the cluster definition. Use it only when you are certain you no longer need that history and understand the cost. For routine bucket maintenance, use object lifecycle rules instead (see [GCS lifecycle documentation](https://cloud.google.com/storage/docs/lifecycle)).

### 9.5 Re-Creation — Bring It Back

After teardown, bringing the cluster back skips the heavy one-time setup. The GCP project already exists, APIs are enabled, service accounts and their keys are in place, the SSH keypair is still in `credentials/`, and your per-cluster env file is untouched.

**Re-creation checklist** (assuming the state bucket was **not** deleted):

1. **Activate the cluster env** (you're back in a fresh terminal):
   ```bash
   just cluster-env <env> <cluster-name>
   ```

2. **Regenerate the cluster manifest** (Phase 4b):
   ```bash
   just gcp-cluster create-cluster-config ${PROJECT_ID} ${BUCKET_NAME}
   ```
   > ⚠️ If you get `Error: cluster "..." already exists`, the old cluster definition is still in the state bucket. kOps `delete cluster --yes` removes it, but if something went wrong during the previous teardown, clear it manually: `kops delete cluster --name=${KOPS_CLUSTER_NAME} --state=${KOPS_STATE_STORE} --yes` (this time it only removes the definition since there are no live resources).

3. **Edit the manifest** (Phase 4c):
   ```bash
   just gcp-cluster edit-cluster
   ```
   Re-apply the same security settings: `kubernetesApiAccess`, etcd volume types (`pd-ssd`), node service account, Cluster Autoscaler, Node Problem Detector, cert-manager, and node-local DNS. If you saved and checked in the manifest (see the reproducibility gap note in [Section 9.1](#91-mindset-cluster-as-cattle)), export the fresh manifest and diff it against your saved copy to confirm you're applying the same edits:
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
   kubectl get pods -n kube-system -l app=cluster-autoscaler
   kubectl get daemonset node-problem-detector -n kube-system
   kubectl top nodes
   kubectl get pods -n cert-manager
   kubectl get pods -n kube-system -l k8s-app=node-local-dns
   ```

**Re-creation time:** Primarily GCE instance boot and health checks, plus the time it takes to review and re-apply manifest edits in Phase 4c. A first-time full bootstrap (Sections 1–4) involves tool installation, API enablement, and SA creation in addition to the cluster provisioning steps above.

### 9.6 The Full Cycle (Recipe)

Here is the complete create → use → destroy → recreate workflow, using the Justfile recipes:

```bash
# ─── FIRST TIME (one-time setup) ───
# Follow Sections 1–4 in full. This gets you:
#   - Tools installed (Section 1)
#   - sa-manager key in place (Section 2)
#   - Per-cluster env file created (Section 3)
#   - APIs enabled, SAs created, cluster bootstrapped (Section 4)
#
# Optional but recommended: after your first successful bootstrap,
# save the edited manifest so re-creation is a diff-check, not a full edit:
#   just gcp-cluster save-cluster-manifest
#   git add config/kops/cluster-manifest.yaml && git commit -m "save known-good cluster manifest"

# ─── USE THE CLUSTER ───
# Deploy apps, run workloads, do whatever you need.

# ─── TEARDOWN ───
just cluster-env <env> <cluster-name>

# Step 1: Dry-run — preview what will be destroyed (safe, no changes)
just gcp-cluster delete-cluster

# Step 2: Execute — full teardown with confirmation prompt
just gcp-cluster delete-cluster yes
#  → Type 'destroy' at the prompt to confirm
#  → Cleanup verification runs automatically
exit  # leave the sub-shell

# ─── RECREATE WHEN NEEDED AGAIN ───
just cluster-env <env> <cluster-name>

# One command runs the full 6-step pipeline:
#   create-config → diff manifest → edit (manual) → apply-ig → update → validate
just gcp-cluster recreate-cluster
#  → Step 3 opens your $EDITOR for Phase 4c security settings — this is the
#    only manual step. If you saved the manifest, the diff in Step 2 shows
#    you exactly what edits to re-apply.
exit

# ─── TEARDOWN AGAIN ───
# (same teardown sequence as above)

# ... repeat as needed ...
```

**What each recipe does:**

| Recipe | Purpose |
|--------|---------|
| `just gcp-cluster delete-cluster` | Dry-run — shows what kOps will destroy. Safe to run anytime. |
| `just gcp-cluster delete-cluster yes` | Full teardown: preflight → confirmation prompt → destroy → cleanup verification (instances, disks, forwarding rules, addresses). |
| `just gcp-cluster save-cluster-manifest` | Exports `kops get cluster -o yaml` to `config/kops/cluster-manifest.yaml`. Commit this file for version-controlled reproducibility. |
| `just gcp-cluster diff-cluster-manifest` | Diffs the live cluster manifest against the saved copy. Exits non-zero when differences exist — review the diff to know what Phase 4c edits to re-apply. |
| `just gcp-cluster recreate-cluster` | Full re-creation pipeline: runs create-config → diff against saved manifest → opens editor for Phase 4c → apply-instancegroups → update-cluster → validate-kops-ha. |

**Cost model:** With this pattern, you pay for GCE instances only while the cluster is up. Shutting down when not in use eliminates idle compute costs entirely. The GCS state bucket costs very little per month. The main trade-off is the re-creation time (mostly instance boot + manifest review) vs. the cost of keeping instances running 24/7.

### 9.7 Caveats

**Workload PersistentVolumes may survive teardown.** kOps-managed disks (boot volumes, etcd) are always destroyed. But workload PVCs backed by GCE persistent disks with a `Retain` reclaim policy are **not** automatically deleted — they become orphaned and continue accruing charges. Check before teardown:
```bash
kubectl get sc   # check StorageClass reclaim policies
kubectl get pv   # list all PVs and their status
```
After teardown, run the disk inventory check from Section 9.3 Step 4 to catch any orphans. If you have valuable data on PVCs, back them up with Velero before tearing down (see `just gcp-cluster setup-cluster-backup`).

**External DNS records break.** If you configured a real DNS zone (not `.k8s.local` gossip), the A records pointing at the API load balancer go stale after teardown. Re-creation gets a new load balancer IP — you'll need to update DNS.

**Pulumi stacks tied to the cluster.** If you deployed applications via Pulumi (see [`pulumi-setup.md`](pulumi-setup.md)), tear those down before destroying the cluster — otherwise the Pulumi state references a cluster that no longer exists:
```bash
# The actual recipe takes <folder> and <stack> arguments:
just gcp-pulumi remove-resource <folder> <stack>
```

**State bucket versioned history is recovery, not a blueprint.** Each `create-cluster-config` + `update-cluster` cycle writes new manifest versions. Over many cycles, old non-current versions accumulate (storage charges apply). Do **not** use `gsutil rm -r -a` to clean them — that permanently deletes all versioned history and removes your recovery mechanism. Instead, use GCS object lifecycle rules to automatically expire old versions after a retention period (e.g., keep 30 days of history). See the [GCS lifecycle documentation](https://cloud.google.com/storage/docs/lifecycle) for setup.

**kOps version drift between cycles.** If you upgrade kOps between teardown and re-creation, the manifest flags or behavior might change. Always check the [kOps release notes](https://kops.sigs.k8s.io/releases/) for breaking changes before recreating. The `.tool-versions` file pins the kOps version — run `just install-asdf-plugins` to sync before recreating.

**GCP project quotas.** Each create/destroy cycle briefly consumes quota for instances during creation, but quota is fully released on teardown. If you cycle frequently (multiple times a day), watch for rate-limiting on the GCE API — kOps makes many sequential API calls during creation, and GCP may throttle if you're creating and deleting in tight loops.

## 10. Next Steps

Your cluster is up and validated. Where to go from here:

- **Deploy applications** — Follow [`docs/pulumi-setup.md`](pulumi-setup.md) to deploy the Pulumi-managed application stack on top of your new cluster.
- **Understand the architecture** — Read [`docs/kops-architecture.md`](kops-architecture.md) for the full architectural rationale, failure modes, and design decisions behind the HA topology, InstanceGroup layout, and networking choices.
- **Set up backups** — Run `just gcp-cluster setup-cluster-backup` to configure Velero for disaster recovery.
- **Explore the cluster** — Run `just gcp-cluster k9s` for an interactive terminal dashboard, or `kubectl get all --all-namespaces` for a quick inventory.
- **Tear it down when done** — See [Section 9: Disposable Cluster Lifecycle](#9-disposable-cluster-lifecycle) to destroy the cluster when it's no longer needed and recreate it later.
