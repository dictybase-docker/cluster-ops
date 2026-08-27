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

Install everything pinned in the active manifest:

```bash
just install-tools
```

Verify afterwards:

```bash
just check-tools
```

## Which Manifest Is Used

`just install-tools` reads the asdf manifest named by `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME`:

| `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` | Manifest used |
|---------------------------------------|---------------|
| Unset | `.tool-versions` at the repo root (dev default) |
| Set | That per-cluster file, e.g. `.tool-versions.<env>.<cluster>` |

Both files are gitignored.

> This pin file is **not** `spec.kubernetesVersion`. The cluster's Kubernetes version lives in Git after bootstrap. Use a per-cluster pin file only when that cluster needs a different `kops`/`kubectl` binary than the repo default.

## Create a Per-Cluster Pin File

```bash
just pin-tool-versions --env <env> --cluster <cluster-name>
```

Copies `.tool-versions` to `.tool-versions.<env>.<cluster>`, refuses to overwrite an existing pin file, and prints the exact `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=` line to add to your env file.

Then re-enter the shell so the new variable is exported, and install:

```bash
exit
just cluster-env --env <env> --cluster <cluster-name>
just install-tools
```

## Upgrading kOps

Pins live in `.tool-versions`. Upgrade kOps through dedicated review PRs — the binary version changes which commands `plan-cluster` and `update-cluster` run, see [day-2 operations](day2-operations.md#kops-version-behavior).
