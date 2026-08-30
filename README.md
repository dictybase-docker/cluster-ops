# cluster-ops

Git-first infrastructure for a kOps-managed Kubernetes cluster on GCP, plus the Pulumi stacks that run on top of it.

Everything is driven through `just` recipes. Cluster identity and shape live in version-controlled YAML under `config/kops/<cluster>/` — not in anyone's shell history.

## Table of Contents

- [Start Here](#start-here)
- [What You Get](#what-you-get)
- [Quick Start](#quick-start)
- [Repository Layout](#repository-layout)
- [Conventions](#conventions)
- [Discovering Recipes](#discovering-recipes)
- [Status](#status)

## Start Here

| I want to… | Go to |
|------------|-------|
| Stand up a cluster from scratch | [`docs/kops-setup.md`](docs/kops-setup.md) |
| Wire up Pulumi and apply StorageClass | [`docs/pulumi-setup.md`](docs/pulumi-setup.md) |
| Deploy production ArangoDB | [`docs/arangodb-deploy.md`](docs/arangodb-deploy.md) |
| Deploy production PostgreSQL 16 (CloudNativePG) | [`docs/postgres-deploy.md`](docs/postgres-deploy.md) |
| Understand the architecture and cost trade-offs | [`docs/kops-gcp-architecture.md`](docs/kops-gcp-architecture.md) |

Run them in that order for a greenfield project. Each guide opens with a Quick Reference block if you have done it before.

## What You Get

- **kOps cluster on GCE** — HA control plane, Cilium networking, pd-ssd etcd, PD CSI driver, and hardening addons (cluster autoscaler, node problem detector, cert-manager, node-local DNS).
- **Three worker pools** — `stateless-web`, `stateful-db`, and a preemptible `batch-spot` pool.
- **Pulumi state backend** — versioned GCS bucket with a KMS-encrypted secrets provider, one stack per cluster.
- **Production ArangoDB** — 9-member Cluster on the `stateful-db` pool, with scheduled restic backups to GCS and an in-cluster restore path.

## Quick Start

```bash
# Verify the core system tools (go, just, gcloud, jq, envsubst)
just check-tools --skip-asdf yes

# Create and enter a per-cluster environment
just create-cluster-env --env <env> --cluster <cluster-name> --project <project-id>
just cluster-env --env <env> --cluster <cluster-name>
```

Then follow [`docs/kops-setup.md`](docs/kops-setup.md).

## Repository Layout

| Path | Contents |
|------|----------|
| `config/kops/<cluster>/` | Canonical cluster manifests — the source of truth |
| [`config/kops/_starter/`](config/kops/_starter/) | Templates `bootstrap-bundle` renders new clusters from |
| [`just_modules/`](just_modules/) | `just` recipe modules ([`gcp-cluster`](just_modules/cluster.justfile), [`gcp-pulumi`](just_modules/pulumi.justfile), [`arangodb`](just_modules/arangodb.justfile), …) |
| [`docs/`](docs/) | Provisioning guides |
| [`docs/reference/`](docs/reference/) | Detailed reference for each guide |
| `credentials/<project-id>/` | Service-account keys and SSH keypairs (gitignored) |
| `clusters/<project-id>/` | Exported kubeconfigs (gitignored) |
| [`gcs-files/`](gcs-files/) | API lists and IAM role definitions |

Pulumi projects live in their own top-level directories ([`storage_class/`](storage_class/), [`arangodb-cluster/`](arangodb-cluster/), [`arangodb-backup/`](arangodb-backup/), …), each with `Pulumi.yaml` plus one `Pulumi.<stack>.yaml` per cluster.

## Conventions

- **One GCP project = one cluster.** Credentials and kubeconfigs are scoped per project ID.
- **Git owns cluster identity** after bootstrap. If the env file and `cluster.yaml` disagree, Git wins.
- **Secrets never land in Git.** Per-cluster env files, SA keys, and kubeconfigs are gitignored; Pulumi secrets are KMS-encrypted in state.
- **Recipes fail loudly.** Production recipes require an explicit stack or cluster name rather than falling back to a default.
- **Documentation follows [`docs/STYLE.md`](docs/STYLE.md)** — lean guides linked to `docs/reference/` detail docs.

## Discovering Recipes

```bash
just --list                  # top-level recipes and modules
just --list gcp-cluster      # cluster lifecycle
just --list gcp-pulumi       # Pulumi backend and stacks
just --list arangodb         # ArangoDB deploy, backup, restore
```

Verification recipes are read-only and exit non-zero on failure:

```bash
just check-tools                    # local toolchain
just gcp-cluster validate-cluster   # cluster health
just gcp-pulumi check-backend       # Pulumi backend wiring
just gcp-pulumi check-storageclass  # StorageClasses
just arangodb check-pool            # stateful-db node pool
just arangodb verify                # full ArangoDB install
```

## Status

| Area | State |
|------|-------|
| Tooling & infrastructure recipes | Tested 2026-08-20 |
| Declarative Git bundle ([`_starter`](config/kops/_starter/)) | Cross-version verified on kops v1.29.2 and v1.36.1; full end-to-end re-creation pending a first live-target run |
| Pulumi backend recipes | Aligned with this repo as of 2026-08-24 |
| Production ArangoDB | Prod configs exist for operator, cluster, databases, backup, restore. Gaps: production kOps bundle, loader prod stacks, PDBs |

> The cluster setup walkthrough previously lived in this file. It now has its own guide: [`docs/kops-setup.md`](docs/kops-setup.md).
