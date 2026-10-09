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

## Service account keys (`sa create-key`)

Key minting is idempotent: a key file whose `private_key_id` matches a live
upstream key is reused (`unchanged`); a missing file mints once; a file whose
key was deleted upstream re-mints exactly once. Key material is written 0600
by a file step outside the cloud seam; only key ID and path are logged.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--name` | Yes | — | Service account name |
| `--project` | Yes | — | GCP project id |
| `--output-file` | No | `key.json` | Key file path |
| `--cred-file` | No | ADC | Credential file |
| `--dry-run` | No | off | Report only |

## SA setup flows (`sa setup-backup` / `sa setup-reader`)

Composite converges (SA → bindings → key) over the primitives above; a failed
binding stops the flow before the key is minted. `--roles` may be empty when
the bindings are conditional.

**Conditions:** the backup recipes pin their grants to one bucket with IAM
condition expressions (`--condition` is deferred from the converge). Those
conditional bindings therefore stay in the shell wrappers; only the key
converge migrated (`sa create-key`, which fixes the previously non-idempotent
mint). Adopting `sa setup-backup`/`setup-reader` in those recipes follows once
`--condition` support lands.
