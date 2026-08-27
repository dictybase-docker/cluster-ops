# kOps Cluster Setup Guide

Bootstraps a kops-managed Kubernetes cluster on GCP from a fresh clone, using declarative Git-first manifests. Ends with a running, validated HA cluster ready for [Pulumi](pulumi-setup.md).

Takes ~30–45 minutes for a clean run — most of it waiting for GCE instances to boot and pass health checks.

**Status**:
- **Tooling & Infrastructure recipes**: Tested 2026-08-20.
- **Declarative Git bundle (`_starter`) schema**: Verified cross-version compatible on kops v1.29.2 (dev) and v1.36.1 (prod) — both `cluster.yaml` and `instancegroups.yaml` pass `kops replace` validation on both binaries. Full end-to-end re-creation still pending first live-target run.

> **Two stores, one handoff.** Before [§3](#3-cluster-bootstrap-git-native-flow), the env file `.env.<env>.<cluster>` holds `PROJECT_ID`, credentials, and local paths. From §3 onward, cluster identity and shape live **only** in Git under `config/kops/<cluster>/`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Prerequisites & Execution Context](#1-prerequisites--execution-context)
- [2. Service Accounts & Authentication](#2-service-accounts--authentication)
- [3. Cluster Bootstrap (Git-Native Flow)](#3-cluster-bootstrap-git-native-flow)
- [4. Day-2 Operations (Git-First Workflow)](#4-day-2-operations-git-first-workflow)
- [5. Exploring the Cluster](#5-exploring-the-cluster)
- [6. Disposable Cluster Lifecycle](#6-disposable-cluster-lifecycle)
  - [6.5 Declarative Re-Creation](#65-declarative-re-creation)
- [7. Next Steps](#7-next-steps)
- [8. Related Documents](#8-related-documents)

---

## Quick Reference

For experienced users. Full details in the sections below.

```bash
# 1. Verify core system tools (go, just, gcloud, jq, envsubst)
just check-tools --skip-asdf yes

# 2. Create and enter the cluster env file
just create-cluster-env --env <env> --cluster <name> --project <project-id>
just cluster-env --env <env> --cluster <name>

# 3. Install pinned tools, then verify everything
just install-tools
just check-tools

# 4. Generate the node SSH keypair
just gcp-cluster generate-ssh-key --project <project-id>

# 5. Point the env file at sa-manager, re-enter, configure gcloud
just cluster-cred --env <env> --cluster <name> --key credentials/<project-id>/sa-manager.json
exit
just cluster-env --env <env> --cluster <name>
just gcp-cluster configure-gcloud

# 6. Enable APIs and create the least-privilege SA
just gcp-api enable-apis  --project ${PROJECT_ID} --api-file gcs-files/apis/enabled_apis.txt
just gcp-api disable-apis --project ${PROJECT_ID} --api-file gcs-files/apis/disable_enabled_apis.txt
just gcp-sa create-sa --project ${PROJECT_ID} --sa-name kops-cluster-creator \
  --roles-file gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  --output-file credentials/${PROJECT_ID}/kops-cluster-creator.json

# 7. Rotate to the narrower key
just cluster-cred --env <env> --cluster <name> --key credentials/${PROJECT_ID}/kops-cluster-creator.json
exit
just cluster-env --env <env> --cluster <name>

# 8. Bootstrap the Git bundle — Git owns identity from here
just gcp-cluster show-public-ip
just gcp-cluster bootstrap-bundle --cluster <name> --project <project-id> --api-access-cidr "<ip>/32"

# 9. Review, then commit the bundle
git add config/kops/<name>/ && git commit -m "<name>: initial cluster manifest bundle"

# 10. Provision
just gcp-cluster create-state-bucket --project <project-id> --bucket-name kops-state-<name>
just gcp-cluster replace-manifests  --cluster <name> --force yes
just gcp-cluster upload-ssh-secret  --cluster <name> --ssh-key credentials/<project-id>/k8sVM.pub
just gcp-cluster plan-cluster       --cluster <name>
just gcp-cluster update-cluster     --cluster <name>

# 11. Validate
just gcp-cluster validate-cluster
just gcp-cluster validate-kops-ha
just gcp-cluster validate-hardening

# 12. Explore
just gcp-cluster export-kubeconfig --cluster <name>
just gcp-cluster k9s
```

Then continue with [`pulumi-setup.md`](pulumi-setup.md).

---

<a id="11-the-gcp-project"></a>
<a id="12-core-system-tools"></a>
<a id="13-environment-variables--the-cluster-env-file"></a>
<a id="131-create-and-activate"></a>
<a id="132-variables-overview"></a>
<a id="133-after-bootstrap--git-takes-identity"></a>
<a id="14-asdf-managed-tools"></a>
<a id="141-tool-version-files"></a>
<a id="15-per-project-file-isolation"></a>
<a id="16-ssh-keypair"></a>

## 1. Prerequisites & Execution Context

You need a GCP project with billing enabled and the core system tools installed. One GCP project hosts exactly one cluster.
→ [Prerequisites](reference/kops/prerequisites.md) · [Cluster env](reference/kops/cluster-env.md) · [Tool versions](reference/kops/tool-versions.md) · [File isolation](reference/kops/file-isolation.md)

```bash
# Core system tools: go, just, gcloud, jq, envsubst
just check-tools --skip-asdf yes

# Create the per-cluster env file, then enter its sub-shell
just create-cluster-env --env <env> --cluster <cluster-name> --project <project-id>
just cluster-env --env <env> --cluster <cluster-name>

# Install and verify the asdf-pinned tools (kubectl, kops, pulumi, velero, helm, k9s)
just install-tools
just check-tools

# Generate the node SSH keypair
just gcp-cluster generate-ssh-key --project <project-id>
```

Stay inside the `cluster-env` sub-shell for the rest of this guide. The file is gitignored and holds `PROJECT_ID`, credential paths, kubeconfig path, and the SSH public key — never kops name, state store, or Kubernetes version, which become Git fields at [§3](#3-cluster-bootstrap-git-native-flow).

Need a different `kops`/`kubectl` binary for this cluster than the repo default? Use `just pin-tool-versions --env <env> --cluster <cluster-name>` ([tool versions](reference/kops/tool-versions.md#create-a-per-cluster-pin-file)).

---

<a id="phase-1a"></a>
<a id="phase-1b"></a>
<a id="phase-2"></a>

## 2. Service Accounts & Authentication

Start with the broad `sa-manager` identity, use it to enable APIs and mint the least-privilege `kops-cluster-creator`, then rotate to that narrower key.
→ [Service accounts](reference/kops/service-accounts.md)

```bash
# Obtain sa-manager (project owners only; otherwise ask the owner for the JSON key)
just gcp-sa setup-sa-manager --project-id ${PROJECT_ID}

# Point the env file at it, re-enter the shell, configure gcloud
just cluster-cred --env <env> --cluster <cluster-name> \
  --key credentials/${PROJECT_ID}/sa-manager.json
exit
just cluster-env --env <env> --cluster <cluster-name>
just gcp-cluster configure-gcloud

# Phase 1a / 1b — enable required APIs, disable unused ones
just gcp-api enable-apis  --project ${PROJECT_ID} --api-file gcs-files/apis/enabled_apis.txt
just gcp-api disable-apis --project ${PROJECT_ID} --api-file gcs-files/apis/disable_enabled_apis.txt

# Phase 2 — create the least-privilege SA
just gcp-sa create-sa --project ${PROJECT_ID} --sa-name kops-cluster-creator \
  --roles-file gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  --output-file credentials/${PROJECT_ID}/kops-cluster-creator.json

# Rotate to the narrower key — both the env credential AND the gcloud identity
just cluster-cred --env <env> --cluster <cluster-name> \
  --key credentials/${PROJECT_ID}/kops-cluster-creator.json
exit
just cluster-env --env <env> --cluster <cluster-name>
just gcp-cluster configure-gcloud --name kops-cluster-creator
```

`configure-gcloud` replaces the five-command `gcloud config configurations` sequence and reads the identity from the key's own `client_email`.

> **Rotate both halves.** `cluster-cred` only changes `GOOGLE_APPLICATION_CREDENTIALS`, which authenticates recipes and libraries. Direct `gcloud` commands authenticate through the *named configuration*, so without the second `configure-gcloud` call your shell keeps acting as `sa-manager`. Running it with `--name kops-cluster-creator` creates a separate configuration, leaving `sa-manager` intact for the rare task that still needs it ([service accounts](reference/kops/service-accounts.md#rotate-to-the-narrower-key)).

---

<a id="step-31--bootstrap-local-manifest-bundle"></a>
<a id="step-32--customize-yaml-in-git"></a>
<a id="step-33--create-state-bucket"></a>
<a id="step-34--push-manifest-bundle-to-state-store"></a>
<a id="step-35--upload-ssh-secret"></a>
<a id="step-36--preview--apply-version-aware"></a>
<a id="step-37--validate-ha-topology--hardening"></a>

## 3. Cluster Bootstrap (Git-Native Flow)

`bootstrap-bundle` renders the manifest bundle locally — zero cloud calls — and is the handoff point after which **Git owns cluster identity and shape**.
→ [Bootstrap detail](reference/kops/bootstrap.md)

```bash
# Step 3.1 — render the local bundle (find your CIDR first)
just gcp-cluster show-public-ip
just gcp-cluster bootstrap-bundle \
  --cluster <cluster-name> \
  --project <project-id> \
  --api-access-cidr "<your-ip>/32"

# Step 3.2 — review the generated YAML, then commit it
$EDITOR config/kops/<cluster-name>/cluster.yaml
$EDITOR config/kops/<cluster-name>/instancegroups.yaml
git add config/kops/<cluster-name>/
git commit -m "<cluster-name>: initial cluster manifest bundle"

# Step 3.3 — create the hardened state bucket
just gcp-cluster create-state-bucket \
  --project <project-id> \
  --bucket-name kops-state-<cluster-name>

# Step 3.4 — push the committed bundle into state storage
just gcp-cluster replace-manifests --cluster <cluster-name> --force yes

# Step 3.5 — upload the public SSH key into kops state
just gcp-cluster upload-ssh-secret \
  --cluster <cluster-name> \
  --ssh-key credentials/<project-id>/k8sVM.pub

# Step 3.6 — preview, then apply (both are kOps-version aware)
just gcp-cluster plan-cluster   --cluster <cluster-name>
just gcp-cluster update-cluster --cluster <cluster-name>

# Step 3.7 — validate health, HA topology, and hardening addons
just gcp-cluster validate-cluster
just gcp-cluster validate-kops-ha
just gcp-cluster validate-hardening
```

`show-public-ip` prints a ready-to-paste `<ip>/32` and refuses a private address that would lock you out. Generated defaults — Kubernetes 1.28.8, Cilium, pd-ssd etcd, CSI driver, hardening addons, and the `stateless-web` / `stateful-db` / `batch-spot` pools — are listed in [bootstrap detail](reference/kops/bootstrap.md#generated-defaults).

---

<a id="41-drift-detection-ci"></a>
<a id="42-when-to-run-a-rolling-update"></a>

## 4. Day-2 Operations (Git-First Workflow)

Every configuration and scaling change is edit YAML → commit → push to state store → preview → apply → verify no drift.
→ [Day-2 operations](reference/kops/day2-operations.md)

```bash
$EDITOR config/kops/<cluster-name>/instancegroups.yaml
git commit -am "<cluster-name>: scale stateless-web pool to 8"
just gcp-cluster replace-manifests --cluster <cluster-name>
just gcp-cluster plan-cluster      --cluster <cluster-name>
just gcp-cluster update-cluster    --cluster <cluster-name>
just gcp-cluster drift-manifests   --cluster <cluster-name>
```

Changing `machineType`, disk size, `image`, or `kubernetesVersion` also needs a rolling update; pool scaling and CIDR changes do not ([change trigger matrix](reference/kops/day2-operations.md#change-trigger-matrix)):

```bash
just gcp-cluster rolling-update --cluster <cluster-name>            # dry-run inspection
just gcp-cluster rolling-update --cluster <cluster-name> --yes yes  # execute
```

Wire `drift-manifests` (blocking) and `plan-cluster` (review output) into CI on PR and a nightly cron ([CI drift detection](reference/kops/day2-operations.md#drift-detection-ci)). After any break-glass `kops edit`, reconcile Git immediately with `just gcp-cluster export-bundle`.

---

## 5. Exploring the Cluster

Export the kubeconfig and open the terminal UI. If `KUBECONFIG` is set from `just cluster-env`, export writes that path.

```bash
just gcp-cluster export-kubeconfig --cluster <cluster-name>
just gcp-cluster k9s
```

---

<a id="61-mindset--durable-blueprint"></a>
<a id="62-what-survives-teardown"></a>
<a id="63-teardown--destroy-the-cluster"></a>
<a id="64-optional-delete-the-state-bucket"></a>
<a id="67-caveats"></a>

## 6. Disposable Cluster Lifecycle

VMs are transient; the blueprint in Git plus local credentials are durable. Teardown destroys compute only — the state bucket, SSH keypair, SA keys, and env file all survive.
→ [Teardown](reference/kops/teardown.md) · [What survives](reference/kops/teardown.md#what-survives-teardown)

```bash
# Destroy the cluster (omit --confirm for a dry-run preview)
just gcp-cluster delete-cluster --confirm yes

# Optional: also delete the state bucket and every object version
just gcp-cluster delete-state-bucket --cluster <cluster-name> --confirm yes
```

### 6.5 Declarative Re-Creation

Rebuild from the Git bundle. Steps 0 and 3 are the ones people forget.
→ [Recreation detail](reference/kops/recreation.md)

```bash
# 0. Re-enter the operator env (recreate the file first if it is gone)
just cluster-env --env <env> --cluster <cluster-name>

# 1. Recreate the state bucket — only if it was deleted
just gcp-cluster create-state-bucket \
  --project <project-id> --bucket-name kops-state-<cluster-name>

# 2. Push the canonical Git bundle to the state store
just gcp-cluster replace-manifests --cluster <cluster-name> --force yes

# 3. Restore the SSH secret — mandatory, it lives in kops state, not Git
just gcp-cluster upload-ssh-secret \
  --cluster <cluster-name> --ssh-key credentials/<project-id>/k8sVM.pub

# 4. Preview and provision
just gcp-cluster plan-cluster   --cluster <cluster-name>
just gcp-cluster update-cluster --cluster <cluster-name>

# 5. Validate
just gcp-cluster validate-cluster
just gcp-cluster validate-kops-ha
just gcp-cluster validate-hardening
```

The Kubernetes layer comes back empty — reapply [`pulumi-setup.md`](pulumi-setup.md) then [`arangodb-deploy.md`](arangodb-deploy.md).

---

## 7. Next Steps

| Document | Use it for |
|----------|------------|
| [`pulumi-setup.md`](pulumi-setup.md) | Pulumi backend, KMS, state bucket, StorageClass |
| [`arangodb-deploy.md`](arangodb-deploy.md) | Production ArangoDB Cluster on `stateful-db` |
| [`kops-gcp-architecture.md`](kops-gcp-architecture.md) | HA vs cost, node capacity, database storage |

---

## 8. Related Documents

**Reference details for this guide:**
- [Prerequisites & execution context](reference/kops/prerequisites.md)
- [The cluster env file](reference/kops/cluster-env.md)
- [asdf-managed tool versions](reference/kops/tool-versions.md)
- [Per-project file isolation & SSH](reference/kops/file-isolation.md)
- [Service accounts & authentication](reference/kops/service-accounts.md)
- [Cluster bootstrap detail](reference/kops/bootstrap.md)
- [Day-2 operations detail](reference/kops/day2-operations.md)
- [Teardown & disposable lifecycle](reference/kops/teardown.md)
- [Declarative re-creation](reference/kops/recreation.md)

**Other documentation:**
- Repo overview: [`README.md`](../README.md)
- Architecture deep dive: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Production ArangoDB: [`arangodb-deploy.md`](arangodb-deploy.md)
- ArangoDB implementation plan: [`plans/arangodb-production.md`](plans/arangodb-production.md)
