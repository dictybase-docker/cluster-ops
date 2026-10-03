# MinIO Import Details

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

## What It Does

Mirrors **one bucket** from a source S3-compatible endpoint (another cluster's MinIO, GCS S3 gateway, AWS S3) into this MinIO, using `mc mirror --preserve --overwrite`. Runs from the operator machine; reaches the in-cluster API through a `kubectl port-forward` to `svc/minio`.

## Command

```bash
just minio import-bucket \
  --bucket <bucket> \
  --source-url <https://source-endpoint:9000> \
  --source-user '<source-access-key>' \
  --source-password '<source-secret-key>'
```

## Behavior

1. Reads this MinIO's root credentials from Secret `minio-root` in the namespace
2. Opens a port-forward `localhost:19000 → svc/minio:9000` (killed on exit)
3. Registers `mc` aliases `minio-src` (explicit source credentials) and `minio-dst` (Secret-derived)
4. Creates the bucket on the target if missing (`mc mb --ignore-existing`)
5. Runs `mc mirror --preserve --overwrite --retry --summary` with HTTP/1.1 forced for the source (`GODEBUG=http2client=0`); `--remove` only when the flag is passed
6. Retries the mirror up to `--retries` times (default 5), re-probing the port-forward before each retry — each attempt resumes, skipping objects already copied

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--bucket` | Yes | — | One bucket per run |
| `--source-url` | Yes | — | Source S3 endpoint incl. scheme and port |
| `--source-user` / `--source-password` | Yes | — | Source credentials; never stored anywhere |
| `--namespace` | No | `prod` | |
| `--secret` | No | `minio-root` | Target credentials Secret |
| `--remove` | No | off | Deletes target objects missing at the source — dangerous, opt-in |
| `--retries` | No | `5` | Mirror attempts before giving up; each attempt resumes where the last stopped |

## Idempotence

Re-runnable. `--overwrite` + `--preserve` copies only new/changed objects and keeps metadata. Without `--remove`, target-only objects survive a re-run.

## Sourcing Credentials

Source credentials are **not** the target's `minio-root` values and are never written to the cluster or the Pulumi stack — they live only in the recipe invocation. For a source MinIO on another cluster of this repo, its credentials sit in that cluster's own `minio` stack Secret.

## Credential Pairs — Do Not Mix

| Alias | Credentials from | Points at |
|-------|------------------|-----------|
| `minio-src` | `--source-user` / `--source-password` flags | source endpoint |
| `minio-dst` | Secret `minio-root` in this cluster | this MinIO via port-forward |

The recipe guards the direction: source → target, one bucket. To seed multiple buckets, run once per bucket.

## Large Imports

The mirror streams through the operator machine over the port-forward — throughput is bounded by the local uplink, not the cluster. For multi-TiB buckets, run from a VM with good bandwidth to both endpoints.

The recipe already mitigates the two laptop-path failure modes: HTTPS load balancers that reset long-lived HTTP/2 streams (HTTP/1.1 is forced for source reads) and transient per-object failures (`mc mirror --retry` retries objects in place, and the outer `--retries` loop resumes the whole mirror). If it is still slow, that is the port-forward uplink ceiling — run from a VM with good bandwidth to both endpoints. See [troubleshooting](troubleshooting.md).
