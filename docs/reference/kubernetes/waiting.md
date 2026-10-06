# Kubernetes Wait Command Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

`cluster-ops wait` owns every condition wait the recipes run: poll loops with
progress logs on stderr, terminal-failure detection, and classified exit
codes. The shell recipes keep their names and flags and call
`go run ./cmd/cluster-ops wait <condition> …`.

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | condition met |
| `1` | timeout exceeded |
| `2` | terminal failure (fail fast — do not retry) |
| `3` | API error (connection, auth, missing resource type) |

## Conditions

| Subcommand | Waits for | Terminal (exit 2) |
|------------|-----------|-------------------|
| `pods-ready` | N pods behind a selector Running with every container ready | `CrashLoopBackOff` / `ImagePullBackOff` / `ErrImagePull` once a container has restarted ≥ 2 times |
| `job-complete` | Job `status.succeeded ≥ 1`; a Job seen once and then TTL-deleted counts as done | `status.failed ≥ 1` — tails the pod's last 100 log lines into the error |
| `resources-gone` | no resources of kind remain (missing namespace or API type counts as gone) | — |
| `deployment-rollout` | `observedGeneration == generation` AND updated/available replicas == desired | `ProgressDeadlineExceeded` |
| `custom-resource` | unstructured `status.<path>` == expected value | any `--failed-value` match (e.g. `Failed`) |
| `crd-established` | CRD reports `Established=True` | — |
| `storage-classes` | named StorageClasses exist with the expected provisioner | provisioner mismatch |
| `nodes-ready` | N nodes matching a selector report `Ready=True` | — |
| `machine-image` | GCP machine image reports `READY` (Compute SDK, ADC) | `FAILED` |

## Shared Flags

| Flag | Default | Notes |
|------|---------|-------|
| `--timeout` | `900` | Wait budget in seconds |
| `--interval` | `5` | Seconds between polls |
| `--kubeconfig` / `--context` | `$KUBECONFIG` / current-context | Kubernetes conditions only |

## Examples

```bash
go run ./cmd/cluster-ops wait pods-ready --namespace prod --selector arango_deployment=arangodb --count 9 --timeout 900
go run ./cmd/cluster-ops wait job-complete --namespace prod --job reset-root-password --timeout 900
go run ./cmd/cluster-ops wait custom-resource --namespace prod --name logto \
    --group postgresql.cnpg.io --version v1 --kind Cluster \
    --status-path phase --expected-value "Cluster in healthy state" --failed-value Failed
go run ./cmd/cluster-ops wait machine-image --project dcr-kube1 --name dcr-kube1-vanilla --timeout 600
```

Progress lines go to stderr (`Waiting for 9 pod(s) in prod …: 3/9 ready (30s elapsed)`), so
piped stdout stays clean.