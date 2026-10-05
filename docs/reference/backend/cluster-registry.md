# Cluster Registry Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

The registry is one YAML file per cluster — `config/clusters/<cluster>.yaml` —
holding every value the backend deploy recipes and the `just ci` helpers need.
It is the single source of truth: no downstream recipe re-derives or hardcodes
these values. Adding a cluster is adding this file; there is deliberately no
generator recipe (a single file with eleven keys — a generator would be longer
than the file).

`just gcp-cluster registry-show` is the read path. It is read-only and fails
closed on every malformed entry.

## Behavior

1. Resolves the entry `config/clusters/<cluster>.yaml`; a missing file fails
   with the expected path and the fix (add the file).
2. Reads all eleven required keys — a missing or empty key fails, naming it.
3. Requires `kops_state` and `pulumi_state` to be concrete `gs://` URIs. An
   env-var reference (for example `${PULUMI_STATE_STORAGE}`) fails — the
   registry must be self-contained data.
4. Requires `kms_secrets_provider` to be a `gcpkms://` URI.
5. Requires `ci_env` to be uppercase (it becomes a GitHub variable prefix).
6. Prints one `key=value` line per entry and the next step
   (`scaffold-backend-stack`).

## Keys

| Key | Required | Notes |
|-----|----------|-------|
| `cluster` | Yes | Cluster name; also the registry filename stem |
| `stack` | Yes | Pulumi stack name deploy recipes target for this cluster |
| `kops_state` | Yes | `gs://` kOps state bucket |
| `gcp_project` | Yes | GCP project id |
| `kms_secrets_provider` | Yes | `gcpkms://` URI of the Pulumi secrets key |
| `pulumi_state` | Yes | `gs://` Pulumi state bucket — concrete URI, never an env reference |
| `namespace` | Yes | Namespace backend services deploy into |
| `ci_env` | Yes | Uppercase GitHub variable prefix (`PROD` → `PROD_CLUSTER`, …) |
| `kops_version` | Yes | kOps version used to manage this cluster |
| `kubectl_version` | Yes | kubectl version used against this cluster |
| `pulumi_version` | Yes | Pulumi CLI version used for deployments |

The three version keys exist because different clusters need different tool
versions (dcr-kube1 runs Kubernetes 1.35.8, dcr-experiments 1.28.8). CI deploys
read them through `<CI_ENV>_KOPS_VERSION` / `<CI_ENV>_KUBECTL_VERSION` /
`<CI_ENV>_PULUMI_VERSION` GitHub variables, published by
[`sync-deploy-vars`](ci-variables.md). The Justfile defaults in dagger-of-dcr
are only a fallback for clusters that pass no version.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--cluster` / `-c` | Yes* | `$CLUSTER_NAME` | Cluster name; resolves `config/clusters/<name>.yaml` |

*One of `--cluster` or `$CLUSTER_NAME` (set inside
[`cluster-env`](../kops/cluster-env.md)) must be present.

## Warnings

- **Never hardcode these values in another recipe.** Read the registry (or the
  GitHub variables it publishes) instead — a second copy is how clusters drift.
- **Never store secrets in the registry.** Credentials live in Kubernetes
  Secrets and GitHub secrets, not here.
- The dcr-kube1 tool versions must fit the cluster's Kubernetes version
  (1.35.8 today). When the cluster upgrades, update this file and re-run
  `just ci sync-deploy-vars` in the same change.
