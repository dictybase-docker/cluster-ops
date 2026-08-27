# Switching Between Clusters

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## How It Works

Each cluster env file carries its own kubeconfig, credentials, KMS URI, and `PULUMI_BACKEND_URL`. Switching clusters means leaving one sub-shell and entering another — there is no in-place switch.

```bash
exit
just cluster-env --env dev --cluster cluster-b
```

## Confirm Before Applying

Before any `pulumi up`, confirm two things:

1. The printed inventory shows a real `PULUMI_BACKEND_URL` — not empty.
2. `kubectl get nodes` shows the cluster you actually mean.

Both are covered by:

```bash
just gcp-pulumi check-backend
```

## Common Mistake: pulumi login Is Global

`pulumi login` sets a **machine-wide** backend, not a per-shell one.

If you enter a cluster shell whose env file has no `PULUMI_BACKEND_URL`, Pulumi silently stays logged into whatever backend you used last — which may belong to a different cluster or project. Commands then read and write the wrong state file.

Fix: make sure `PULUMI_BACKEND_URL` is set in the env file ([cluster env](cluster-env.md)), and run `check-backend` after switching.
