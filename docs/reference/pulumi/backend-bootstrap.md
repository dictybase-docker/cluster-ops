# Pulumi Backend Bootstrap

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

Run once per GCP project, from an activated cluster shell ([cluster env](cluster-env.md)).

## 1. Create the pulumi-manager Key

```bash
just gcp-sa create-sa --sa-name pulumi-manager
```

Creates the `pulumi-manager` service account and writes its JSON key to `${PULUMI_GCP_CREDENTIALS}` — by default `credentials/<project-id>/pulumi-manager.json`.

This is the identity every Pulumi recipe authenticates as. It is separate from the kOps cluster-creator key so that state-plane and control-plane permissions stay distinct.

## 2. KMS Keyring and Crypto Key

```bash
just gcp-kms create-keyring-and-key
```

Reads `${PULUMI_SECRET_PROVIDER}` for the keyring/key names and `${PULUMI_GCP_CREDENTIALS}` for auth.

This key encrypts every Pulumi secret value in the state file. Losing it makes existing encrypted config unreadable — it is not recoverable from the state bucket alone.

## 3. GCS State Bucket and Login

```bash
just gcp-pulumi pulumi-gcs-setup
```

Creates `${PULUMI_BACKEND_URL}` (default `gs://pulumi-state-<project-id>`) with object versioning enabled, then runs `pulumi login` against it.

| Flag | Default |
|------|---------|
| `--sa-json-path` | `$PULUMI_GCP_CREDENTIALS`, else `$GOOGLE_APPLICATION_CREDENTIALS` |
| `--gcs-bucket` | Derived from `$PULUMI_BACKEND_URL`, else `pulumi-state-<project-id>` |
| `--location` | `us-central1` |
| `--lifecycle-config` | None — optional path to a lifecycle policy file |

The recipe is idempotent: an existing bucket is reported and reused rather than recreated.

## Verify

```bash
just gcp-pulumi check-backend
```

Confirms the `PULUMI_*` variables are set, the state bucket exists and is versioned, the KMS key is reachable, and the active `pulumi login` matches `$PULUMI_BACKEND_URL`.
