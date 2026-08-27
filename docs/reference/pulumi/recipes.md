# gcp-pulumi Recipe Reference

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

All recipes accept `--stack <name>`. If omitted they use `$PULUMI_STACK`, falling back to `dev` only for the recipes that predate that variable.

## Backend and Stack Lifecycle

| Recipe | What it does | Environment variables |
|--------|--------------|-----------------------|
| `just gcp-pulumi pulumi-gcs-setup` | Create/version GCS state bucket + `pulumi login` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL` |
| `just gcp-pulumi ensure-stack` | `stack select`, or `stack init` if it does not exist yet | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_STACK` (required) |
| `just gcp-pulumi new-stack` | `stack init` with KMS secrets provider | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi new-stack-from` | Init + copy config from another stack | `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi cleanup-resource` | `stack rm --preserve-config` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |

## Configuration

| Recipe | What it does | Environment variables |
|--------|--------------|-----------------------|
| `just gcp-pulumi set-config` | `config set --path <key> <value>` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_STACK` |
| `just gcp-pulumi set-secret` | `config set --path --secret <key> <value>` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_STACK` |

## Apply and Destroy

| Recipe | What it does | Environment variables |
|--------|--------------|-----------------------|
| `just gcp-pulumi preview` | `pulumi preview` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi create-resource` | `pulumi up -f -y` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |
| `just gcp-pulumi remove-resource` | `pulumi destroy -f -y` | `PULUMI_GCP_CREDENTIALS`, `PULUMI_BACKEND_URL`, `PULUMI_STACK` |

## Verification

| Recipe | What it checks | Flags |
|--------|----------------|-------|
| `just gcp-pulumi check-tools` | `pulumi`, `gcloud`, `kubectl`, `jq` present; prints versions | none |
| `just gcp-pulumi check-backend` | `PULUMI_*` set, manager key present, bucket exists and is versioned, KMS key reachable, active login matches `$PULUMI_BACKEND_URL` | none |
| `just gcp-pulumi check-storageclass` | Named StorageClasses exist and use the expected provisioner; reports the cluster default | `--classes`, `--provisioner` |

Verification recipes are read-only and exit non-zero when a required check fails.

`check-backend` treats "not logged in" as a failure, not a warning — an unset backend means Pulumi is still pointed at whichever backend was used last.

`check-storageclass` defaults to `--classes dictycr-balanced --provisioner pd.csi.storage.gke.io`. Production declares two classes, so pass both ([StorageClass](storage-class.md#verify)):

```bash
just gcp-pulumi check-storageclass --classes dictycr-balanced,dictycr-ssd
```
