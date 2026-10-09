# Cluster Hardening Check Details

Back to: [kOps Cluster Setup](../../kops-setup.md)

## What It Does

Verifies the five post-provisioning hardening components: cluster-autoscaler,
node-problem-detector, metrics-server (reporting, not just installed),
cert-manager and the node-local DNS cache. Prints one line per component;
exits non-zero when any component is missing, partially ready or degraded.
Read-only, zero prompts.

## Behavior

1. cluster-autoscaler — pods with `app=cluster-autoscaler` in `kube-system` are Running.
2. node-problem-detector — DaemonSet `numberReady == desiredNumberScheduled` (partial readiness fails the gate).
3. metrics-server — the metrics API (`/apis/metrics.k8s.io/v1beta1/nodes`) answers; a not-serving API is `not reporting (wait 60s and retry)`, transport problems are `metrics API error: …`.
4. cert-manager — pods with `app.kubernetes.io/instance=cert-manager` in `kube-system` or `cert-manager` are Running.
5. node-local-dns — pods with `k8s-app=node-local-dns` in `kube-system` are Running.

Every component is reported even when earlier ones fail (no short-circuit).

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--kubeconfig` | No | `$KUBECONFIG` | Kubeconfig path |

## Notes

- Component checks are shared with `validate-kops-ha` via the exported `internal/custodian` checks.
- The pre-migration shell recipe remains as `validate-hardening-shell` for one release (rollback).
