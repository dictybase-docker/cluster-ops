# Cluster Environment File

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## What It Is

`create-cluster-env` writes a gitignored `.env.<env>.<cluster-name>` file — see [`kops-setup.md` §1](../../kops-setup.md#1-prerequisites--execution-context).

It also creates `.tool-versions.<env>.<cluster>` from the repo `.tool-versions` manifest when that per-cluster file is missing. Existing per-cluster manifests are preserved unchanged. The env file records the manifest selector so `prepare-tools` installs the correct versions after `cluster-env` activation.

It infers the GCP project from `config/kops/<cluster-name>/cluster.yaml` (or `$PROJECT_ID` / `--project`) and fills in canonical paths.

## Command

```bash
just create-cluster-env --env <env> --cluster <cluster-name> --force yes
```

## Variables Written

| Variable | Default |
|----------|---------|
| `PROJECT_ID` | `spec.project` in `config/kops/<cluster-name>/cluster.yaml` |
| `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` | `.tool-versions.<env>.<cluster>`; created from `.tool-versions` when missing |
| `GOOGLE_APPLICATION_CREDENTIALS` | `credentials/<project-id>/kops-cluster-creator.json` |
| `KUBECONFIG` | `clusters/<cluster-name>/kubeconfig` |
| `PULUMI_GCP_CREDENTIALS` | `credentials/<project-id>/pulumi-manager.json` |
| `PULUMI_SECRET_PROVIDER` | `gcpkms://projects/<project-id>/locations/<region>/keyRings/<cluster-name>/cryptoKeys/<cluster-name>` (region from `cluster.yaml`, default `us-central1`) |
| `PULUMI_BACKEND_URL` | `gs://pulumi-state-<project-id>` |
| `PULUMI_STACK` | `<cluster-name>` — the one unique stack this cluster uses in every project; each project needs a matching `Pulumi.<cluster>.yaml` before `ensure-stack` will init it ([stack names](stack-names.md)) |

## Overrides

Pass a flag when a default is wrong, e.g.:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> \
  --pulumi-secret-provider "gcpkms://projects/.../cryptoKeys/..." \
  --force yes
```

If `PROJECT_ID` cannot be inferred, the recipe stops and says so rather than guessing. With `--force yes`, the env file is replaced, but an existing `.tool-versions.<env>.<cluster>` file is never overwritten.

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

Credentials belong in this file only, never in `.envrc` ([prerequisites](prerequisites.md#access)) — the env file is gitignored, `.envrc` is not, and it leaks into every shell in the repo rather than the one cluster it belongs to.
