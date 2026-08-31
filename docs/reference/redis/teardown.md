# Redis Teardown Details

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## Warning

**Destructive. Clone only.** Deletes the data PVC — all Redis data.

## Command

```bash
just redis teardown --namespace prod --delete-pvc yes
```

## Behavior

Order of operations:
1. `pulumi destroy` on the `redis-standalone` stack — removes the Deployment, Service, and Secret `redis-auth`
2. Deletes the leftover `redis-data` PVC in the namespace

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--namespace` | Yes | Target namespace |
| `--name` | No | Deployment/PVC base name (default `redis`) |
| `--delete-pvc` | Yes | Must be `yes` — deletes the data PVC (all Redis data) |
| `--stack` | No | Defaults to `$PULUMI_STACK`; no dev fallback |

## What It Leaves Alone

- `prod` namespace itself
- `stateful-db` pool — see [kops-setup.md §4](../../kops-setup.md#4-day-2-operations-git-first-workflow)
