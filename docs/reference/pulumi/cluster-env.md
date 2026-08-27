# Cluster Environment File

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## What It Is

`create-cluster-env` writes a gitignored `.env.<env>.<cluster-name>` file — see [README §1.3](../../../README.md#13-environment-variables--the-cluster-env-file).

It infers the GCP project from `config/kops/<cluster-name>/cluster.yaml` (or `$PROJECT_ID` / `--project`) and fills in canonical paths.

## Command

```bash
just create-cluster-env --env <env> --cluster <cluster-name> --force yes
```

## Variables Written

| Variable | Default |
|----------|---------|
| `PROJECT_ID` | `spec.project` in `config/kops/<cluster-name>/cluster.yaml` |
| `GOOGLE_APPLICATION_CREDENTIALS` | `credentials/<project-id>/kops-cluster-creator.json` |
| `KUBECONFIG` | `clusters/<cluster-name>/kubeconfig` |
| `PULUMI_GCP_CREDENTIALS` | `credentials/<project-id>/pulumi-manager.json` |
| `PULUMI_SECRET_PROVIDER` | `gcpkms://projects/<project-id>/locations/us-central1/keyRings/<cluster-name>/cryptoKeys/<cluster-name>` |
| `PULUMI_BACKEND_URL` | `gs://pulumi-state-<project-id>` |
| `PULUMI_STACK` | `<cluster-name>` — the stack every project on this cluster uses ([stack names](stack-names.md)) |

## Overrides

Pass a flag when a default is wrong, e.g.:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> \
  --pulumi-secret-provider "gcpkms://projects/.../cryptoKeys/..." \
  --force yes
```

If `PROJECT_ID` cannot be inferred, the recipe stops and says so rather than guessing.

## Environment Variable Contract

Recipes resolve values in this order:

1. Exported environment variables (from the cluster shell)
2. CLI flags — these **override** the environment
3. Missing required values abort with an error, never a silent default

## Activating the Shell

```bash
just cluster-env --env <env> --cluster <cluster-name>
```

The recipe prints which variables are set and which are still empty. Stay in that sub-shell for the rest of the setup. Type `exit` or press Ctrl-D to leave.
