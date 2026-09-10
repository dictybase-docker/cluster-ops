# Pulumi Backend Bootstrap

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

Run once per GCP project, from an activated cluster shell ([cluster env](cluster-env.md)).

```bash
just gcp-pulumi bootstrap-backend
```

## Stages

The recipe folds four recipes into one sequential run; each stage aborts the run on failure:

1. [pulumi-manager key](#1-pulumi-manager-key) — `just gcp-sa create-sa --sa-name pulumi-manager`
2. [KMS keyring and crypto key](#2-kms-keyring-and-crypto-key) — `just gcp-kms create-keyring-and-key`
3. [GCS state bucket and login](#3-gcs-state-bucket-and-login) — `just gcp-pulumi pulumi-gcs-setup`
4. [Verify](#verify) — `just gcp-pulumi check-backend`

The per-stage recipes remain available for re-running a single stage — e.g. after a `check-backend` failure names the exact broken step.

**Re-running the first stage mints a new SA key.** `create-sa` writes a fresh JSON key on every run; old keys are not revoked, so they accumulate on the service account. Audit and prune with:

```bash
gcloud iam service-accounts keys list \
    --iam-account="pulumi-manager@${PROJECT_ID}.iam.gserviceaccount.com"
```

The role-binding step retries on the propagation-delay `400 does not exist` that hits a freshly created service account; any other failure stops immediately. Re-run `just gcp-pulumi bootstrap-backend` — an existing SA is reused, and no key is minted until role assignment succeeds.

## 1. pulumi-manager Key

Creates the `pulumi-manager` service account and writes its JSON key to `${PULUMI_GCP_CREDENTIALS}` — by default `credentials/<project-id>/pulumi-manager.json`.

This is the identity every Pulumi recipe authenticates as. It is separate from the kOps cluster-creator key so that state-plane and control-plane permissions stay distinct.

## 2. KMS Keyring and Crypto Key

Reads `${PULUMI_SECRET_PROVIDER}` for the keyring/key names and `${PULUMI_GCP_CREDENTIALS}` for auth.

This key encrypts every Pulumi secret value in the state file. Losing it makes existing encrypted config unreadable — it is not recoverable from the state bucket alone.

## 3. GCS State Bucket and Login

Creates `${PULUMI_BACKEND_URL}` (default `gs://pulumi-state-<project-id>`) with object versioning enabled, then runs `pulumi login` against it.

| Flag | Default |
|------|---------|
| `--sa-json-path` | `$PULUMI_GCP_CREDENTIALS`, else `$GOOGLE_APPLICATION_CREDENTIALS` |
| `--gcs-bucket` | Derived from `$PULUMI_BACKEND_URL`, else `pulumi-state-<project-id>` |
| `--location` | `us-central1` |
| `--lifecycle-config` | None — optional path to a lifecycle policy file |

The recipe is idempotent: an existing bucket is reported and reused rather than recreated.

## Verify

Confirms the `PULUMI_*` variables are set, the state bucket exists and is versioned, the KMS key is reachable, and the active `pulumi login` matches `$PULUMI_BACKEND_URL`.

A failure message names the stage to re-run — e.g. `PULUMI_SECRET_PROVIDER` empty means re-enter the cluster shell, not re-run `create-keyring-and-key`.