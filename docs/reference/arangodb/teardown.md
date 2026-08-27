# ArangoDB Teardown Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## Warning

**Destructive. Clone only.**

## Command

```bash
just arangodb teardown --namespace prod --delete-pvcs yes
```

## Behavior

Order of operations:
1. `_destroy-stack` cluster
2. `_wait-gone` CR/pods/Services
3. `_destroy-stack` operator
4. `_wait-gone` operator pods
5. `_remove-pvcs`
6. `_report-clean`

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--namespace` | Yes | Target namespace |
| `--delete-pvcs` | No | Omit to leave leftover disks (warning, exit 0). Set `yes` to delete |

Stack comes from `$PULUMI_STACK`.

## What It Leaves Alone

- Backup stack (`arangodb-backup`)
- Loaders
- Restore stack (`arangodb-restore`)
- `backup_secrets`
- `stateful-db` pool — see [README §4](../../../README.md#4-day-2-operations-git-first-workflow) / [§6](../../../README.md#6-disposable-cluster-lifecycle)
