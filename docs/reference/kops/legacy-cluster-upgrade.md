# Legacy kOps 1.29 Cluster Upgrades (`dcr-experiments`)

Back to: [kOps Cluster Setup](../../kops-setup.md)

## Cluster Shape and Differences from Production

The `dcr-experiments` lab cluster (`dictycr-dev-staging-dcr-experiments-k8s.local`, namespace `dev`, stack `experiments`) differs from the production `dcr-kube1` cluster in four ways:

| Property | `dcr-experiments` (lab) | `dcr-kube1` (production) |
|---|---|---|
| kOps / Kubernetes pin | `kops v1.29.2` / `v1.28.8` | `kops v1.36.1` / `v1.35.8` |
| Node OS image family | `ubuntu-2204-lts` (`ubuntu-2204-jammy-v20261021`) | `ubuntu-2404-lts-amd64` (`ubuntu-2404-noble-amd64-v20261024`) |
| Topology | Single zone (`us-central1-c`), 1 control-plane VM, 1 worker pool (`nodes-us-central1-c`, 4 VMs) | Three zones (`a`, `b`, `c`), 3 control-plane VMs, separate `nodes` and `stateful-db` pools |
| Datastores | Single-node ArangoDB (`Single` mode, no PDBs) and pre-plugin CloudNativePG (`1.24`, PostgreSQL 14) | 9-member ArangoDB cluster and Barman Cloud plugin CloudNativePG (`1.30`, PostgreSQL 16) |

Because `kops v1.29.2` predates `kops reconcile cluster`, `just gcp-cluster apply-cluster` updates cloud templates only; you must run `just gcp-cluster rolling-update --yes yes` afterward to replace VMs.

---

## Upgrade Procedure

```bash
# 1. Inspect the latest Ubuntu 22.04 LTS image in GCP
gcloud compute images describe-from-family ubuntu-2204-lts \
  --project ubuntu-os-cloud \
  --format='value(name,creationTimestamp)'

# 2. Update spec.image in config/kops/dictycr-dev-staging-dcr-experiments/instancegroups.yaml
$EDITOR config/kops/dictycr-dev-staging-dcr-experiments/instancegroups.yaml
git commit -am "dcr-experiments: upgrade node image"

# 3. Push manifests and update GCE instance templates
just gcp-cluster apply-cluster \
  --cluster dictycr-dev-staging-dcr-experiments \
  --state gs://kops-state-dcr-experiments

# 4. Roll the control plane first, then the worker pool
just gcp-cluster rolling-update \
  --cluster dictycr-dev-staging-dcr-experiments \
  --state gs://kops-state-dcr-experiments \
  --instance-group control-plane-us-central1-c \
  --yes yes

just gcp-cluster rolling-update \
  --cluster dictycr-dev-staging-dcr-experiments \
  --state gs://kops-state-dcr-experiments \
  --instance-group nodes-us-central1-c \
  --yes yes
```

---

## Issues Encountered and Solutions

| # | Issue on `dcr-experiments` | Symptom | Solution Applied |
|---|---|---|---|
| 1 | Single-instance `dev/logto-primary` PodDisruptionBudget (`minAvailable: 1`) | Draining the worker node that hosts `dev/logto-1` fails with `Cannot evict pod as it would violate the pod's disruption budget`. | Set `spec.enablePDB: false` on `Cluster` `dev/logto` so the operator deletes `dev/logto-primary`. |
| 2 | `kube-system/metadata-proxy-v0.12` init container fails on existing nodes | When `apply-cluster` refreshes DaemonSets before nodes roll, `update-ipdtables` runs `ip addr add dev lo 169.254.169.252/32` and fails with `RTNETLINK answers: File exists`, which blocks `kops validate cluster`. | Replace `ip addr add` with idempotent `ip addr replace dev lo 169.254.169.252/32` in the `metadata-proxy-v0.12` DaemonSet init container, and roll `control-plane-us-central1-c` before `nodes-us-central1-c`. |
| 3 | Missing port `4003` (`cilium-etcd`) internal load balancer forwarding rule in `kops v1.29.2` | Worker `cilium-agent` pods time out connecting to `https://api.internal.dictycr-dev-staging-dcr-experiments-k8s.local:4003` (`10.0.16.2:4003`) and restart every 15 minutes. | Create internal TCP forwarding rule `cilium-etcd-us-central1-dictycr-dev-staging-dcr-experiments` on `10.0.16.2:4003` pointing to backend service `api-dictycr-dev-staging-dcr-experiments-k8s-local`. |

