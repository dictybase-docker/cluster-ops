# Pulumi Backend Verification

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

`check-backend` is a **read-only** verification of the Pulumi backend wiring. `bootstrap-backend` runs it as its final stage ([stages](backend-bootstrap.md#stages)); run it standalone after any cluster switch or as a day-2 drift check — it never mutates anything.

```bash
just gcp-pulumi check-backend
```

## Behavior

1. Requires all four `PULUMI_*` variables — anything empty means the shell is not the cluster shell.
2. Checks the manager key file exists at `$PULUMI_GCP_CREDENTIALS` and exports `GOOGLE_APPLICATION_CREDENTIALS` from it for the remaining checks.
3. Checks the state bucket `gs://$PULUMI_BACKEND_URL` exists and reports object versioning — `versioning_enabled`, the field gcloud's own schema uses.
4. Parses the `gcpkms://` URI in `$PULUMI_SECRET_PROVIDER` into project/location/keyring/key and describes the crypto key.
5. Compares the active `pulumi whoami` against `$PULUMI_BACKEND_URL` — `pulumi login` is machine-global, so a stale login silently points at another backend.

Prints one `PASS`/`FAIL` per check, then a summary; exits non-zero when any check failed.

## Environment

| Variable | Used for |
|----------|---------|
| `PULUMI_GCP_CREDENTIALS` | Manager key — must exist on disk |
| `PULUMI_SECRET_PROVIDER` | KMS keyring/key to describe |
| `PULUMI_BACKEND_URL` | State bucket and expected login |
| `PULUMI_STACK` | Presence check only — not validated here |

## Failures and Fixes

| FAIL message | Cause | Fix |
|--------------|-------|-----|
| `PULUMI_* is empty — enter the cluster shell` | Run outside the `cluster-env` sub-shell | `just cluster-env --env <env> --cluster <cluster-name>` |
| `manager key missing at …` | Key file deleted or not yet created | `just gcp-sa create-sa --sa-name pulumi-manager` |
| `state bucket … not found or unreachable` | Bucket not created, or no read access | Re-run `just gcp-pulumi bootstrap-backend` |
| `versioning NOT enabled` | Pre-existing bucket without versioning | `just gcp-pulumi pulumi-gcs-setup` re-asserts it |
| `KMS key not reachable` | Keyring/key not created, or no describe access | `just gcp-kms create-keyring-and-key` |
| `PULUMI_SECRET_PROVIDER is not a well-formed gcpkms URI` | Hand-edited env file | Regenerate: `just create-cluster-env --force yes` |
| `cannot determine the active Pulumi backend (not logged in?)` | No `pulumi login` on this machine | Re-run `just gcp-pulumi pulumi-gcs-setup` |
| `active login is 'X', expected Y` | Machine-global login left over from another cluster | Re-run `just gcp-pulumi pulumi-gcs-setup` |

A failure names the stage to re-run — e.g. `PULUMI_SECRET_PROVIDER` empty means re-enter the cluster shell, not re-run `create-keyring-and-key`.
