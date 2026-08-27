# Pulumi Stack Names

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## The Rule

One project (`arangodb-cluster`, `storage_class`, ...) can hold many stacks — one per cluster it is deployed to.

**Do not reuse the same stack name across different clusters** for anything beyond throwaway lab work. A stack name should identify *which cluster* it targets, so `pulumi stack ls` and `pulumi -s <name> ...` stay unambiguous when several clusters run side by side.

## Naming by Profile

| Profile | Stack name | Why |
|---------|-----------|-----|
| Simple lab | `dev` | Matches shipped `Pulumi.dev.yaml`; one shared throwaway cluster, so a name collision is harmless |
| Shared lab | `experiments` | Same reasoning — one shared cluster |
| Any real cluster (staging, prod-us, prod-eu, ...) | The cluster's own name, e.g. `dcr-kube1` | Unique per cluster, never collides even when two clusters are both "production" |

## How Recipes Resolve It

[`create-cluster-env`](cluster-env.md) writes `PULUMI_STACK=<cluster-name>` into the env file.

Every `just gcp-pulumi` recipe defaults `--stack` to `$PULUMI_STACK` when the flag is omitted, so day-to-day commands never spell out a stack name.

Override the default only when you deliberately want a different name — for example sharing one stack across a small pool of near-identical clusters:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> \
  --pulumi-stack <custom-name> --force yes
```

## Per-Stack Files

Each project directory holds `Pulumi.yaml` plus one `Pulumi.<stack>.yaml` per stack.

Secret values inside those files are encrypted with the KMS provider — `PULUMI_SECRET_PROVIDER` plus the `pulumi-manager` key ([backend bootstrap](backend-bootstrap.md)).
