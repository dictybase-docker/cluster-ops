# PostgreSQL Teardown Details

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## Warning

**Destructive. Clone only.** Deletes the data PVCs (all database files) and the backup bucket with **every backup in it** (`ForceDestroy: true`).

## Command

```bash
just postgres teardown --namespace prod --delete-pvcs yes --delete-backups yes
```

Both `yes` flags are mandatory — the recipe refuses to run otherwise, so neither the data loss nor the backup loss is silent.

## Behavior

Order of operations:
1. `pulumi destroy` on the `cloudnative-pg-cluster` stack — removes the Cluster CR, ScheduledBackup, Secrets, and the backup bucket
2. Deletes leftover `cnpg.io/cluster=<name>` PVCs in the namespace

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--namespace` | Yes | Target namespace |
| `--cluster` | No | Cluster name (default `logto`) — used for the PVC selector |
| `--delete-pvcs` | Yes | Must be `yes` — deletes data PVCs |
| `--delete-backups` | Yes | Must be `yes` — acknowledges the bucket destroy |

Stack comes from `$PULUMI_STACK`.

## What It Leaves Alone

- Operator stack (`cloudnative-pg-operator`) — destroy by hand with `just gcp-pulumi remove-resource --folder cloudnative-pg-operator` once no Cluster CRs remain anywhere; CRDs vanish with it
- `postgres-backup-sa` service account and its IAM binding — prune with `gcloud` if the install is permanent
- `prod` / `operators` namespaces
- `stateful-db` pool — see [kops-setup.md §4](../../kops-setup.md#4-day-2-operations-git-first-workflow)

## Keeping the Backups

To destroy the database but keep the bucket, remove the bucket from Pulumi state **before** teardown so the destroy skips it:

```bash
pulumi -C cloudnative-pg-cluster -s "$PULUMI_STACK" state delete <bucket-urn>
```

Find the URN with `pulumi -C cloudnative-pg-cluster -s "$PULUMI_STACK" stack --show-urns`.
