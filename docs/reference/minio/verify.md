# MinIO Verify Details

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

## What It Does

Read-only post-install check of the running standalone MinIO: node pool, pod
readiness, data PVC, Service port, and the root-credentials Secret. Changes
nothing; safe to re-run at any time.

## Command

```bash
just minio verify
```

## Behavior

Each check prints PASS/FAIL (plain text — colors are disabled in this recipe);
the recipe exits non-zero if any of them fails.

1. **Pool** — `kubectl get nodes -l pool=database` returns exactly `--node-count` nodes (default 3)
2. **Pod** — at least one `app.kubernetes.io/name=minio` pod is `Running` with every container `ready`
3. **PVC** — at least one PVC in the namespace whose name starts with `minio` is `Bound`
4. **Service** — the first port of Service `minio` is `9000`
5. **Secret** — `<secret>` exists in the namespace

The pool check here is a **count only**. Unlike
[`check-pool`](pool-requirements.md#verification) it does not re-check the
`dedicated=database:NoSchedule` taint, node Ready status, or zone spread — run
`just minio check-pool` when the pool itself is suspect.

The Secret check confirms presence, not contents: a `minio-root` carrying keys
other than `rootUser` / `rootPassword` still passes here and fails later, when
`import-bucket` reads those exact keys — see
[troubleshooting](troubleshooting.md).

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | Namespace MinIO runs in |
| `--secret` / `-e` | No | `minio-root` | Root credentials Secret name |
| `--pool` / `-p` | No | `database` | Value of the node label `pool` |
| `--node-count` / `-c` | No | `3` | Expected node count in that pool |

No `--stack` flag: every check is a live `kubectl` read, so the recipe does not
touch Pulumi and does not need `$PULUMI_STACK`.

## What It Does Not Check

- **Data** — no bucket listing, no object counts. After an import, the
  `import-bucket` run prints its own object count; see
  [import details](import.md).
- **S3 API reachability** — no client handshake against port 9000. A pod that
  is `Ready` but rejecting credentials passes verify.

## When Checks Fail

The recipe prints the failing check and points at
[troubleshooting](troubleshooting.md), which maps each symptom to its cause.
