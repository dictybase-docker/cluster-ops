# Declarative Re-Creation

Back to: [kOps Cluster Setup](../../kops-setup.md)

Rebuild a torn-down cluster from the Git bundle. Everything needed survives teardown except the VMs themselves and the SSH secret ([what survives](teardown.md#what-survives-teardown)).

## Step 0 — Re-enter the Operator Env

The one step that genuinely can't be folded — a shell that just exited (or never existed) can't remember `--env`/`--cluster` for you.

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

## Steps 1–5 — One Recipe

```bash
just gcp-cluster create-cluster
```

This is the **same recipe** used at [initial bootstrap](../../kops-setup.md#3-cluster-bootstrap-git-native-flow) — recreation and first-time creation share the same precondition (no live cluster, SSH secret not yet uploaded), so they share the same steps:

| Step | Equivalent standalone recipe |
|------|-------------------------------|
| 1. Recreate state bucket (idempotent \u2014 no-ops if it survived teardown) | `just gcp-cluster create-state-bucket` |
| 2. Push the Git bundle to the state store | `just gcp-cluster replace-manifests --force yes` |
| 3. Restore SSH secret (mandatory \u2014 teardown wipes it, unlike the bucket) | `just gcp-cluster upload-ssh-secret` |
| 4. Preview and provision | `just gcp-cluster plan-cluster` then `just gcp-cluster update-cluster` |
| 5. Validate | `just gcp-cluster validate-cluster`, `validate-kops-ha`, `validate-hardening` |

All five steps default `--cluster`/`--project`/etc. from the active shell — no flags needed beyond what step 0 already established.

Use the standalone recipes directly only if `create-cluster` fails partway and you need to resume from a specific step (e.g. the bucket recreated fine but the SSH upload needs a different `--ssh-key` path).

## After the Cluster Is Back

The Kubernetes layer is empty — StorageClasses and application stacks are Pulumi-managed and must be reapplied:

1. [`pulumi-setup.md`](../../pulumi-setup.md) — backend and StorageClass
2. [`arangodb-deploy.md`](../../arangodb-deploy.md) — production ArangoDB
