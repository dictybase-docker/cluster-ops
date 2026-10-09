# Cluster Verification Details

Back to: [kOps Cluster Setup](../../kops-setup.md)

## What It Does

Composite gate: runs the three cluster validations in one pass —
`kops validate cluster` (health), the HA topology suite (control-plane zones,
batch taints, shared component checks) and the hardening addons. Prints every
check from all three groups; exits non-zero when any group fails. Read-only.

## Behavior

1. The recipe ensures the per-cluster kubeconfig exists (exports it from the state store when missing — the one fix-up the wrapper keeps).
2. Group 1 — kops validation (see [Cluster Validation Check Details](validate.md)); runs even without a kubeconfig.
3. Group 2 — HA topology, 7 checks (shared with `validate-kops-ha` via `internal/custodian`).
4. Group 3 — hardening addons, 5 checks (see [Cluster Hardening Check Details](hardening.md)).
5. When the kubeconfig cannot be loaded, groups 2 and 3 each report `not run — kubeconfig missing (run just gcp-cluster export-kubeconfig)`; nothing silently skips.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--cluster` | No | `$CLUSTER_NAME` | Cluster name |
| `--kops-name` | No | `$KOPS_CLUSTER_NAME` / `<cluster>-k8s.local` | Full kops DNS name |
| `--state` | No | `$KOPS_STATE_STORE` / `gs://kops-state-<cluster>` | Kops state URI |
| `--waittime` | No | `20` | Minutes to wait |
| `--kubeconfig` | No | `$KUBECONFIG` | Kubeconfig path |

## Notes

- The pre-migration shell recipe remains as `verify-cluster-shell` for one release (rollback).
