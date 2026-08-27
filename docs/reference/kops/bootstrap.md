# Cluster Bootstrap Detail

Back to: [kOps Cluster Setup](../../kops-setup.md)

## The Handoff

`bootstrap-bundle` is where Git takes over as source of truth. Inside an active `cluster-env` shell, `--cluster` and `--project` default from `CLUSTER_NAME`/`PROJECT_ID` — only `--api-access-cidr` needs typing, since there's no safe default for it (see [choosing the CIDR](#choosing---api-access-cidr) below). The recipe writes identity into YAML.

```bash
just gcp-cluster bootstrap-bundle --api-access-cidr "<your-ip>/32"
```

Outside that shell, or to target a different cluster than the active one, pass both explicitly:

```bash
just gcp-cluster bootstrap-bundle \
  --cluster <cluster-name> \
  --project <project-id> \
  --api-access-cidr "<your-ip>/32"
```

### What It Does

- Renders `config/kops/<cluster-name>/cluster.yaml` and `instancegroups.yaml` from `config/kops/_starter/`.
- Pre-configures repository defaults: CSI driver, pd-ssd etcd volumes, cluster autoscaler, cert-manager, node-local DNS, and 3 worker pools (`stateless-web`, `stateful-db`, `batch-spot`).
- **Pure local operation** — zero cloud API calls, zero state-store mutations.

## Choosing `--api-access-cidr`

Sets GCP firewall rules restricting Kubernetes API server access (`spec.kubernetesApiAccess`).

Find your public IPv4 and the ready-to-paste CIDR:

```bash
just gcp-cluster show-public-ip
```

The recipe rejects a private or non-IPv4 answer rather than emitting a CIDR that would silently lock you out.

| Situation | What to pass |
|-----------|--------------|
| Dynamic / home ISP (Comcast, Xfinity) | Your current public IP as `/32` — leases rotate, so expect to update it |
| Static VPN / office gateway | The fixed egress range, e.g. `198.51.100.0/24` |
| Open access `0.0.0.0/0` | Temporary dev/break-glass only — **not** for production |

> Never pass a private CIDR such as `192.168.0.0/16`, `10.0.0.0/8`, or `172.16.0.0/12`. GCP sees your NATed public address, so a private range matches nothing.

## Updating the CIDR After Your IP Changes

If a dynamic ISP reassigns your address, `kubectl` loses connectivity. Fix it the Git-native way:

1. Get the new address:
   ```bash
   just gcp-cluster show-public-ip
   ```
2. Update `spec.kubernetesApiAccess` in `config/kops/<cluster-name>/cluster.yaml`:
   ```yaml
   spec:
     kubernetesApiAccess:
       - "<new-ip>/32"
   ```
3. Commit and apply:
   ```bash
   git add config/kops/${CLUSTER_NAME}/cluster.yaml
   git commit -m "${CLUSTER_NAME}: update API access CIDR"
   just gcp-cluster apply-cluster
   ```

No rolling update is needed — this is a firewall rule change only ([change trigger matrix](day2-operations.md#change-trigger-matrix)). `apply-cluster` folds replace-manifests → plan-cluster → update-cluster → drift-manifests; see [day-2 operations](day2-operations.md) for what each step does.

## Generated Defaults

### `cluster.yaml`

| Field | Default |
|-------|---------|
| `spec.kubernetesVersion` | `1.28.8` |
| `spec.kubernetesApiAccess` | `["<your-ip>/32"]` |
| `spec.networking.cilium` | enabled |
| `spec.etcdClusters[*].volumeType` | `pd-ssd` |
| `spec.cloudProvider.gce.pdCSIDriver` | `enabled: true` |
| Hardening addons | `clusterAutoscaler`, `nodeProblemDetector`, `certManager`, `nodeLocalDNS` |

### `instancegroups.yaml`

| Pool | Role | Machine type | Size | Disk |
|------|------|--------------|------|------|
| `master-us-central1-c` | Control plane | `n1-custom-4-8192` | 1 | 75GB |
| `nodes` | Default workers | `n1-custom-2-4096` | 4 | 100GB |
| `stateless-web` | Web/API | `e2-standard-4` | min 3 / max 6 | 100GB |
| `stateful-db` | Databases | `n2-standard-4` | min 3 / max 3 | 100GB |
| `batch-spot` | Spot compute | `e2-standard-4` | min 0 / max 4 | preemptible |

> Production database workloads need an extra taint on `stateful-db` — see [`arangodb-deploy.md`](../../arangodb-deploy.md) and [pool requirements](../arangodb/pool-requirements.md).

## Version-Aware Preview & Apply

`plan-cluster` and `update-cluster` dispatch on the pinned kOps binary:

| Action | kOps ≤ 1.30.x (repo pin v1.29.2) | kOps ≥ 1.31.0 |
|--------|-----------------------------------|---------------|
| Preview (dry-run) | `kops update cluster --admin` | `kops reconcile cluster` |
| Apply | `kops update cluster --yes --admin` | `kops reconcile cluster --yes` |
| Rolling update | separate `kops rolling-update` if needed | included in `reconcile` |
| Validate | `kops validate cluster` | same |
