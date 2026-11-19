# Falcon Sensor Install Details

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## What It Does

`deploy` applies the `falcon-sensor` stack. The stack creates these items:

1. Namespace `falcon-system` with the Pod Security labels `enforce`, `audit`, and `warn` set to `privileged`. The sensor pods are privileged, so a lower level blocks them.
2. Secret `falcon-sensor-cid` with `FALCONCTL_OPT_CID` and the optional `FALCONCTL_OPT_PROVISIONING_TOKEN`.
3. Helm release `falcon-sensor` (chart `falcon-sensor` **1.37.0**). The release creates DaemonSet `falcon-sensor` with the container `falcon-node-sensor`.

## Command

```bash
just falcon deploy
```

## Behavior

1. Make sure that `--namespace` matches `properties.namespace`.
2. Make sure that `properties.cloud`, `properties.cid`, and the mirrored image digest are set.
3. Make sure that the cluster has nodes and that all nodes are in `PROJECT_ID`.
4. Run `ensure-stack`, `preview`, and `create-resource`.
5. Wait until the DaemonSet has one ready pod on each node. The recipe shows a warning when a pod cannot pull its image.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` | No | `properties.namespace` | A different value stops the recipe before a change |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` / `--interval` | No | `60` / `10` | A 10-minute wait |

## Chart Values

| Value | Setting | Reason |
|-------|---------|--------|
| `node.image.repository` / `node.image.digest` | Artifact Registry mirror | The pods pull one fixed digest |
| `node.image.pullPolicy` | `IfNotPresent` | The digest does not change |
| `node.daemonset.tolerations` | `operator: Exists` | The sensor runs on control-plane, spot, and dedicated nodes |
| `node.clusterName` | `properties.clusterName` | kOps is a self-managed cluster |
| `falcon.cloud` | `properties.cloud` | The sensor connects to the tenant data center |
| `falconSecret` | Secret `falcon-sensor-cid` | The CID is not in the chart values |
| `container.enabled` | `false` | No sidecar injector |

The stack does not set `node.backend`. Chart 1.37 ignores it, and the sensor selects eBPF itself. The stack sets no resource requests or limits.

## Node Resources

Each node runs one more privileged pod. The `nodes` and control-plane groups use `e2-standard-2` machines. Before the first deploy, make sure that each node has free CPU and memory. A pod that stays `Pending` stops `deploy` at the end of the wait.
