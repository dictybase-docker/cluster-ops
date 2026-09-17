# Per-Project File Isolation & SSH

Back to: [kOps Cluster Setup](../../kops-setup.md)

## Per-Project File Isolation

One GCP project hosts exactly one cluster. Scope cluster-specific files to the project ID:

| Artifact | Location |
|----------|----------|
| SSH keys | `credentials/${PROJECT_ID}/` |
| Service-account JSON | `credentials/${PROJECT_ID}/` |
| kubeconfig | `clusters/<cluster-name>/` |

Paths are relative to the repo root. Because one project hosts exactly one cluster, the two namings identify the same thing; the recipes just pick different keys.

`create-cluster-env` resolves `KUBECONFIG` in this order: `--kubeconfig`, then a non-empty inherited `KUBECONFIG`, then an existing `clusters/<cluster-name>/kubeconfig`, then an existing `clusters/<project-id>/kubeconfig` (legacy layout), else the default `clusters/<cluster-name>/kubeconfig`.

```bash
SSH_KEY="${PWD}/credentials/${PROJECT_ID}/k8sVM.pub"
KUBECONFIG="${PWD}/clusters/${CLUSTER_NAME}/kubeconfig"
```

`just gcp-cluster export-kubeconfig` writes `$KUBECONFIG` when that variable is set, creating the parent directory first — [cluster access](cluster-access.md).

## SSH Keypair

Generate a dedicated Ed25519 keypair (RSA-4096 via `--type rsa`). The recipe refuses to overwrite an existing key. Inside an active `cluster-env` shell it needs no flags — the key path is derived from `PROJECT_ID`:

```bash
just gcp-cluster generate-ssh-key
```

Creates:

| File | Path |
|------|------|
| Private | `credentials/<project-id>/k8sVM` |
| Public | `credentials/<project-id>/k8sVM.pub` |

### Which Path Wins

1. If `SSH_KEY` is already in the activated env, that path wins.
2. Otherwise `--project` (or `PROJECT_ID`) builds `credentials/${PROJECT_ID}/k8sVM`.

Persist the **public** path in the env file — either `--ssh-key` on `create-cluster-env`, or edit the file and re-enter `just cluster-env`.

The public key is uploaded into kops state as a secret during [bootstrap](bootstrap.md), and must be re-uploaded after any teardown ([recreation](recreation.md)).
