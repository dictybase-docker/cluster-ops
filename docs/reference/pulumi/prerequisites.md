# Pulumi Setup Prerequisites

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## Cluster Handoff

Finish [`kops-setup.md`](../../kops-setup.md) first. This guide starts where the kOps guide ends — cluster up and validated through [Step 3.7](../../kops-setup.md#step-37--validate-ha-topology--hardening).

Required state:

| Item | Check |
|------|-------|
| Cluster env file active | `just cluster-env --env <env> --cluster <cluster-name>` |
| Nodes healthy | `kubectl get nodes` all Ready |
| CSI for GCE PD enabled | `spec.cloudProvider.gce.pdCSIDriver.enabled: true` |

> Without the PD CSI driver, PVCs stay Pending forever. Enable it before applying any StorageClass.

### Storage-friendly Topology

Only matters if this cluster will host databases:

- **Lab** — a small worker pool is enough.
- **Production HA** — private topology + multi-zone workers + `stateful-db` InstanceGroup. See [README Step 3.2](../../kops-setup.md#step-32--customize-yaml-in-git), [Step 3.7](../../kops-setup.md#step-37--validate-ha-topology--hardening), and [`kops-gcp-architecture.md`](../../kops-gcp-architecture.md).

## Tools

Same baseline as the kOps guide — see [README §1.2](../../kops-setup.md#12-core-system-tools) and [§1.4](../../kops-setup.md#14-asdf-managed-tools).

| Tool | Source | Required |
|------|--------|----------|
| `just` | system | Yes |
| `kubectl` | asdf | Yes |
| `pulumi` | asdf | Yes |
| `gcloud` | asdf | Yes |
| `jq` | system | Yes |
| `direnv` | system | Optional |

Install a pinned version into the active tool-versions file:

```bash
just install-tool --name <tool> --version <version>
```

Verify everything at once:

```bash
just gcp-pulumi check-tools
```

## Access

| Need | Why |
|------|-----|
| Working kubeconfig | Pulumi Kubernetes provider applies StorageClass |
| IAM to create SA keys / KMS / GCS | Pulumi manager identity and state backend |
| Paths under `credentials/<project-id>/` | Per-project isolation ([README §1.5](../../kops-setup.md#15-per-project-file-isolation)) |

> **Do not** put Pulumi credentials in `.envrc`. They belong in the per-cluster env file only, which is gitignored.

## One Project = One Cluster

Same rule as the kOps guide — see [README §1.1](../../kops-setup.md#11-the-gcp-project) and [§1.5](../../kops-setup.md#15-per-project-file-isolation).
