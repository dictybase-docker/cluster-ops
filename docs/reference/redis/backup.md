# Redis Backup Details

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## What It Does

Two stacks, two commands:

1. **`redis-backup-secrets`** — creates Secret `redis-backup-auth` (keys `resticPass`, `gcsProject`, `gcsCredentials`) in the namespace-bootstrap app namespace. The `gcsCredentials` value is the content of the `redis-backup-sa` JSON key, read from disk at `pulumi up` time
2. **`redis-backup`** — creates the GCS bucket `restic-redis-backup-<project-id>`, a daily **1AM** CronJob (`redis-backup-cronjob`, one hour before the ArangoDB backup window), and an immediate first-run Job (`redis-immediate-backup-job`, TTL 15 minutes) so a fresh deploy proves the pipeline

Both jobs run image `dictybase/database-backup` (`app redis-backup`), restic-snapshot the Redis dump to `gs://<bucket>/` with `RESTIC_PASSWORD` from the Secret and the SA key mounted at `/var/secret/gcs-credentials`.

## Commands

```bash
just redis configure-backup-secrets --restic-password '<restic-pass>'
just redis deploy-backup
```

`configure-backup-secrets` is the composite: it creates/reuses the SA and key (unless `--no-setup-sa`), stores all four secret values on the `redis-backup-secrets` stack, applies it, and verifies the Secret is live. `deploy-backup` pins `gcp:project` from `PROJECT_ID`, applies the `redis-backup` stack, waits for the immediate Job, tails its log, and prints the CronJob.

## Behavior

`configure-backup-secrets`:

1. Creates/reuses service account `redis-backup-sa` (idempotent describe-probe)
2. Grants `roles/storage.objectAdmin` with an IAM condition pinning it to `projects/_/buckets/restic-redis-backup-<project-id>` — nothing else in the project. Project-level (not bucket-level) because `deploy-backup` creates the bucket AFTER the key must exist
3. Mints `credentials/<project-id>/redis-backup-sa.json` — **skipped if the file already exists** (key creation is not idempotent; old keys keep working until deleted). Audit with `gcloud iam service-accounts keys list --iam-account redis-backup-sa@<project>.iam.gserviceaccount.com --project <project>`
4. Creates no namespaces — the `namespace-bootstrap` stack owns `prod`/`operators`; the Secret's namespace comes from its `appNamespace` export, and an explicit `--namespace` must match it
5. Stores the four values (restic password, GCS project, key file path, key name) as KMS-encrypted config on the `redis-backup-secrets` stack and applies it

`deploy-backup`:

1. Pins `gcp:project` on the `redis-backup` stack — without it the pulumi-gcp provider falls back to `GOOGLE_CLOUD_PROJECT`, where a stale value from another cluster env creates the bucket in the wrong project
2. Applies the stack: bucket (versioning on, 58-day soft delete, `ForceDestroy: true`), CronJob, immediate Job
3. Waits for the immediate Job; on failure tails 100 log lines; on success tails the restic summary

## Flags — configure-backup-secrets

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--restic-password` | Yes | — | restic repository password; stored encrypted on the `redis-backup-secrets` stack; never generated or defaulted |
| `--setup-sa` | No | `true` | SA + key setup; disable with `--no-setup-sa` when pointing at your own key |
| `--gcs-project` | No | `$PROJECT_ID` | From the cluster env |
| `--gcs-key-file` | No | `credentials/<project-id>/redis-backup-sa.json` | Read at `pulumi up` time — must exist on this machine |
| `--key-name` | No | `gcsCredentials` | Secret key the JSON lands under |
| `--namespace` | No | namespace-bootstrap export | Optional guard; must match |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## Flags — deploy-backup

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` | No | `prod` | |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` / `--interval` | No | `90` / `10` | 15-minute Job wait budget |

## Service Account Pairs — Do Not Mix

`redis-backup-sa` is scoped to the Redis restic bucket only — it is **not** interchangeable with ArangoDB's `backup-gcs-sa` (pinned to `restic-arangodb-backup-prod`) or with the postgres `postgres-backup-sa`. Neither key can write the other system's bucket.

## Restore

Reserved for a future revision — no in-cluster restore recipe exists yet. The restic repository in `gs://restic-redis-backup-<project-id>` is readable with the backup key (`restic -r gs:... snapshots` via any restic container with the Secret mounted), but nothing in this repo wires a restore Job; recovery today is manual.

## Hazards

- The bucket lives in the `redis-backup` stack with **`ForceDestroy: true`** — `pulumi destroy` deletes the bucket **and every backup in it**. Soft-delete (58 days) is the only safety net, and only within that window.
- The immediate Job TTL is 15 minutes — a failed deploy leaves the CronJob in place but you must read the failure from the recipe output, not from the cluster.
