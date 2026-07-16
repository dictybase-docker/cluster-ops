# Kops Cluster Creation Guide

**Last tested**: 2026-07-16

## Overview

This guide walks you through bootstrapping a kops-managed Kubernetes cluster on GCP from a fresh clone of the repo, all the way to a running cluster with a Pulumi-managed application stack on top.

- **Who it's for**: Platform operators who have access to a GCP project and the `sa-manager` service account key.
- **What you'll end up with**: A fully provisioned Kubernetes cluster (GCE instances, networking, IAM, state storage) plus the Pulumi scaffolding for deploying applications.
- **How long it takes**: ~30–45 minutes for a clean run (most of that is waiting for GCE instances to boot and pass health checks).
- **Prerequisites in one sentence**: An existing GCP project with billing enabled, `sa-manager.json` key, and the tools listed in Section 1.
- **Reusable across environments**: This guide is written to be generic — dev, staging, prod, whatever. Change the project ID, pick a cluster name, adjust a few optional knobs (node count, machine size), and the same steps produce a cluster in a different GCP project. The only per-environment artifact you create is a small env file (Section 3).

## Table of Contents
- [Overview](#overview)
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
- [5. Verification & Access](#5-verification--access)
  - [5.1 Sanity Checks (Per Phase)](#51-sanity-checks-per-phase)
  - [5.2 Cluster Health](#52-cluster-health)
  - [5.3 Getting Inside the Cluster](#53-getting-inside-the-cluster)
- [6. Post-Provisioning (Pulumi)](#6-post-provisioning-pulumi)
- [7. Common Pitfalls](#7-common-pitfalls)
  - [7.1 ".envrc Changes Don't Take Effect" (~35% of issues)](#71-envrc-changes-dont-take-effect-35-of-issues)
  - [7.2 "Missing KOPS_CLUSTER_NAME / SSH_KEY / KUBERNETES_VERSION" (~25% of issues)](#72-missing-kops_cluster_name--ssh_key--kubernetes_version-25-of-issues)
  - [7.3 Re-running Against an Existing Cluster (~15% of issues)](#73-re-running-against-an-existing-cluster-15-of-issues)
  - [7.4 Forgot the SSH Keypair (~10% of issues)](#74-forgot-the-ssh-keypair-10-of-issues)
  - [7.5 Wrong KOPS_CLUSTER_NAME Suffix (~8% of issues)](#75-wrong-kops_cluster_name-suffix-8-of-issues)
  - [7.6 "Service Not Enabled" During SA Creation (~7% of issues)](#76-service-not-enabled-during-sa-creation-7-of-issues)
- [8. Escalation — When to Call for Help](#8-escalation--when-to-call-for-help)

## 1. Prerequisites & Tooling

Before diving in, let's make sure you have everything you need. Think of this as packing your bags before a trip — missing one item can grind things to a halt later.

### 1.1 The GCP Project

You'll need a **GCP project that already exists** with billing enabled. This isn't something the repo's tooling creates — an org admin needs to set it up in the GCP console beforehand. Make a note of the `PROJECT_ID`; you'll need it for nearly every command that follows.

### 1.2 Core System Tools

These aren't managed by `asdf` — install them through your system package manager:

| Tool | Why You Need It | Check Command |
|------|-----------------|--------------|
| **Go** (≥1.21) | Builds the repo's Go binaries (`gcp-tools`, `kops-cluster-creator`, `util`) that the Justfile recipes shell out to | `go version` |
| **just** | The task runner — every recipe in this guide (install tools, create cluster, deploy) is a `just` command | `just --version` |
| **gcloud** | Google Cloud CLI — used by several recipes behind the scenes and handy for manual debugging | `gcloud version` |
| **direnv** | Auto-loads environment variables from `.envrc` when you enter the project directory — no manual `export` needed | `direnv version` |
| **jq** | JSON processor used by several Justfile recipes behind the scenes | `jq --version` |

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
    # Example: just install-tool kubectl 1.28.9
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

    *(Source: live IAM policy for a working project, 2026-07-16 — see `docs/sa-manager-roles.md`.)*

2.  **Save Key**: Place it in `./credentials/sa-manager.json`.

3.  **Configure Environment**:
    ```bash
    just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/sa-manager.json"
    ```
    **Expected output**: `direnv: loading ...` followed by `direnv: export ~PATH` (exact vars vary). If you see `direnv: command not found`, install direnv — [Section 1.2](#12-core-system-tools).

    Behind the scenes, this writes `export GOOGLE_APPLICATION_CREDENTIALS=...` into `.envrc` and runs `direnv allow`. If you have `direnv`'s shell hook set up, the variable loads automatically whenever you `cd` into the project. If not, you'll need to manually `eval "$(direnv export bash)"` (or restart your shell) for it to take effect.

> **What happens next**: During cluster bootstrap, the tooling will automatically create a narrower `kops-cluster-creator` service account and rotate `GOOGLE_APPLICATION_CREDENTIALS` to point at *that* key instead. You don't need to do this yourself — the single `init-kops-cluster` recipe handles both credential rotations. Just know that by the time the cluster is up, `sa-manager` is no longer in active use.

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

    > **You can add more SA identities later the same way** (e.g., a `kops-creator` configuration for the `kops-cluster-creator` key). Each gets its own named slot — see `docs/sa-manager-roles.md` for the full pattern.

## 3. Environment Variables — The Cluster Config File

Before `init-kops-cluster` will work, it needs to know a handful of things about your cluster: its name, where to store its state, which SSH key to use, and so on. These are defined in a **per-cluster env file** — one file per environment, following the naming convention `.env.<env>.<cluster>` (e.g., `.env.dev.my-cluster`, `.env.staging.my-cluster`, `.env.prod.my-cluster`).

The repo ships with `.env.dev.dcr-experiments` as a concrete example. You can copy it as a starting point, or create your own from scratch — either way, the five variables below are what matter.

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

These variables control the shape of your cluster. **Add them to the same per-cluster env file you created in Section 3.1** — the `kops-cluster-creator` binary reads them directly from the environment via `EnvVars` bindings in `internal/kops/flags.go`. If unset, the hardcoded defaults in that file kick in. You'll typically want smaller values for dev and larger ones for prod.

| Variable | Controls | Default | Example (dev) | Example (prod) |
|----------|----------|---------|---------------|----------------|
| `TOTAL_NODES` | How many worker nodes | `4` | `2` | `6` |
| `NODE_MACHINE` | Worker VM type | `n1-custom-2-4096` | `n1-custom-1-2048` | `n1-custom-4-8192` |
| `NODE_DISK_SIZE` | Worker boot disk (GB) | `100` | `50` | `200` |
| `TOTAL_MASTER` | Control plane nodes | `1` | `1` | `1` |
| `MASTER_MACHINE` | Control plane VM type | `n1-custom-4-8192` | `n1-custom-2-4096` | `n1-custom-8-16384` |
| `MASTER_DISK_SIZE` | Control plane boot disk (GB) | `75` | `50` | `150` |
| `COMPUTE_IMAGE` | OS image for all nodes | `ubuntu-os-cloud/ubuntu-2204-jammy-v20240829` | *(leave default)* | *(leave default)* |

## 4. Cluster Bootstrap

Now the fun begins. With your per-cluster env file ready (it has `PROJECT_ID`, `BUCKET_NAME`, the five cluster vars from Section 3.2, plus any optional tuning knobs from 3.4), start by setting the credential and activating the cluster:

> All commands assume `${PROJECT_ID}` and `${BUCKET_NAME}` are already in your cluster env file (Section 3.2). The credential is managed through the env file too — `cluster-cred` writes it, `cluster-env` loads it.

**Phase 0 — Set the sa-manager credential and activate the cluster**
```bash
just cluster-cred <env> <cluster-name> credentials/sa-manager.json
just cluster-env <env> <cluster-name>
```
This writes `GOOGLE_APPLICATION_CREDENTIALS` into your cluster env file pointing at the `sa-manager` key, then drops you into a sub-shell with all vars loaded. Phases 1a through 2 run with `sa-manager`'s broad permissions.

**Phase 1a — Enable required APIs**
```bash
just gcp-api enable-apis ${PROJECT_ID} gcs-files/apis/enabled_apis.txt
```
Turns on the 42 GCP services the cluster needs (Compute, Container, Storage, IAM, KMS, Monitoring, Logging, etc.).

**Phase 1b — Disable unused APIs**
```bash
just gcp-api disable-apis ${PROJECT_ID} gcs-files/apis/disable_enabled_apis.txt
```
Turns off 8 services that aren't used (BigQuery, Deployment Manager, Datastore, etc.). Purely a cleanup step — not a hard dependency.

**Phase 2 — Create the kops-cluster-creator service account**
```bash
just gcp-sa create-sa ${PROJECT_ID} kops-cluster-creator \
  gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  credentials/kops-cluster-creator.json
```
A dedicated identity with just the 8 roles it needs (compute admin, network admin, storage admin, IAM, logging). This is least privilege in action — rather than using the all-powerful `sa-manager` forever, we mint a narrower key specifically for cluster provisioning.

**Phase 3 — Rotate the credential in the cluster env file**
```bash
just cluster-cred <env> <cluster-name> credentials/kops-cluster-creator.json
```
This updates the `GOOGLE_APPLICATION_CREDENTIALS` line in your cluster env file to point at the new `kops-cluster-creator` key. To pick it up, exit the sub-shell and re-enter:
```bash
exit
just cluster-env <env> <cluster-name>
```
From this point on, the narrower `kops-cluster-creator` key is what's used for everything. The `sa-manager` key goes dormant — and `.envrc` was never touched.

**Phase 4 — State store bucket + kops create + provision + validate**
```bash
just gcp-cluster create-kops-cluster ${PROJECT_ID} ${BUCKET_NAME}
```
This one bundles several sub-steps:
- Finds or creates the GCS state bucket with versioning and lifecycle rules
- Runs `kops create cluster` — builds the manifest and writes it to the bucket (**no VMs exist yet** — it's a blueprint, not a building)
- Runs `kops update cluster --yes` — turns the blueprint into real GCE instances
- Validates the cluster and prints status

---

**A note on idempotency**: Phases 0–2 are safe to re-run (they check if the thing already exists before creating it). Phase 4's `kops create cluster` step is *not* — running it against an existing cluster name will error out because a manifest with that name already lives in the state bucket.

**Expected successful output** (final lines):
```
Cluster "my-cluster.k8s.local" created in GCS bucket gs://kops-state-my-cluster
...
Your cluster my-cluster.k8s.local is ready
```

**Common failure signatures**:
- `Error: bucket "..." already exists in a different project` — the bucket name is taken globally. Pick a different `<BUCKET_NAME>`.
- `Error: could not find the specified project` — the `<PROJECT_ID>` is wrong or the project doesn't exist. Double-check in the GCP console.
- `Error: SERVICE_ACCOUNT_NOT_FOUND` — the credential rotation didn't take effect in your shell. See [Section 7.1](#71-envrc-changes-dont-take-effect-35-of-issues).

## 5. Verification & Access

Once the bootstrap command completes, here's how to confirm everything is healthy and get hands-on access.

### 5.1 Sanity Checks (Per Phase)

> These commands assume `${PROJECT_ID}` and `${BUCKET_NAME}` are still in your shell environment from [Section 4](#4-cluster-bootstrap). If not, re-export them:
> ```bash
> export PROJECT_ID="<your-gcp-project-id>"
> export BUCKET_NAME="<your-bucket-name>"
> ```

You can run these manually for peace of mind (though `init-kops-cluster` handles most of them automatically):

*   **After APIs are enabled**: `just gcp-api list-enabled-apis ${PROJECT_ID} /tmp/enabled.txt` and compare against `gcs-files/apis/enabled_apis.txt`. If APIs are missing, re-run `init-kops-cluster` (Phase 1 is idempotent).
*   **After the service account is created**: Confirm `credentials/kops-cluster-creator.json` exists with tight permissions (`ls -la credentials/kops-cluster-creator.json` should show mode `0600`). If the file is missing, re-run `init-kops-cluster` (Phases 1–4 are idempotent). If it fails again, escalate per [Section 8](#8-escalation--when-to-call-for-help).
*   **After credentials rotate**: `grep GOOGLE_APPLICATION_CREDENTIALS .envrc` should point at `kops-cluster-creator.json`, not `sa-manager.json`. If it still shows `sa-manager`, your shell may not have picked up the `.envrc` change — see [Section 7.1](#71-envrc-changes-dont-take-effect-35-of-issues).
*   **After the bucket is created**: `gsutil ls -L -b gs://${BUCKET_NAME}` should show versioning enabled and a lifecycle rule present. If the bucket doesn't exist, re-run `init-kops-cluster` (Phase 4 is idempotent).
*   **After kops create cluster**: `kops get cluster --state $KOPS_STATE_STORE` should list your new cluster — this confirms the manifest exists even before VMs are provisioned. If nothing shows up, check that `KOPS_STATE_STORE` and `KOPS_CLUSTER_NAME` are set correctly — see [Section 7.2](#72-missing-kops_cluster_name--ssh_key--kubernetes_version-25-of-issues).

### 5.2 Cluster Health

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

### 5.3 Getting Inside the Cluster

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

## 6. Post-Provisioning (Pulumi)

Once the cluster is running, deploy the application stack on top using Pulumi.

1.  **Initialize & Deploy**:
    ```bash
    # Set these once for your environment, then the command is copy-paste-ready:
    export STACK_NAME="<stack-name>"        # e.g., dev-my-app
    export FROM_STACK="<base-stack>"         # base stack to inherit config from, or use "" for none
    export PROJECT_ID="<your-gcp-project-id>"
    export KEYRING="<kms-keyring>"           # created during cluster bootstrap
    export KEY="<kms-key>"                    # created during cluster bootstrap
    export BUCKET="<pulumi-state-bucket>"     # e.g., pulumi-state-my-cluster

    just pulumi-init-and-deploy ${STACK_NAME} ${FROM_STACK} ${PROJECT_ID} ${KEYRING} ${KEY} ${BUCKET}
    ```
    *Creates Pulumi IAM, KMS keys, state bucket, and deploys initial resources.*

    **Expected healthy output**:
    ```
    Updating stack '<STACK_NAME>'
    ...
    Resources:
        + N created
    Duration: XmYs
    ```
    followed by a summary of created resources (service accounts, KMS keyrings, storage buckets).

2.  **Verify the deployment**:
    ```bash
    pulumi stack output --stack <STACK_NAME>
    ```
    **Expected output**: Lists the stack's exported values (e.g., service account emails, bucket names). If this returns nothing or an error, the deployment may not have completed — re-run the deploy command.

**If Pulumi fails with an IAM or permissions error**: This typically means the `kops-cluster-creator` key doesn't have the roles Pulumi needs (Pulumi requires its own `pulumi-manager` service account, which `pulumi-init-and-deploy` creates). Check that `GOOGLE_APPLICATION_CREDENTIALS` still points at `kops-cluster-creator.json` — see [Section 7.1](#71-envrc-changes-dont-take-effect-35-of-issues). If the error persists after 2 retries, escalate per [Section 8](#8-escalation--when-to-call-for-help).

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

`kops create cluster` (Phase 5) is not idempotent — if a cluster with the same name already exists in the state bucket, it will error with something like:
```
Error: cluster "my-cluster.k8s.local" already exists
```
If you need to re-create: either use a new cluster name, or delete the existing cluster first:
```bash
kops delete cluster --name $KOPS_CLUSTER_NAME --state $KOPS_STATE_STORE --yes
```
**Expected output**: `Deleted cluster: "my-cluster.k8s.local"` — after this you can re-run `init-kops-cluster`.
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
| `init-kops-cluster` fails after **3 retries** with the same error | Indicates a systemic issue, not a transient glitch |
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
