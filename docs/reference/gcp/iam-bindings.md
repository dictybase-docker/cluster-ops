# IAM Binding Converge Details

Back to: [kOps Cluster Setup](../../kops-setup.md)

## What It Does

Converges GCP IAM bindings idempotently: reads the current policy, adds the
member to the role only when missing, and writes back with etag optimistic
concurrency. Re-running reports `unchanged`. Read-modify-write only — no
other policy content is touched.

## Behavior

1. Reads the project IAM policy (or bucket policy for `add-bucket-binding`).
2. Member already bound to the role → `unchanged` (no write).
3. Otherwise adds the member (policy copied — the read value is never mutated) and writes with `SetPolicy`.
4. An etag conflict (409/Aborted) retries the full read-modify-write cycle with a fresh etag — 3 attempts, exponential backoff.
5. `--dry-run` reports the intended action and never writes.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--project` | Yes | — | GCP project id (`iam add-binding`) |
| `--bucket` | Yes | — | Bucket name (`iam add-bucket-binding`) |
| `--member` | Yes | — | e.g. `serviceAccount:<email>` |
| `--role` | Yes | — | e.g. `roles/storage.admin` |
| `--cred-file` | No | ADC | Service account key path |
| `--dry-run` | No | off | Report only |

## Notes

- The `gcp-role` wrappers (`add-role-to-sa`, `assign-roles-to-sa`) call `iam add-binding`; their pre-migration bodies remain as `*-shell` for one release.
