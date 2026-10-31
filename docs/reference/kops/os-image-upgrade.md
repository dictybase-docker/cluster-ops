# OS Image Upgrades and Stateful Pool Drain Safeguards

Back to: [kOps Cluster Setup](../../kops-setup.md)

## Upgrade Procedure

Run `just gcp-cluster check-cves` inside `just cluster-env` to check whether the cluster's manifest or live GCE node boot disks lag behind the latest Ubuntu LTS security image in GCP and to scan running workloads with `trivy k8s` (`trivy` is an asdf-managed tool — install it with `just install-tool --name trivy --version 0.76.0`; see [asdf-managed tools](tool-versions.md)). When a newer image is available, update `spec.image` across every instance group in `config/kops/${CLUSTER_NAME}/instancegroups.yaml`, commit the change, apply the manifest bundle, and roll the nodes.

```bash
# 0. One-time: install trivy in the active cluster tool manifest
just install-tool --name trivy --version 0.76.0

# 1. Check node boot-image currency against ubuntu-os-cloud and scan workloads
just gcp-cluster check-cves

# 2. Update spec.image in config/kops/${CLUSTER_NAME}/instancegroups.yaml and commit
$EDITOR config/kops/${CLUSTER_NAME}/instancegroups.yaml
git commit -am "${CLUSTER_NAME}: upgrade node image"

# 3. Push manifests, reconcile cloud templates, and verify zero drift
just gcp-cluster apply-cluster

# 4. Confirm all instance groups report NEEDUPDATE=0 and sync pool labels
just gcp-cluster rolling-update --yes yes
```

---

## Stateful Pool Drain Issues and Safeguards

The `stateful-db` pool hosts ArangoDB (`agents`, `dbservers`, `coordinators`), CloudNativePG PostgreSQL (`logto`), MinIO, and Redis across three zones (`us-central1-a`, `us-central1-b`, `us-central1-c`). Three failure modes can stall node replacement on this pool if the cluster and datastores are not configured for drain operations.

| # | Failure mode | Symptom during node roll | Safeguard in `cluster-ops` |
|---|---|---|---|
| 1 | Replacement GCE VM registers without `pool=database` label and taint | Evicted pods from the first rolled node stay `Pending`. ArangoDB PodDisruptionBudgets (`minAvailable: 2`) drop to `0` allowed disruptions and block the next node drain. | `update-cluster` and `rolling-update` run an in-roll background loop that applies `pool=database` and `dedicated=database:NoSchedule` as soon as a new `stateful-db` node joins. |
| 2 | Single-instance PostgreSQL PodDisruptionBudget (`minAvailable: 1`) | Eviction of `logto-1` fails with `Cannot evict pod as it would violate the pod's disruption budget` until the 15-minute drain timeout expires. | `cloudnative-pg-cluster` sets `spec.enablePDB: false` when `instances` is `1` and `true` only when `instances` is greater than `1`. |
| 3 | ArangoDB default `terminationGracePeriodSeconds: 3600` on Coordinators and DBServers | The Agent pod (`grace: 60s`) exits first on the draining node. The operator cannot contact the missing Agent to clear the `database.arangodb.com/graceful-shutdown` finalizer on the Coordinator and DBServer pods, so drain stalls for 15 minutes per node. | `arangodb-cluster` sets `terminationGracePeriodSeconds: 120` on `agents`, `dbservers`, and `coordinators` (override with `properties.<group>.terminationGracePeriodSeconds`). |

---

### 1. In-Roll Node Pool Label and Taint Sync

**Issue:** GCE Managed Instance Groups do not propagate kOps `spec.nodeLabels` to replacement VMs and reject `spec.taints` keys that contain slashes. If `sync-node-pools` runs only after the entire cluster roll finishes, the first replaced `stateful-db` node remains unlabeled while `kops` tries to drain the second `stateful-db` node. Because pods from the first node cannot schedule onto the unlabeled replacement VM, PodDisruptionBudgets block eviction on the second node.

**Solution:** Both `just gcp-cluster update-cluster` and `just gcp-cluster rolling-update --yes yes` start a background loop before `kops` begins node replacement. Every 5 seconds, the loop labels any unlabeled `kops.k8s.io/instancegroup=stateful-db` node with `pool=database` and applies the `dedicated=database:NoSchedule` taint. When the roll exits, a shell `trap` stops the background loop and runs `just gcp-cluster sync-node-pools` for final verification.

---

### 2. Single-Instance CloudNativePG PodDisruptionBudget

**Issue:** CloudNativePG creates a primary `PodDisruptionBudget` (`<cluster>-primary` with `minAvailable: 1`) by default. When a cluster runs with `instances: 1` (such as `logto` on `dcr-kube1`), `ALLOWED DISRUPTIONS` is permanently `0`. Kubernetes Eviction requests cannot evict `logto-1` during a node drain.

**Solution:** The `cloudnative-pg-cluster` stack sets `spec.enablePDB` from the replica count:

- `instances: 1` → `spec.enablePDB: false` (no `PodDisruptionBudget` resource is created, so node drain evicts the single pod cleanly and reschedules it).
- `instances > 1` → `spec.enablePDB: true`.

Reconcile an existing PostgreSQL cluster with:

```bash
just gcp-pulumi preview --folder cloudnative-pg-cluster
just gcp-pulumi create-resource --folder cloudnative-pg-cluster
```

---

### 3. ArangoDB Termination Grace Period

**Issue:** `kube-arangodb` sets `terminationGracePeriodSeconds: 60` on `agent` pods and `terminationGracePeriodSeconds: 3600` (1 hour) on `coordinator` and `dbserver` pods, and attaches the `database.arangodb.com/graceful-shutdown` finalizer to every member pod. When `kops` drains a `stateful-db` node, it evicts the `agent`, `coordinator`, and `dbserver` pods on that node at the same time. The `agent` pod terminates after 60 seconds, which prevents the operator from completing agency leadership resignation before the 15-minute `kops` drain timeout expires.

**Solution:** The `arangodb-cluster` stack sets `terminationGracePeriodSeconds: 120` on all three member groups (`agents`, `dbservers`, and `coordinators`) by default. You can override this value per group in `arangodb-cluster/Pulumi.<stack>.yaml` under `properties.agents.terminationGracePeriodSeconds`, `properties.dbservers.terminationGracePeriodSeconds`, or `properties.coordinators.terminationGracePeriodSeconds`.

Reconcile an existing ArangoDB cluster without rotating passwords:

```bash
just arangodb update-cluster
```

---

## Related Documents

- [Legacy kOps 1.29 Cluster Upgrades (`dcr-experiments`)](legacy-cluster-upgrade.md)
- [Day-2 Operations Detail](day2-operations.md)
- [kOps Cluster Setup Guide](../../kops-setup.md)

