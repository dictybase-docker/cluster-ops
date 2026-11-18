# Filestash Operations

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

Every recipe on this page except `probe-public` takes `--stack` (default
`$PULUMI_STACK`) and `--namespace` (default `properties.namespace` of
`filestash/Pulumi.<stack>.yaml`). A `--namespace` that differs from the stack
file stops the recipe before any change.

## Table of Contents

- [just filestash upgrade](#just-filestash-upgrade)
- [just filestash rollback](#just-filestash-rollback)
- [just filestash purge-uploads](#just-filestash-purge-uploads)
- [just filestash audit-shares](#just-filestash-audit-shares)
- [just filestash probe-public](#just-filestash-probe-public)
- [just filestash resize-state](#just-filestash-resize-state)
- [just filestash teardown](#just-filestash-teardown)
- [Monitoring](#monitoring)

## just filestash upgrade

### What It Does

Makes a state snapshot, then pins a new Filestash image and deploys it. The old image stays in the stack file for `rollback`.

### Behavior

1. Checks `--tag` and `--digest`. It stops when the stack already pins that digest.
2. Runs `backup-now` and prints the snapshot id. Record the id.
3. Writes the current image to `previousImage.name`, `previousImage.tag`, and `previousImage.digest`.
4. Writes `--tag` and `--digest` to `image.tag` and `image.digest`.
5. Runs `deploy`, then `verify`.

After the recipe, commit the stack file. Then run the
[isolation tests](first-run.md#isolation-tests) and
[`probe-admin`](admin-access.md#just-filestash-probe-admin) again.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--tag` | Yes | none | Letters, digits, `.`, `_`, `-`. |
| `--digest` | Yes | none | `sha256:<64 hex>`. The vendor publishes only `latest`, so the digest is the pin. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash rollback

### What It Does

Pins `previousImage` again and deploys it.

### Behavior

1. Reads `previousImage.tag` and `previousImage.digest`. When no `upgrade` wrote them, the recipe stops.
2. Writes them to `image.tag` and `image.digest`.
3. Runs `deploy`.

If the new release changed the state format, also restore the snapshot that
`upgrade` made:
`just filestash restore-state --in-place yes --snapshot <id>`. Commit the
stack file after the run.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash purge-uploads

### What It Does

Lists or deletes the objects in `uploads` that are older than `--older-than` days. It runs as `filestash-operator`.

### Behavior

1. Checks that `--older-than` is a positive number of days.
2. Without `--confirm yes`, lists the objects (`mc find --older-than`). This is a dry run.
3. With `--confirm yes`, deletes the same objects (`mc rm --recursive --force --older-than`).

There is no default age. The retention period is a project decision.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--older-than` | Yes | none | Days. |
| `--confirm` | No | none | `yes` deletes. Any other value lists only. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash audit-shares

### What It Does

Lists each share link and fails on a rule break. The recipe makes no change.

### Behavior

1. Checks for a local `sqlite3`.
2. Copies the live `share.sql` from the Filestash pod to a temporary file.
3. Checks the copy with `PRAGMA quick_check`. A copy made during a write can be damaged. In that case, run the recipe again.
4. Prints each link with its id, path, read, upload, and write flags, users, and expiry.
5. Fails on an upload link with no expiry, a link with write permission, or a path outside `/uploads/` and `/downloads/`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash probe-public

### What It Does

Checks the direct file URLs on the public MinIO host from outside the cluster. It does not use the Filestash route. It needs one file that `publish` made.

### Behavior

1. Sends each request in the table without credentials.
2. Compares each HTTP status with the expected status. Each mismatch prints `FAIL`, and the recipe exits non-zero at the end.

| Request | Expected |
|---|---|
| `GET downloads/<key>` | 200 |
| `GET downloads/<key>` with `Range: bytes=0-9` | 206 |
| `GET downloads/`, `GET uploads/` (list) | 403 |
| `GET uploads/probe-<pid>` | 403 |
| `PUT downloads/probe-<pid>` | 403 |
| `DELETE downloads/probe-<pid>-absent` | 403 |

The write and delete requests use a throwaway key. The probe never sends a
write or a delete to the published `--key`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--host` | Yes | none | The public MinIO host: `storage.dictycr.org` on `dcr-kube1`, `storage.dictybase.dev` on `experiments`. |
| `--key` | Yes | none | A published key in `downloads`, `<dataset>/<file>`. |

The recipe takes no `--stack` or `--namespace`. It uses the public URL only.

## just filestash resize-state

### What It Does

Grows `pvc/filestash-state` and writes the new size to the stack file.

### Behavior

1. Checks `--size`. It stops when the size is not larger than the current size, because a PVC cannot shrink.
2. Checks that the storage class of the PVC allows volume expansion.
3. Writes `storage.size` to the stack file.
4. Runs `deploy`.

Commit the stack file after the run.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--size` | Yes | none | A quantity such as `10Gi`. Use `Mi`, `Gi`, or `Ti`. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash teardown

### What It Does

**Destructive.** Destroys the `filestash` stack. This deletes the state PVC (`config.json` and every share link) and Secrets `filestash` and `filestash-users`.

### Behavior

1. Stops unless `--delete-pvc yes` is given.
2. Prints the state files in `/app/data/state/db` that the destroy deletes.
3. Runs `pulumi destroy` on the `filestash` stack.

These stay: the buckets and their objects, the `restic` snapshots, and the
`filestash-backup` stack. With no Filestash pod, each backup Job fails. The
stack file keeps the encrypted values. A later `deploy` and
`restore-state --in-place yes` bring the service back.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--delete-pvc` | Yes | none | Must be `yes`. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## Monitoring

The cluster has no alert system. Run `just filestash verify` by hand. The
owner and the frequency of that check are deployment inputs.

Watch these signals:

- Pod readiness.
- PVC use.
- The age of the newest snapshot.
- HTTP 5xx at the Gateway.
- The `AUDIT` lines in the Filestash log.
