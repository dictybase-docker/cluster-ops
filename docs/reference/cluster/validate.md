# Cluster Validation Check Details

Back to: [kOps Cluster Setup](../../kops-setup.md)

## What It Does

Wraps `kops validate cluster` with classified outcomes: success, timeout
budget exceeded, missing `kops` binary and command failures are distinct
results with named causes. Read-only — it only runs kops' own validation.

## Behavior

1. Resolves the kops DNS name: `--kops-name`, then `$KOPS_CLUSTER_NAME`, then `<cluster>-k8s.local`.
2. Resolves the state URI: `--state`, then `$KOPS_STATE_STORE`, then `gs://kops-state-<cluster>`.
3. Runs `kops validate cluster --name=<kn> --state=<st> --wait <waittime>m`.
4. Classifies the outcome by error type: missing binary → `kops not found on PATH — run just prepare-tools`; budget exceeded → `timed out after <n>m waiting for <kn>`; non-zero exit → `kops validate cluster failed: …` with a Fix line; anything else → `kops validate cluster error: …`.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--cluster` | No | `$CLUSTER_NAME` | Cluster name |
| `--kops-name` | No | `$KOPS_CLUSTER_NAME` / `<cluster>-k8s.local` | Full kops DNS name |
| `--state` | No | `$KOPS_STATE_STORE` / `gs://kops-state-<cluster>` | Kops state URI |
| `--waittime` | No | `20` | Minutes to wait |

## Notes

- The pre-migration shell recipe remains as `validate-cluster-shell` for one release (rollback).
