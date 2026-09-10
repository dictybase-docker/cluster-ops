# Service Accounts & Authentication

Back to: [kOps Cluster Setup](../../kops-setup.md)

Two identities, used in sequence:

| Identity | Scope | Used for |
|----------|-------|----------|
| `sa-manager` | Broad | Enabling APIs and creating the narrower SA |
| `kops-cluster-creator` | Least-privilege | Everything from bootstrap onward |

Work inside the env shell ([cluster env](cluster-env.md)) so `${PROJECT_ID}` is set.

## Composite Recipes

Two recipes fold the whole identity chain. Neither needs a shell re-entry in the middle — gcloud configurations persist in `~/.config/gcloud`, and none of these steps read `GOOGLE_APPLICATION_CREDENTIALS`.

### `bootstrap-identities`

Folds `setup-sa-manager` + `configure-gcloud --name ${PROJECT_ID}-sa-manager` + `setup-kops-creator` (Phase 1a enable APIs + Phase 1b disable unused APIs + Phase 2 create the SA) into one call.

### `rotate-to-creator`

Folds `cluster-cred` + `configure-gcloud --name ${PROJECT_ID}-kops-cluster-creator` into one call. `configure-gcloud` uses a **separate** named configuration, so your `sa-manager` config stays available for the rare task that needs it. The single `exit`/re-enter afterwards is the only boundary: it re-sources the env file so Section 3 tools (`kops`/`kubectl`) see the new `GOOGLE_APPLICATION_CREDENTIALS`.

### `rotate-to-manager`

The inverse — folds `cluster-cred` + `configure-gcloud --name ${PROJECT_ID}-sa-manager` back to the broad identity. Use it for admin tasks that need `iam.serviceAccountKeyAdmin`/`cloudkms.admin` rights the creator lacks, e.g. [`bootstrap-backend`](../pulumi/backend-bootstrap.md) preflight or minting another SA. Same single `exit`/re-enter boundary afterwards.

## Obtain sa-manager

**If you are the project owner**, create the SA and download its key:

```bash
just gcp-sa setup-sa-manager
```

**Otherwise**, ask the owner for the JSON key and save it as `credentials/${PROJECT_ID}/sa-manager.json`.

## Point the Env File at the Key

Inside an active `cluster-env` shell, `--env`/`--cluster` default from `CLUSTER_ENV`/`CLUSTER_NAME` — only `--key` needs typing:

```bash
just cluster-cred --key credentials/${PROJECT_ID}/sa-manager.json
exit
just cluster-env --env <env> --cluster <cluster-name>
```

(Re-entering the shell always needs `--env`/`--cluster` typed — that's the one thing a shell that just exited can't remember for you.)

Optional extra line in the same file (not written by `cluster-cred`):

```bash
SA_MANAGER_KEY="${PWD}/credentials/${PROJECT_ID}/sa-manager.json"
```

## Configure gcloud

Two independent authentication paths exist, and they must be rotated together:

| Path | Authenticates | Controlled by |
|------|---------------|---------------|
| `GOOGLE_APPLICATION_CREDENTIALS` | `just` recipes, client libraries, Pulumi | `just cluster-cred` |
| Named gcloud configuration | Direct `gcloud ...` commands you type | `just gcp-cluster configure-gcloud` |

Changing one does **not** change the other.

```bash
just gcp-cluster configure-gcloud
```

Creates (or reuses) the `${PROJECT_ID}-sa-manager` configuration, activates it, authenticates the service account, and sets project and zone.

**Why project-scoped & terminal-isolated.** A gcloud configuration name is machine-global — `~/.config/gcloud` holds every config plus a single shared `active_config` pointer. Project-scoped names (`<project>-sa-manager`, `<project>-kops-cluster-creator`) prevent configs from overwriting each other. In addition, `just cluster-env` exports `CLOUDSDK_ACTIVE_CONFIG_NAME` to pin configuration selection to that sub-shell, preventing parallel terminals from switching each other's active identity.

| Flag | Default |
|------|---------|
| `--name` | `${PROJECT_ID}-sa-manager` |
| `--project` | `$PROJECT_ID` |
| `--key-file` | `$GOOGLE_APPLICATION_CREDENTIALS` |
| `--zone` | `us-central1-c` |

The service-account email is read from the key's own `client_email` field, so a differently named key still activates the identity it actually contains.

## Phases 1a, 1b, 2 — Enable APIs, Disable Unused APIs, Create kops-cluster-creator

One recipe folds all three phases:

```bash
just gcp-cluster setup-kops-creator
```

Internally, in order:

| Phase | Equivalent standalone recipe |
|-------|-------------------------------|
| 1a — enable required APIs | `just gcp-api enable-apis --api-file gcs-files/apis/enabled_apis.txt` |
| 1b — disable unused APIs | `just gcp-api disable-apis --api-file gcs-files/apis/disable_enabled_apis.txt` |
| 2 — create kops-cluster-creator | `just gcp-sa create-sa --sa-name kops-cluster-creator --roles-file gcs-files/roles-permissions/kops-cluster-creator-roles.txt --output-file credentials/${PROJECT_ID}/kops-cluster-creator.json` |

All three already default `--project` from `PROJECT_ID`; only `--api-file`/`--roles-file`/`--output-file` need typing when run standalone.

Use the standalone recipes directly only if you need to re-run one phase in isolation (e.g. re-enabling a single API without touching the SA).

## Rotate to the Narrower Key

Rotate **both** authentication paths, or direct `gcloud` keeps running as the broad `sa-manager` identity:

```bash
# 1. Env credential — used by recipes, libraries, Pulumi
just cluster-cred --key credentials/${PROJECT_ID}/kops-cluster-creator.json
exit
just cluster-env --env <env> --cluster <cluster-name>

# 2. gcloud identity — used by direct gcloud commands
just gcp-cluster configure-gcloud --name ${PROJECT_ID}-kops-cluster-creator
```

Inside `just cluster-env`, `CLOUDSDK_ACTIVE_CONFIG_NAME` pins configuration selection to the active shell automatically. Outside that subshell (or to switch in a cold shell), set:

```bash
export CLOUDSDK_ACTIVE_CONFIG_NAME=${PROJECT_ID}-kops-cluster-creator
# or globally:
gcloud config configurations activate ${PROJECT_ID}-kops-cluster-creator
```

Confirm which identity is active:

```bash
gcloud config configurations list
```

When decommissioning a project or abandoning its cluster, clean up both local configurations with `just gcp-cluster cleanup-gcloud-config` (→ [Teardown](teardown.md#optional-delete-local-gcloud-configurations)).

From here the narrower key is used for cluster provisioning.

> Do not add `KOPS_CLUSTER_NAME`, `KOPS_STATE_STORE`, `BUCKET_NAME`, or `KUBERNETES_VERSION` to the env file — [bootstrap](bootstrap.md) writes those into Git.
