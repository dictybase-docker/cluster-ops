# Redis Restore and Cross-Cluster Bootstrap Details

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## What It Does

The `redis-restore` stack creates a one-off Kubernetes Job (`redis-restore-<restoreId>`, `ttlSecondsAfterFinished: 3600`) that restores a `restic` snapshot from Google Cloud Storage into the in-cluster `redis` Service (`:6379`).

The Job runs two containers in sequence, sharing an ephemeral scratch volume (`redis-restore-scratch`, `10Gi` `dictycr-balanced`) mounted at `/restore`:

1. **`restic-restore`** (`restic/restic:0.17.0`): Restores `/redis-backup.rdb` from `gs:<bucket>:/` into `/restore/redis-backup.rdb`. When `noLock: true` is set (cross-project bootstrap), prepends `--no-lock` so the read-only service account does not attempt to write a lock object into the source bucket.
2. **`redis-restore`** (`redis:8.4.6`): Starts a temporary local `redis-server` on `127.0.0.1:6380` inside the Job pod to load `/restore/redis-backup.rdb`, optionally runs `FLUSHALL` on the target `redis` Service (`overwrite: true`), migrates all keys and TTLs across logical databases `0..15` via `MIGRATE`, and runs `BGSAVE` and `BGREWRITEAOF` on the target so both RDB and AOF persistence on PVC `redis-data` stay current.

---

## Cross-Cluster Bootstrap (Cloning from a Source Cluster)

To seed a new cluster's Redis instance from a source cluster's `gs://restic-redis-backup-<source-project>` bucket:

**In the SOURCE cluster sub-shell:**

```bash
just redis grant-source-bucket-reader
just redis source-secret-value --namespace prod
```

---

**In the TARGET cluster sub-shell:**

```bash
just redis configure-source-secrets \
  --restic-password '<source-restic-pass>' \
  --gcs-project <source-project-id> \
  --gcs-key-file credentials/<source-project-id>/redis-restic-reader.json

just redis bootstrap-from-snapshot \
  --namespace prod \
  --bucket restic-redis-backup-<source-project-id>
```

### Behavior

1. `grant-source-bucket-reader` creates or reuses `redis-restic-reader@<source-project>.iam.gserviceaccount.com`, grants `roles/storage.objectViewer` on the source bucket, and writes `credentials/<source-project>/redis-restic-reader.json`.
2. `configure-source-secrets` sets `properties.sourceSecret.*` on `redis-backup-secrets` and creates Secret `redis-backup-source` in the target cluster's `prod` namespace without modifying `redis-backup-auth`.
3. `bootstrap-from-snapshot` registers an `EXIT` trap that runs `just redis reset-restore-config`, then runs `configure-bootstrap` (`noLock: true`, `overwrite: true`, Secret `redis-backup-source`, source bucket) followed by `apply-restore`.

### Flags — `bootstrap-from-snapshot`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | Yes | — | Target namespace (`prod`) |
| `--bucket` / `-b` | Yes | — | Source GCS bucket (`restic-redis-backup-<source-project>`) |
| `--snapshot` / `-p` | No | `latest` | Restic snapshot ID or `latest` |
| `--server` / `-v` | No | `redis` | Target Redis Service name |
| `--restore-id` / `-i` | No | `bootstrap-<UTC timestamp>` | DNS-1123 label (at most 49 characters) |
| `--secret` / `-c` | No | `redis-backup-source` | Read-only source Secret name |
| `--stack` / `-s` | No | `$PULUMI_STACK` | Target cluster stack |

---

## Same-Cluster Disaster Recovery Restore

To restore Redis from this cluster's own `gs://restic-redis-backup-<project-id>` bucket:

```bash
just redis configure-restore --namespace prod
just redis apply-restore
```

### Behavior

1. `configure-restore` validates `--restore-id`, computes `confirmTarget` (`<namespace>/<server>/<restoreId>`), and writes the configuration to `redis-restore`.
2. `apply-restore` previews and applies the `redis-restore` stack, waits for `restic-restore` and `redis-restore` to complete, and prints the logs of both containers.

### Flags — `configure-restore`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | Target namespace |
| `--server` / `-v` | No | `redis` | Target Redis Service name |
| `--restore-id` / `-i` | No | `drill-<UTC timestamp>` | DNS-1123 label (at most 49 characters) |
| `--snapshot` / `-p` | No | `latest` | Restic snapshot ID or `latest` |
| `--overwrite` / `-w` | No | `yes` | Runs `FLUSHALL` and `MIGRATE ... REPLACE` when `yes` |
| `--stack` / `-s` | No | `$PULUMI_STACK` | Target cluster stack |
