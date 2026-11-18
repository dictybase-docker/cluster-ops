# Filestash Operations

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

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

Runs `backup-now` and prints the snapshot id, writes the current image to `previousImage`, pins `--tag` and `--digest`, runs `deploy` and `verify`. Run the isolation tests and `probe-admin` after each upgrade.

| Flag | Required |
|---|---|
| `--tag` | Yes |
| `--digest` | Yes (`sha256:<64 hex>`) |

## just filestash rollback

Pins `previousImage` and runs `deploy`. Fails when no upgrade wrote `previousImage`. If the new release changed the state format, restore the snapshot that `upgrade` made: `just filestash restore-state --in-place yes --snapshot <id>`. `upgrade` prints the id.

## just filestash purge-uploads

As `filestash-operator`, lists the objects in `uploads` older than `--older-than` days. With `--confirm yes`, deletes them. There is no default age: the retention period is a project decision.

## just filestash audit-shares

Copies the live `share.sql` from the Filestash pod, checks it with `PRAGMA quick_check`, and lists each link with its path, flags, users, and expiry. Fails on an upload link with no expiry, a link with write permission, or a path outside `/uploads/` and `/downloads/`. Needs a local `sqlite3`.

## just filestash probe-public

Checks direct URLs from outside the cluster. `--host` is the MinIO host, and `--key` is a published file.

| Request | Expected |
|---|---|
| `GET downloads/<key>` | 200 |
| `GET` with `Range: bytes=0-9` | 206 |
| `GET downloads/`, `GET uploads/` | 403 |
| `GET uploads/<key>` | 403 |
| `PUT` and `DELETE` of a throwaway key in `downloads` | 403 |

The probe never sends a write or a delete to the published `--key`.

## just filestash resize-state

Grows `pvc/filestash-state` to `--size`. Fails before any change when the size is not larger or the storage class does not allow expansion. Writes `storage.size` and runs `deploy`. Commit the stack file after the run.

## just filestash teardown

**Destructive.** Requires `--delete-pvc yes`. Destroys the `filestash` stack, which deletes the state PVC and Secrets `filestash` and `filestash-users`. The buckets, their objects, and the restic snapshots stay. The stack file keeps the encrypted values, so a later `deploy` and `restore-state --in-place yes` bring the service back.

## Monitoring

The cluster has no alert system. Run `just filestash verify` by hand. The owner and the frequency of that check are deployment inputs. Watch these signals: pod readiness, PVC use, the age of the newest snapshot, HTTP 5xx at the Gateway, and the `AUDIT` lines in the Filestash log.
