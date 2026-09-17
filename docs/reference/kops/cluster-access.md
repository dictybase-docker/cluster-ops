# Cluster Access — kubeconfig, k9s, Status

Back to: [kOps Cluster Setup](../../kops-setup.md)

## What It Does

`export-kubeconfig` writes an **admin** kubeconfig for the live cluster, pulling the cluster's CA and API endpoint from the kops state store. Everything that talks to the API afterwards — `kubectl`, `k9s`, Pulumi, the ArangoDB/Postgres recipes — reads that file through `KUBECONFIG`.

Run it once after [`create-cluster`](bootstrap.md) and again whenever the admin credential expires or the API endpoint changes.

## Command

```bash
just gcp-cluster export-kubeconfig
just gcp-cluster k9s
```

## Behavior

`export-kubeconfig` resolves its target before calling kops:

1. Cluster name — `--cluster`, else `CLUSTER_NAME` (exported by `just cluster-env`).
2. kops DNS name — `--kops-name`, else `KOPS_CLUSTER_NAME`, else `<cluster>-k8s.local`.
3. State store — `--state`, else `KOPS_STATE_STORE`, else `gs://kops-state-<cluster>`.
4. Output path — when `KUBECONFIG` is set, its parent directory is created (`mkdir -p`) and the path is passed as `--kubeconfig`. When `KUBECONFIG` is unset, no `--kubeconfig` flag is passed and kops merges into `~/.kube/config` instead.
5. Runs `kops export kubeconfig --admin` with the flags resolved above.

`create-cluster-env` sets `KUBECONFIG` to `clusters/<cluster-name>/kubeconfig` unless you pass `--kubeconfig`, inherit a non-empty `KUBECONFIG`, or an older `clusters/<project-id>/kubeconfig` already exists ([file isolation](file-isolation.md)).

## Flags

| Flag | Required | Default | Notes |
|------|:--------:|---------|-------|
| `--cluster` / `-c` | No | `$CLUSTER_NAME` | Short cluster name; seeds the two defaults below |
| `--kops-name` / `-n` | No | `$KOPS_CLUSTER_NAME`, else `<cluster>-k8s.local` | Full kops DNS name in `cluster.yaml` → `metadata.name` |
| `--state` / `-s` | No | `$KOPS_STATE_STORE`, else `gs://kops-state-<cluster>` | Must match `cluster.yaml` → `spec.configBase` |

Inside `just cluster-env` all three default correctly — the minimal invocation takes no flags.

## The Admin Credential

`--admin` is passed with no duration, so the certificate uses the kops default lifetime (kops re-issues on every export; it is a short-lived admin credential, not a permanent one). When it expires, `kubectl` fails with a TLS/authentication error — re-run `export-kubeconfig`.

For a longer-lived or separately named file:

```bash
just gcp-cluster export-named-kubeconfig --name <name> --duration-hours 24
```

> `export-named-kubeconfig` writes `<name>.yaml` into the current directory and passes **no** `--name`/`--state`. It works only when `KOPS_CLUSTER_NAME` and `KOPS_STATE_STORE` are exported in the shell — which the per-cluster env file deliberately does not do ([cluster env](cluster-env.md#git-owned-after-bootstrap)). Export both inline, or use `export-kubeconfig`.

## k9s and Status

| Recipe | Runs | Use |
|--------|------|-----|
| `just gcp-cluster k9s` | `k9s` | Terminal UI; reads `KUBECONFIG` and nothing else |
| `just gcp-cluster cluster-status` | `kubectl version`, `kubectl cluster-info`, `kubectl get nodes` | Fast non-interactive health glance |

`k9s` is one of the asdf-pinned tools — install it with `just prepare-tools` ([tool versions](tool-versions.md)).

## Related

- Post-create validation (`validate-cluster`, `validate-kops-ha`, `validate-hardening`) — [bootstrap](bootstrap.md)
- kubeconfig path convention — [file isolation](file-isolation.md)
