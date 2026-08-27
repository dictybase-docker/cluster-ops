# Declarative Re-Creation

Back to: [kOps Cluster Setup](../../kops-setup.md)

Rebuild a torn-down cluster from the Git bundle. Everything needed survives teardown except the VMs themselves ([what survives](teardown.md#what-survives-teardown)).

## Step 0 — Re-enter the Operator Env

```bash
just cluster-env --env <env> --cluster <cluster-name>
```

Recreate the env file first if it is gone:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> \
  --project <project-id> \
  --credentials credentials/<project-id>/kops-cluster-creator.json \
  --ssh-key credentials/<project-id>/k8sVM.pub
```

## Step 1 — Recreate State Bucket

Only if it was deleted ([bucket deletion](teardown.md#optional-delete-the-state-bucket)):

```bash
just gcp-cluster create-state-bucket \
  --project <project-id> \
  --bucket-name kops-state-<cluster-name>
```

## Step 2 — Push the Git Bundle to the State Store

```bash
just gcp-cluster replace-manifests --cluster <cluster-name> --force yes
```

## Step 3 — Restore SSH Secret

**Mandatory after teardown** — the secret lives in kops state, not in Git:

```bash
just gcp-cluster upload-ssh-secret \
  --cluster <cluster-name> \
  --ssh-key credentials/<project-id>/k8sVM.pub
```

## Step 4 — Preview and Provision

```bash
just gcp-cluster plan-cluster --cluster <cluster-name>
just gcp-cluster update-cluster --cluster <cluster-name>
```

## Step 5 — Validate

```bash
just gcp-cluster validate-cluster
just gcp-cluster validate-kops-ha
just gcp-cluster validate-hardening
```

## After the Cluster Is Back

The Kubernetes layer is empty — StorageClasses and application stacks are Pulumi-managed and must be reapplied:

1. [`pulumi-setup.md`](../../pulumi-setup.md) — backend and StorageClass
2. [`arangodb-deploy.md`](../../arangodb-deploy.md) — production ArangoDB
