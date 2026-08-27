# Per-Project File Isolation & SSH

Back to: [kOps Cluster Setup](../../kops-setup.md)

## Per-Project File Isolation

One GCP project hosts exactly one cluster. Scope cluster-specific files to the project ID:

| Artifact | Location |
|----------|----------|
| SSH keys | `credentials/${PROJECT_ID}/` |
| Service-account JSON | `credentials/${PROJECT_ID}/` |
| kubeconfig | `clusters/${PROJECT_ID}/` |

Paths are relative to the repo root. `create-cluster-env --project <id>` defaults `KUBECONFIG` to `clusters/<project-id>/kubeconfig`.

```bash
SSH_KEY="${PWD}/credentials/${PROJECT_ID}/k8sVM.pub"
KUBECONFIG="${PWD}/clusters/${PROJECT_ID}/kubeconfig"
```

`just gcp-cluster export-kubeconfig` writes `$KUBECONFIG` when that variable is set.

## SSH Keypair

Generate a dedicated Ed25519 keypair (RSA-4096 via `--type rsa`). The recipe refuses to overwrite an existing key:

```bash
just gcp-cluster generate-ssh-key --project <project-id>
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
