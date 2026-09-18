# NATS Teardown Details

Back to: [NATS Deploy Guide](../../nats-deploy.md)

## Command

```bash
just nats teardown
```

Core pub/sub keeps no message persistence — no PVC, no backup job — so
teardown loses nothing except connectivity while the server is gone.

## Behavior

Order of operations:
1. `pulumi destroy` on the `nats` stack — removes the Helm release (StatefulSet, ConfigMap, Services, Secret `nats-auth`, `nats-box`)

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--stack` | No | Defaults to `$PULUMI_STACK`; no dev fallback |

## What It Leaves Alone

- The `prod` namespace
- Every instance group, including `stateful-db` — see [kops-setup.md §4](../../kops-setup.md#4-day-2-operations-git-first-workflow)
