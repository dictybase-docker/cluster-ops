# Service Accounts & Authentication

Back to: [kOps Cluster Setup](../../kops-setup.md)

Two identities, used in sequence:

| Identity | Scope | Used for |
|----------|-------|----------|
| `sa-manager` | Broad | Enabling APIs and creating the narrower SA |
| `kops-cluster-creator` | Least-privilege | Everything from bootstrap onward |

Work inside the env shell ([cluster env](cluster-env.md)) so `${PROJECT_ID}` is set.

## Obtain sa-manager

**If you are the project owner**, create the SA and download its key:

```bash
just gcp-sa setup-sa-manager --project-id ${PROJECT_ID}
```

**Otherwise**, ask the owner for the JSON key and save it as `credentials/${PROJECT_ID}/sa-manager.json`.

## Point the Env File at the Key

```bash
just cluster-cred --env <env> --cluster <cluster-name> \
  --key credentials/${PROJECT_ID}/sa-manager.json
exit
just cluster-env --env <env> --cluster <cluster-name>
```

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

Creates (or reuses) the `sa-manager` configuration, activates it, authenticates the service account, and sets project and zone.

| Flag | Default |
|------|---------|
| `--name` | `sa-manager` |
| `--project` | `$PROJECT_ID` |
| `--key-file` | `$GOOGLE_APPLICATION_CREDENTIALS` |
| `--zone` | `us-central1-c` |

The service-account email is read from the key's own `client_email` field, so a differently named key still activates the identity it actually contains.

## Phase 1a — Enable APIs

```bash
just gcp-api enable-apis --project ${PROJECT_ID} \
  --api-file gcs-files/apis/enabled_apis.txt
```

## Phase 1b — Disable Unused APIs

```bash
just gcp-api disable-apis --project ${PROJECT_ID} \
  --api-file gcs-files/apis/disable_enabled_apis.txt
```

## Phase 2 — Create kops-cluster-creator

```bash
just gcp-sa create-sa --project ${PROJECT_ID} --sa-name kops-cluster-creator \
  --roles-file gcs-files/roles-permissions/kops-cluster-creator-roles.txt \
  --output-file credentials/${PROJECT_ID}/kops-cluster-creator.json
```

## Rotate to the Narrower Key

Rotate **both** authentication paths, or direct `gcloud` keeps running as the broad `sa-manager` identity:

```bash
# 1. Env credential — used by recipes, libraries, Pulumi
just cluster-cred --env <env> --cluster <cluster-name> \
  --key credentials/${PROJECT_ID}/kops-cluster-creator.json
exit
just cluster-env --env <env> --cluster <cluster-name>

# 2. gcloud identity — used by direct gcloud commands
just gcp-cluster configure-gcloud --name kops-cluster-creator
```

Using a **separate** configuration name keeps the `sa-manager` configuration intact for the occasional task that genuinely needs the broader identity (creating another SA, changing IAM). Switch between them with:

```bash
gcloud config configurations activate kops-cluster-creator
gcloud config configurations activate sa-manager
```

Confirm which identity is active:

```bash
gcloud config configurations list
```

From here the narrower key is used for cluster provisioning.

> Do not add `KOPS_CLUSTER_NAME`, `KOPS_STATE_STORE`, `BUCKET_NAME`, or `KUBERNETES_VERSION` to the env file — [bootstrap](bootstrap.md) writes those into Git.
