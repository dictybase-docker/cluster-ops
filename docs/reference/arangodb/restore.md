# ArangoDB Restore Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## Overview

One in-cluster Job: init container `restic restore` → main container `arangorestore`, sharing ephemeral scratch volume. Reverse of [backup](backup.md) — no laptop download, no local Docker, no port-forward.

Run drills against a **clone** cluster or scratch namespace. Same Job is real disaster-recovery path when restoring after data loss.

## Prerequisites

Target namespace needs:
- Reachable coordinator (on a clone: fresh `arangodb-cluster` apply first)
- Secret `arangodb-pass`
- Secret `dictycr`

## Step 1: Configure

```bash
just arangodb configure-restore --namespace <target-namespace>
```

### Config Keys

| Key | Derived by default | Override |
|-----|-------------------|----------|
| `namespace` | None — required | `--namespace <ns>` |
| `server` | First Service labelled `arango_deployment=arangodb` (excludes per-member and `-int`/`-ea` Services); falls back to `arangodb` with warning | `--server <svc>` |
| `restoreId` | `drill-<UTC YYYYMMDD-HHMMSS>` (DNS-1123-safe) | `--restore-id <id>` |
| `snapshot` | `latest` | `--snapshot <restic-snapshot-id>` |
| `confirmTarget` | Computed `<namespace>/<server>/<restoreId>` | Never typed by hand |

### Why the Ceremony

`arangodb-restore` refuses to build the Job unless `confirmTarget` equals `<namespace>/<server>/<restoreId>` exactly (`types.go`, `validateConfirmTarget`). Stale or copy-pasted config cannot silently restore into wrong target.

Recipe also rejects `--restore-id` that isn't DNS-1123 or over 46 chars (keeps Job name under 63 chars). Reusing an id updates same Job; new id creates fresh one.

## Step 2: Apply

```bash
just arangodb apply-restore
```

### Behavior

1. Reads `namespace`, `server`, `restoreId`, `snapshot` from stack config
2. `preview` → `create-resource`
3. Waits for `restic-restore` init container to terminate `Completed`, prints log
4. Waits for Job, prints `arangorestore` log and scratch PVC

### Failure Handling

- Fails fast on non-zero init container exit, dumps that container's log
- `BackoffLimit: 0` means no retry
- Budget: `--retries 180 --interval 10` (30 minutes per phase)

## Step 3: List Snapshots (if needed)

Wrong snapshot or want older point in time:

```bash
just arangodb list-snapshots-in-cluster --namespace <target-namespace>
```

### Behavior

- Starts throwaway `restic/restic:0.17.0` pod (`--rm`, `restartPolicy: Never`)
- Uses `dictycr` secret wiring
- Needs no local `restic` or kops state store
- Read-only — never writes to repository

### Flags

| Flag | Required | Default |
|------|----------|---------|
| `--namespace` | Yes | — |
| `--bucket` | No | `restic-arangodb-backup-prod` |
| `--secret` | No | `dictycr` |
| `--image` | No | `restic/restic:0.17.0` |

Then re-run step 1 with `--snapshot <id>`.

## Step 4: Re-run Databases

After restore, re-run [logical databases](databases.md) if application user `backend` or grants didn't come back — `arangorestore --create-database true` recreates databases/documents, not necessarily every grant.

## Step 5: Record RPO/RTO

- **RPO**: Snapshot's timestamp (from list in step 3)
- **RTO**: Wall-clock time from step 1 to verified `Completed` Job

## Cleanup

Job and scratch PVC self-delete after 1 hour (`ttlSecondsAfterFinished: 3600`). Inspect logs before then.

Tear down restore stack when done — one-off action, not standing service:

```bash
just gcp-pulumi remove-resource --folder arangodb-restore
```
