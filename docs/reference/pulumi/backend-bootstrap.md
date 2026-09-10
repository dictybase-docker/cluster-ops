# Pulumi Backend Bootstrap

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

Run once per GCP project, from an activated cluster shell ([cluster env](cluster-env.md)).

```bash
just gcp-pulumi bootstrap-backend
```

## Stages

The recipe folds five stages into one sequential run; each aborts the run on failure. A preflight first checks the active identity holds the admin roles the bootstrap needs (`iam.serviceAccountAdmin`, `iam.serviceAccountKeyAdmin`, `resourcemanager.projectIamAdmin`, `cloudkms.admin`, `storage.admin`) — the least-privilege `kops-cluster-creator` lacks the key-admin and KMS roles, so rotate first with `just gcp-cluster rotate-to-manager`. The preflight also rejects `PULUMI_*` env values that belong to another project (regenerate the env file with `create-cluster-env --force yes`).

1. [pulumi-manager key](#1-pulumi-manager-key) — `just gcp-sa create-sa --sa-name pulumi-manager`, **skipped when the existing key authenticates**
2. Key-propagation wait — a freshly minted SA key can take ~1 minute before token exchange works; the recipe loops until the key authenticates
3. [KMS keyring and crypto key](#2-kms-keyring-and-crypto-key) — runs **as `sa-manager`** (`--credentials-file $GOOGLE_APPLICATION_CREDENTIALS`); `pulumi-manager` holds only `cloudkms.cryptoOperator` and cannot create keyrings
4. [GCS state bucket and login](#3-gcs-state-bucket-and-login) — `just gcp-pulumi pulumi-gcs-setup`
5. [Verify](#verify) — `just gcp-pulumi check-backend`

The per-stage recipes remain available for re-running a single stage — e.g. after a `check-backend` failure names the exact broken step.

**Re-running the first stage mints a key only when needed.** `bootstrap-backend` skips minting when `${PULUMI_GCP_CREDENTIALS}` exists and authenticates; a mint writes a fresh JSON key there, and old keys are not revoked — audit and prune with:

```bash
gcloud iam service-accounts keys list \
    --iam-account="pulumi-manager@${PROJECT_ID}.iam.gserviceaccount.com"
```

The role-binding step retries on the propagation-delay `400 does not exist` that hits a freshly created service account; any other failure stops immediately. Re-run `just gcp-pulumi bootstrap-backend` — an existing authenticating key is reused, and no key is minted until it is actually needed.

## 1. pulumi-manager Key

Creates the `pulumi-manager` service account and writes its JSON key to `${PULUMI_GCP_CREDENTIALS}` — by default `credentials/<project-id>/pulumi-manager.json`.

This is the identity every Pulumi recipe authenticates as. It is separate from the kOps cluster-creator key so that state-plane and control-plane permissions stay distinct.

## 2. KMS Keyring and Crypto Key

Reads `${PULUMI_SECRET_PROVIDER}` for the keyring/key names. Inside `bootstrap-backend` it authenticates as the **active shell identity** (`sa-manager`, which holds `cloudkms.admin`); standalone `just gcp-kms create-keyring-and-key` defaults its credentials to `${PULUMI_GCP_CREDENTIALS}`.

The recipe is idempotent: an existing keyring or crypto key is reported and reused rather than recreated, so re-running converges the same `dcr-kube1` keyring/key.

This key encrypts every Pulumi secret value in the state file. Losing it makes existing encrypted config unreadable — it is not recoverable from the state bucket alone.

## 3. GCS State Bucket and Login

Creates `${PULUMI_BACKEND_URL}` (default `gs://pulumi-state-<project-id>`) with object versioning enabled, then runs `pulumi login` against it.

| Flag | Default |
|------|---------|
| `--sa-json-path` | `$PULUMI_GCP_CREDENTIALS`, else `$GOOGLE_APPLICATION_CREDENTIALS` |
| `--gcs-bucket` | Derived from `$PULUMI_BACKEND_URL`, else `pulumi-state-<project-id>` |
| `--location` | `us-central1` |
| `--lifecycle-config` | None — optional path to a lifecycle policy file |

The recipe is idempotent: an existing bucket is reused rather than recreated, and object versioning is re-asserted on every run — a pre-existing bucket without versioning is converged instead of failing `check-backend`.

## Verify

Confirms the `PULUMI_*` variables are set, the state bucket exists and is versioned, the KMS key is reachable, and the active `pulumi login` matches `$PULUMI_BACKEND_URL`.

A failure message names the stage to re-run — e.g. `PULUMI_SECRET_PROVIDER` empty means re-enter the cluster shell, not re-run `create-keyring-and-key`.