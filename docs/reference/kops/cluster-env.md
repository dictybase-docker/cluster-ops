# The Cluster Env File

Back to: [kOps Cluster Setup](../../kops-setup.md)

## What It Is

Until Git YAML exists, the operator environment lives in one **per-cluster env file**: `.env.<env>.<cluster>` (for example `.env.dev.my-cluster`).

- Gitignored.
- Plain `VAR=value` lines — no `export` prefix.
- `just cluster-env` auto-exports them via `set -a`.

Use it for what you need **before** [bootstrap](bootstrap.md): `PROJECT_ID`, credential paths, kubeconfig path, SSH public key, optional asdf pin file, later Pulumi paths.

> Do **not** put cluster credentials in `.envrc`. `.envrc` is not the per-cluster credential store.

## Create and Activate

Create the file as soon as you know the environment name, short cluster name, and GCP project. The `sa-manager` JSON is **not** required yet — that comes in [service accounts](service-accounts.md).

```bash
just create-cluster-env \
  --env <env> \
  --cluster <cluster-name> \
  --project <project-id>
```

`--cluster` is only the env **filename** suffix. It is not written as `KOPS_CLUSTER_NAME`. `--project` writes `PROJECT_ID` so later `--project` flags default from the activated shell. Optional `--credentials` / `--ssh-key` only if those files already exist.

Activate:

```bash
just cluster-env --env <env> --cluster <cluster-name>
```

The sub-shell reads the file once at start. After you edit the file — or after `just cluster-cred` — `exit` and run `just cluster-env` again.

### Session-Only Convenience Exports

Beyond sourcing the file, `cluster-env` also exports three variables for the life of that shell — **never written to the `.env.<env>.<cluster>` file itself**:

| Variable | Value | Why |
|----------|-------|-----|
| `CLUSTER_NAME` | The `--cluster` you passed to `cluster-env` | Every `gcp-cluster` recipe already reads `CLUSTER_NAME` as a fallback for `--cluster` — this is what lets you stop retyping it |
| `CLUSTER_ENV` | The `--env` you passed | Lets `cluster-cred` default `--env` on re-entry |
| `CLUSTER_ENV_FILE` | Full path to the sourced file | Diagnostic only; not read by other recipes |

This does not weaken "Git owns identity after bootstrap" ([below](#git-owned-after-bootstrap)) — nothing is persisted. `CLUSTER_NAME` is re-derived from the flag you type every time you enter the shell, exactly like today, just no longer requiring you to also pass `--cluster` to every subsequent command in the same session.

## Env-Owned Variables

Set these as you go; they never move to Git.

| Variable | Set up in | Purpose |
|----------|-----------|---------|
| `PROJECT_ID` | [Prerequisites](prerequisites.md) — needed before bootstrap | GCP project for SA, SSH, and `bootstrap-bundle --project` |
| `GOOGLE_APPLICATION_CREDENTIALS` | [Service accounts](service-accounts.md) | Active GCP service-account JSON |
| `SA_MANAGER_KEY` | [Service accounts](service-accounts.md) | Path to `sa-manager.json` (optional; same file as GAC at first) |
| `KUBECONFIG` | [File isolation](file-isolation.md) | Exported kubeconfig path |
| `SSH_KEY` | [File isolation](file-isolation.md#ssh-keypair) | Node SSH **public** key |
| `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` | [Tool versions](tool-versions.md) | Optional per-cluster asdf pin file |
| `PULUMI_GCP_CREDENTIALS` | [`pulumi-setup.md`](../../pulumi-setup.md) | Path to `pulumi-manager.json` |
| `PULUMI_SECRET_PROVIDER` | [`pulumi-setup.md`](../../pulumi-setup.md) | `gcpkms://…` URI |
| `PULUMI_BACKEND_URL` | [`pulumi-setup.md`](../../pulumi-setup.md) | Pulumi state bucket |

## Git-Owned After Bootstrap

Do **not** write these into the env file.

| Field | Where it lives after bootstrap |
|-------|-------------------------------|
| Short cluster name | directory `config/kops/<cluster>/` |
| Full kops DNS name | `cluster.yaml` → `metadata.name` |
| State store | `cluster.yaml` → `spec.configBase` |
| GCP project | `cluster.yaml` → `spec.project` |
| Kubernetes version | `cluster.yaml` → `spec.kubernetesVersion` |
| Topology, CIDRs, addons, instance groups | `cluster.yaml` / `instancegroups.yaml` |

Day-2 recipes still accept `--cluster` / `--project` / `--state` on the command line. After bootstrap, pass `--cluster` (the Git directory name). Do not keep a second copy of name/state/version in the env file.

## After Bootstrap — Git Takes Identity

`just gcp-cluster bootstrap-bundle` is the handoff. It writes `metadata.name`, `spec.project`, and `spec.configBase` into `config/kops/<cluster>/cluster.yaml`. From that point:

- Edit identity and shape in YAML, then commit.
- Leave the env file as credentials + local paths + `PROJECT_ID` (still useful as a `--project` default).
- If `PROJECT_ID` and `spec.project` ever disagree, **Git wins**. Fix the env file or pass `--project` explicitly.
