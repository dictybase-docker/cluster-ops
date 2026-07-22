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
  - [1.4 SSH Keypair and Local Repo](#14-ssh-keypair-and-local-repo)
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
- [Appendix A — Production HA Deployment Reference](#appendix-a--production-ha-deployment-reference)
  - [A.1 Which Setting Controls Which Pool](#a1-which-setting-controls-which-pool)
  - [A.2 Version Command Matrix](#a2-version-command-matrix)
  - [A.3 Automation Summary](#a3-automation-summary)

## Overview

This guide walks you through bootstrapping a kops-managed Kubernetes cluster on GCP from a fresh clone of the repo, all the way to a running cluster with a Pulumi-managed application stack on top.

- **Who it's for**: Platform operators who have access to a GCP project and the `sa-manager` service account key.
- **What you'll end up with**: A fully provisioned Kubernetes cluster (GCE instances, networking, IAM, state storage) plus the Pulumi scaffolding for deploying applications.
- **How long it takes**: ~30–45 minutes for a clean run (most of that is waiting for GCE instances to boot and pass health checks).
- **Prerequisites in one sentence**: An existing GCP project with billing enabled, `sa-manager.json` key, and the tools listed in Section 1.
- **Reusable across environments**: This guide is written to be generic — dev, staging, prod, whatever. Change the project ID, pick a cluster name, adjust a few optional knobs (node count, machine size), and the same steps produce a cluster in a different GCP project. The only per-environment artifact you create is a small env file (Section 3).
- **Pulumi post-provisioning**: Once the cluster is up, deploying the application stack is covered separately in [`docs/pulumi-setup.md`](pulumi-setup.md).

## Quick Setup

1. [Install the tools.](#1-prerequisites--tooling) `go`, `just`, `gcloud`, `direnv`, `jq` at system level, then `just install-asdf-plugins` for asdf-managed tools.
2. [Generate the SSH keypair and prepare the repo.](#14-ssh-keypair-and-local-repo) Create `credentials/k8sVM` and the `credentials/` directory.
3. [Set up authentication.](#2-authentication-setup) Place `sa-manager.json` in `credentials/` and configure `GOOGLE_APPLICATION_CREDENTIALS`.
4. [Create your cluster env file.](#3-environment-variables--the-cluster-config-file) Copy `.env.dev.dcr-experiments`, then set the seven required variables.
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

```bash
# Generate the SSH keypair (no Justfile recipe — do this by hand):
ssh-keygen -t rsa -b 4096 -f credentials/k8sVM -N "" -C "kops-cluster-nodes"

# Create the credentials directory (gitignored — safe for secrets):
mkdir -p credentials
```

Both key files are gitignored. A fresh clone always needs the keypair (or a secure copy from another operator).

## 2. Authentication Setup

You need the **Service Account Manager** key. It has broad permissions (creating other service accounts, enabling APIs, managing IAM) so the rest of the process can run with tighter, least-privilege credentials.

1.  **Obtain Key**: Request the `sa-manager` JSON key from the project owner.

    **If you *are* the project owner**, create the SA and its key with one command:

    ```bash
    just gcp-sa setup-sa-manager <PROJECT_ID>
    # key saved to credentials/sa-manager.json by default
    # optional: specify a custom path as a second argument
    ```

    This recipe creates the SA (skips if it already exists), binds all 13 manager roles, and downloads the JSON key. It works with whatever gcloud identity is active — your personal login, a named configuration, or an activated service account — so long as that identity has IAM admin privileges on the project.

    > The repo also has an older recipe (`just gcp-sa create-sa-manager`) that uses ADC and the Go binary, but `setup-sa-manager` is recommended since it stays entirely in gcloud-land.

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

    *(Source: live IAM policy for a working project, 2026-07-16.)*

2.  **Save Key**: Place it in `./credentials/sa-manager.json`.

3.  **Configure Environment**:
    ```bash
    just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/sa-manager.json"
    ```
    If you see `direnv: command not found`, install direnv — [Section 1.2](#12-core-system-tools).

    Behind the scenes, this writes `export GOOGLE_APPLICATION_CREDENTIALS=...` into `.envrc` and runs `direnv allow`. If you have `direnv`'s shell hook set up, the variable loads automatically whenever you `cd` into the project. If not, you'll need to manually `eval "$(direnv export bash)"` (or restart your shell) for it to take effect.

> **What happens next**: During cluster bootstrap, the tooling will automatically create a narrower `kops-cluster-creator` service account and rotate `GOOGLE_APPLICATION_CREDENTIALS` to point at *that* key instead. You don't need to do this yourself — the bootstrap phases (Phases 0–4 in [Section 4](#4-cluster-bootstrap)) handle both credential rotations. Just know that by the time the cluster is up, `sa-manager` is no longer in active use.

4.  **(Optional but recommended) Set up a gcloud named configuration**: Step 3 above tells the *SDKs and Go binaries* which key to use. If you also want `gcloud` CLI commands (`gcloud projects get-iam-policy`, `gcloud storage`, etc.) to run as `sa-manager`, create a dedicated named configuration for it:

    ```bash
    export PROJECT_ID="<your-gcp-project-id>"

    gcloud config configurations create sa-manager
    gcloud auth activate-service-account \
      sa-manager@${PROJECT_ID}.iam.gserviceaccount.com \
      --key-file=credentials/sa-manager.json
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
You're now in a sub-shell with all the cluster variables loaded. Every `just` command targets that cluster. To switch clusters, `exit` back to the parent shell and run `just cluster-env` with a different env file.

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

With your per-cluster env file ready (it has `PROJECT_ID`, `BUCKET_NAME`, the five cluster-level vars from Section 3.2, plus any optional tuning knobs from 3.4), start by setting the credential and activating the cluster:

> All commands assume `${PROJECT_ID}` and `${BUCKET_NAME}` are already in your cluster env file (Section 3.2). The credential is managed through the env file too — `cluster-cred` writes it, `cluster-env` loads it.

<a id="phase-0"></a>

### Phase 0 — Credential & cluster-env
```bash
just cluster-cred <env> <cluster-name> credentials/sa-manager.json
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
  credentials/kops-cluster-creator.json
```
A dedicated identity with just the 8 roles it needs (compute admin, network admin, storage admin, IAM, logging). This is least privilege in action — rather than using the all-powerful `sa-manager` forever, we mint a narrower key specifically for cluster provisioning.

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
**What it does:** Creates the bucket (skips if it already exists), then enables versioning, uniform bucket-level access, and public access prevention. If the bucket name is already taken globally, pick a different `<BUCKET_NAME>`.

<a id="phase-4b"></a>

### Phase 4b — Generate cluster manifest

This command writes the manifest to the state bucket but provisions **nothing** — it is a blueprint, not a building.

```bash
just gcp-cluster create-cluster-config ${PROJECT_ID} ${BUCKET_NAME}
```
**Under the hood**, the recipe builds and runs `./bin/cluster-ops kops create-config --project-id <project>`. The binary reads all values from environment variables via `cmd/cluster-ops/main.go` `kopsCLIFlags()` bindings — no shell variable expansion. If a variable is unset, the binary uses the hardcoded default listed in [Section 3.4](#34-optional-tuning-knobs) (e.g., `TOTAL_NODES=4`, `TOPOLOGY=public`, `NETWORKING=cilium-etcd`).

> **Production HA tip:** Set `TOPOLOGY=private`, `NETWORKING=cilium`, `TOTAL_MASTER=3`, and `CONTROL_PLANE_ZONES` in your cluster env file to override the binary defaults. See the production examples in [Section 3.4](#34-optional-tuning-knobs).

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
> **This repo currently pins kOps v1.29.2** (see `.tool-versions`). All `kops` commands are dispatched through `cluster-ops`, which auto-detects the installed kOps version and uses the correct subcommand.

<a id="phase-6"></a>

### Phase 6 — Validate HA topology

A single recipe checks everything:

```bash
just gcp-cluster validate-kops-ha
```
**What it checks:** cluster health (`kops validate cluster`), InstanceGroup listing, node zone distribution (per pool), taints on batch nodes, and control-plane zone spread.

<a id="phase-7"></a>

### Phase 7 — Post-Provisioning Hardening

After the cluster is provisioned and validated, verify that the reliability components enabled in Phase 4c are running correctly:

- **Cluster Autoscaler** (`spec.clusterAutoscaler.enabled: true` in Phase 4c) — auto-scales elastic InstanceGroups
- **Node Problem Detector** (`spec.nodeProblemDetector.enabled: true` in Phase 4c) — monitors kernel logs, taints unhealthy nodes
- **cert-manager** (`spec.certManager.enabled: true` in Phase 4c) — handles x509 certificate provisioning and rotation
- **Node local DNS cache** (`spec.kubeDNS.nodeLocalDNS.enabled: true` in Phase 4c) — per-node DNS caching DaemonSet, reduces DNS latency and CoreDNS load
- **Metrics Server** — automatically enabled by kOps when Cluster Autoscaler is active

**Verify all five are running:**

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
- **Understand the architecture** — Read [`docs/kops-architecture.md`](kops-architecture.md) for the full architectural rationale, failure modes, and design decisions behind the HA topology, InstanceGroup layout, and networking choices.
- **Set up backups** — Run `just gcp-cluster setup-cluster-backup` to configure Velero for disaster recovery.
- **Explore the cluster** — Run `just gcp-cluster k9s` for an interactive terminal dashboard, or `kubectl get all --all-namespaces` for a quick inventory.
- **Tear it down when done** — See [Section 6: Disposable Cluster Lifecycle](#6-disposable-cluster-lifecycle) to destroy the cluster when it's no longer needed and recreate it later.

<a id="appendix-a--production-ha-deployment-reference"></a>

## Appendix A — Production HA Deployment Reference

This section cross-references which kOps settings control which architectural component, the version-specific command matrix, and the current state of automation (what's implemented, what's still manual, and remaining gaps). For cluster teardown and re-creation, see [Section 6: Disposable Cluster Lifecycle](#6-disposable-cluster-lifecycle).

### A.1 Which Setting Controls Which Pool

> **Env Var Override** column references are detailed in [Section 3.4](#34-optional-tuning-knobs). Default values shown there.

| Pool | kOps `create cluster` Flag | InstanceGroup Field(s) | Env Var Override |
|------|---------------------------|----------------------|------------------|
| **Control plane** | `--control-plane-count`, `--control-plane-size`, `--control-plane-volume-size`, `--control-plane-zones` | Managed via `spec.controlPlane.kubelet`; separate IGs auto-created per zone | `TOTAL_MASTER`, `MASTER_MACHINE`, `MASTER_DISK_SIZE`, `CONTROL_PLANE_ZONES` |
| **Stateless web (default workers)** | `--node-count=3`, `--node-size=e2-standard-4`, `--node-volume-size=100` | `spec.machineType`, `spec.minSize`, `spec.maxSize`, `spec.zones` | `TOTAL_NODES`, `NODE_MACHINE`, `NODE_DISK_SIZE` |
| **Stateful database** | *(not created by `kops create cluster`)* | Created via `kops create instancegroup`. Set `spec.machineType: n2-standard-4`, `spec.minSize: 3`, `spec.maxSize: 3`, `spec.nodeLabels.pool: database` | None — manual only |
| **Batch/Spot** | *(not created by `kops create cluster`)* | Created via `kops create instancegroup`. Set `spec.machineType: e2-standard-4`, `spec.minSize: 0`, `spec.maxSize: 4`, `spec.taints[0]: spot:true:NoSchedule`, `spec.gce.preemptible: true` | None — manual only |
| **etcd volumes** | *(not a flag)* | `spec.etcdClusters[0].etcdMembers[*].volumeType: pd-ssd`, `spec.etcdClusters[1].etcdMembers[*].volumeType: pd-ssd` | None — manifest only |
| **Private topology** | `--topology=private` | `spec.topology.masters: private`, `spec.topology.nodes: private` | None — flag/manifest only |
| **API CIDR restriction** | *(not a flag)* | `spec.kubernetesApiAccess: ["<cidr>/32"]` | None — manifest only |
| **Networking / CNI** | `--networking=cilium` | `spec.networking.cilium` | None — flag only |
| **Cluster Autoscaler** | *(not a flag)* | `spec.clusterAutoscaler.enabled: true` — auto-provisions the autoscaler pod with GCE MIG permissions | None — manifest only |
| **Node Problem Detector** | *(not a flag)* | `spec.nodeProblemDetector.enabled: true` — deploys as a DaemonSet, monitors kernel logs, taints unhealthy nodes | None — manifest only |
| **Metrics Server** | *(not a flag)* | `spec.metricsServer.enabled: true` — enables `kubectl top` and HPA. Automatically enabled when Cluster Autoscaler is active | None — manifest only |
| **cert-manager** | *(not a flag)* | `spec.certManager.enabled: true` — handles x509 certificates. Set `spec.certManager.managed: false` if running externally | None — manifest only |
| **Node local DNS cache** | *(not a flag)* | `spec.kubeDNS.nodeLocalDNS.enabled: true` — per-node DNS caching DaemonSet. Reduces DNS latency and CoreDNS load | None — manifest only |

### A.2 Version Command Matrix

| Action | kOps ≤ 1.30.x (current: v1.29.2) | kOps ≥ 1.31.0 |
|--------|----------------------------------|---------------|
| Create manifest | `kops create cluster ...` | `kops create cluster ...` |
| Edit manifest | `kops edit cluster ...` | `kops edit cluster ...` |
| Apply changes | `kops update cluster --yes --admin` | `kops reconcile cluster --yes` |
| Rolling update (if needed) | `kops rolling-update cluster --yes` | *(included in `reconcile`)* |
| Validate health | `kops validate cluster --wait 10m` | `kops validate cluster --wait 10m` |
| Justfile recipe | `just gcp-cluster update-cluster` | `just gcp-cluster update-cluster` (version-aware) or `just gcp-cluster reconcile-cluster` (explicit) |

> **Warning:** Do not mix workflows. If you create a cluster with kOps 1.31+, always use `kops reconcile`. If you create with kOps 1.29/1.30, always use `kops update cluster`. The `kops reconcile` command replaces both `update` and `rolling-update` for v1.31+.

### A.3 Automation Summary

**Implemented recipes:**

| Recipe | Covers |
|--------|--------|
| `just gcp-cluster create-state-bucket <project> <bucket>` | Phase 4a — create and harden GCS bucket |
| `just gcp-cluster create-cluster-config <project> <bucket>` | Phase 4b — generate HA manifest from env vars |
| `just gcp-cluster apply-instancegroups` | Phase 4d — apply checked-in InstanceGroup templates |
| `just gcp-cluster update-cluster` | Phase 5 — version-aware apply (auto-detects kOps version) |
| `just gcp-cluster reconcile-cluster` | Same version-aware dispatch as `update-cluster` |
| `just gcp-cluster validate-kops-ha` | Phase 6 — HA topology validation |
| `just gcp-cluster validate-hardening` | Phase 7 — hardening component checks |
| `just gcp-cluster list-instancegroups` | Inspection — list all InstanceGroups |
| `just gcp-cluster delete-cluster` | Section 6 — dry-run teardown preview |
| `just gcp-cluster delete-cluster yes` | Section 6 — full teardown with confirmation |
| `just gcp-cluster save-cluster-manifest` | Section 6 — export live manifest as YAML |
| `just gcp-cluster diff-cluster-manifest` | Section 6 — diff live manifest against saved copy |
| `just gcp-cluster recreate-cluster` | Section 6 — full re-creation pipeline |

**Still manual:**

| Step | Why Manual |
|------|-----------|
| Phase 4c — `just gcp-cluster edit-cluster` | Opens `$EDITOR` for security settings (API access, etcd volumes, node SA). Human judgment required. |
| InstanceGroup template authoring | Edit templates in `config/kops/instancegroups/` once, commit, recipe applies automatically. |

**Remaining gap** (if you upgrade to kOps ≥ 1.31):

| Gap | Recommendation |
|-----|---------------|
| The `cluster-ops create-config` path creates a single default worker pool | For the three-pool HA architecture, the InstanceGroup templates handle the additional pools. `cluster-ops kops create-config` creates the base cluster + default pool; `cluster-ops ig apply` adds the rest. |

> All recipes delegate to the `cluster-ops` binary (`./bin/cluster-ops`). The legacy `create-kops-cluster` recipe has been removed — use the phased HA workflow in Section 4 for production, or `create-cluster-config` + `update-cluster` for single-pool dev clusters.
