# asdf-Managed Tools

Back to: [kOps Cluster Setup](../../kops-setup.md)

## Pinned Tools

`asdf` pins exact versions of these:

| Tool | Purpose | Docs |
|------|---------|------|
| `kubectl` | Talk to the Kubernetes API | [kubernetes.io](https://kubernetes.io/docs/reference/kubectl/kubectl/) |
| `kops` | Provision and manage the cluster | [kops.sigs.k8s.io](https://kops.sigs.k8s.io/) |
| `pulumi` | Deploy the application stack | [pulumi.com](https://www.pulumi.com/docs/) |
| `velero` | Backup and restore | [velero.io](https://velero.io/docs/) |
| `helm` | Kubernetes package manager | [helm.sh](https://helm.sh/docs/) |
| `k9s` | Terminal cluster UI | [k9scli.io](https://k9scli.io/) |
| `mc` | MinIO client (S3 management & backups) | [min.io](https://min.io/docs/minio/linux/reference/minio-mc.html) |

Install everything pinned in the active manifest, then verify — one recipe folds both:

```bash
just prepare-tools
```

Equivalent to running `just install-tools` then `just check-tools` separately; use those two directly if you only want one half (e.g. `check-tools` alone as a repeatable health probe).

## Which Manifest Is Used

`create-cluster-env` creates `.tool-versions.<env>.<cluster>` when that file does not exist, then writes its name to `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` in the matching env file. Existing per-cluster manifests are preserved unchanged.

`just prepare-tools` therefore uses the active cluster manifest:

| Environment | Manifest used |
|-------------|---------------|
| Inside `cluster-env` | `.tool-versions.<env>.<cluster>` |
| Outside `cluster-env` | `.tool-versions` at the repo root |

Both files are gitignored. `--force yes` replaces the env file but never overwrites an existing per-cluster manifest.

> This tool manifest is **not** `spec.kubernetesVersion`. The cluster's Kubernetes version lives in Git after bootstrap. The per-cluster manifest pins the command-line tools used for that cluster.

## Per-Cluster Manifest Lifecycle

Create and activate the cluster environment:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> --project <project-id>
just cluster-env --env <env> --cluster <cluster-name>
```

Then install and verify the versions selected for that cluster:

```bash
just prepare-tools
```

To change one tool later, run `just install-tool` inside the active cluster shell. It updates the active per-cluster manifest and installs the requested version:

```bash
just install-tool --name <tool> --version <version>
```

## Upgrading kOps

Pins live in `.tool-versions.<env>.<cluster>`. Upgrade kOps through dedicated review PRs — the binary version changes which commands `plan-cluster` and `update-cluster` run, see [day-2 operations](day2-operations.md#kops-version-behavior).
