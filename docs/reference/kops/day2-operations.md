# Day-2 Operations Detail

Back to: [kOps Cluster Setup](../../kops-setup.md)

After bootstrap, **all configuration and scaling changes follow the declarative Git-first workflow**. Git is the source of truth; the state store and the cloud are downstream of it.

## The Full Loop

```bash
# 1. Edit the canonical local YAML
$EDITOR config/kops/<cluster-name>/cluster.yaml        # cluster settings, security, CIDRs
$EDITOR config/kops/<cluster-name>/instancegroups.yaml # pool sizing, machine types

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

# 7. If VM sizes or boot disks changed, roll the instances
just gcp-cluster rolling-update \
  --cluster <cluster-name> \
  --instance-group stateless-web \
  --yes yes
```

## Break-Glass Emergency Edits

If an operator must run `kops edit` during a live outage, reconcile Git immediately after:

```bash
just gcp-cluster export-bundle --cluster <cluster-name>
git diff
git commit -am "HOTFIX: reconcile emergency live edits"
```

Skipping this leaves the live cluster ahead of Git, which the nightly drift check will catch.

## Drift Detection (CI)

Two checks, wired into CI on PR and a nightly cron:

| Check | Role / Catches |
|-------|----------------|
| `just gcp-cluster drift-manifests` | **Blocking.** Non-zero exit if canonical `cluster.yaml` or `instancegroups.yaml` differs from live state storage — catches a `kops edit` that was never backfilled |
| `just gcp-cluster plan-cluster` | **Review output.** Version-aware preview without `--yes`, rendering the pending cloud diff into PR comments |

```yaml
# Example CI step:
- name: Check Manifest Drift
  run: just gcp-cluster drift-manifests

- name: Preview Cloud Plan
  run: just gcp-cluster plan-cluster
```

CI runs with a read-only service account (`roles/compute.viewer` + bucket read).

- **Nightly failure** on `drift-manifests` indicates an uncommitted live edit.
- **On PRs**, `drift-manifests` verifies the base state is clean while `plan-cluster` renders the pending diff for human review.

## When to Run a Rolling Update

`update-cluster` updates the cloud infrastructure templates in GCP, but does not necessarily restart live running VMs.

### How to Check

Run `rolling-update` without `--yes` — it defaults to dry-run inspection:

```bash
just gcp-cluster rolling-update --cluster <cluster-name>
```

| Output | Meaning |
|--------|---------|
| `No cluster updates required.` | Done — no VMs need replacement |
| `NEEDUPDATE > 0` | Running VMs are on old templates |

To execute:

```bash
just gcp-cluster rolling-update --cluster <cluster-name> --yes yes
```

### Change Trigger Matrix

| Change Made in YAML | Rolling Update Needed? | Why |
|---|:---:|---|
| **`machineType`** (CPU / RAM change) | **Yes** | Cannot resize a running GCE VM on the fly |
| **`rootVolumeSize`** / disk type | **Yes** | Requires boot disk recreation |
| **`image`** (OS security upgrade) | **Yes** | New base OS image requires a fresh VM |
| **`kubernetesVersion`** (k8s upgrade) | **Yes** | Node requires kubelet / containerd binary update |
| **`minSize` / `maxSize`** (pool scaling) | **No** | GCE MIG scales VM count automatically |
| **`kubernetesApiAccess`** (API CIDRs) | **No** | GCP load balancer / firewall rule update only |
| **Addon configs** (Autoscaler, Cert-Manager) | **No** | In-cluster pod/DaemonSet update |

### kOps Version Behavior

| Pin | Behavior |
|-----|----------|
| **kOps ≤ 1.30** (`dev` pin v1.29.2) | `update-cluster` and `rolling-update` are separate commands. Running `rolling-update` after VM template changes is required |
| **kOps ≥ 1.31** (`prod` pin v1.36.1) | `update-cluster` runs `kops reconcile cluster --yes`, which reconciles cloud resources and rolls nodes sequentially in one pass. Use `rolling-update` only for targeted pool rolls (`--instance-group <name>`) or forced restarts (`--force yes`) |