---

### 1. Single-Instance PostgreSQL PodDisruptionBudget on Pre-Plugin CNPG

**Issue:** `dcr-experiments` runs an older CloudNativePG operator (`1.24`, PostgreSQL 14) without the `barmancloud.cnpg.io` `ObjectStore` CRD used by newer production stacks (see [PostgreSQL pool requirements](../postgres/pool-requirements.md)). Its single-instance `dev/logto` cluster still created a `dev/logto-primary` `PodDisruptionBudget` (`minAvailable: 1`, `ALLOWED DISRUPTIONS: 0`), which blocks node drain.

**Solution:** Disable the `PodDisruptionBudget` directly on the `dev/logto` `Cluster` resource without running the production plugin stack against `experiments`:

```bash
kubectl patch cluster -n dev logto --type=merge -p '{"spec":{"enablePDB":false}}'
```

---

### 2. `metadata-proxy-v0.12` Init Container Idempotency

**Issue:** `nodes-us-central1-c` in `dcr-experiments` carries the legacy `cloud.google.com/metadata-proxy-ready: "true"` node label, which schedules the `kube-system/metadata-proxy-v0.12` DaemonSet (`hostNetwork: true`). When `kops update cluster --yes` updates the DaemonSet on running nodes before they are replaced, the `update-ipdtables` init container executes `ip addr add dev lo 169.254.169.252/32`. Because `169.254.169.252/32` is already bound to `lo` on the host, the init container exits with `RTNETLINK answers: File exists` (`Init:CrashLoopBackOff`), and `kops rolling-update` refuses to start while a `system-node-critical` pod is pending.

**Solution:** Use `ip addr replace dev lo 169.254.169.252/32` in the `metadata-proxy-v0.12` init container so pod restarts on existing hosts succeed before node replacement begins.

---

### 3. Cilium Etcd Port `4003` Internal Load Balancer Forwarding Rule

**Issue:** In `dcr-experiments`, `cilium` runs with `etcdManaged: true` (`https://api.internal.dictycr-dev-staging-dcr-experiments-k8s.local:4003`). On worker nodes, `/etc/hosts` maps `api.internal.dictycr-dev-staging-dcr-experiments-k8s.local` to the internal load balancer VIP `10.0.16.2`. While `kops v1.36.1` creates three internal forwarding rules on `10.0.16.2` (`443`, `3988`, and `4003`), `kops v1.29.2` created only `api-us-central1-...` (`443`) and `kops-controller-us-central1-...` (`3988`). Without port `4003` forwarded on `10.0.16.2`, worker `cilium-agent` pods could not reach `etcd-cilium` on the control plane and restarted every 15 minutes (`Unable to connect to kvstore: timed out while waiting for etcd session`).

**Solution:** Create the missing internal TCP `4003` forwarding rule on `10.0.16.2` targeting the control-plane internal backend service `api-dictycr-dev-staging-dcr-experiments-k8s-local`:

```bash
gcloud compute forwarding-rules create cilium-etcd-us-central1-dictycr-dev-staging-dcr-experiments \
  --project dcr-experiments \
  --region us-central1 \
  --load-balancing-scheme INTERNAL \
  --network dictycr-dev-staging-dcr-experiments-k8s-local \
  --subnet us-central1-dictycr-dev-staging-dcr-experiments-k8s-local \
  --address api-us-central1-dictycr-dev-staging-dcr-experiments-k8s-local \
  --ip-protocol TCP \
  --ports 4003 \
  --backend-service api-dictycr-dev-staging-dcr-experiments-k8s-local \
  --backend-service-region us-central1
```

## Related Documents

- [OS Image Upgrades and Stateful Pool Drain Safeguards](os-image-upgrade.md)
- [Day-2 Operations Detail](day2-operations.md)
- [kOps Cluster Setup Guide](../../kops-setup.md)
