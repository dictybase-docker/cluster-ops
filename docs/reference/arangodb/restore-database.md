# ArangoDB Single-Database Restore Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

This document explains how to restore or import one ArangoDB database from a restic snapshot without a restore of the full instance.

## Table of Contents

- [What It Does](#what-it-does)
- [Restore From This Cluster (`restore-database`)](#restore-from-this-cluster-restore-database)
- [Import From Another Project (`import-database`)](#import-from-another-project-import-database)
- [Step-by-Step Flags (`configure-restore` and `configure-bootstrap`)](#step-by-step-flags-configure-restore-and-configure-bootstrap)
- [Preflight Checks](#preflight-checks)
- [Overwrite Semantics](#overwrite-semantics)
- [Config Keys and Cleanup](#config-keys-and-cleanup)
- [Restore Arguments Per Mode](#restore-arguments-per-mode)
- [Failure Modes](#failure-modes)
- [Lifecycle and Teardown](#lifecycle-and-teardown)
- [Related](#related)

---

## What It Does

Two composite recipes restore one ArangoDB database out of a restic snapshot that holds the full instance.

| Recipe | Source Repository | Secret Identity | Stack Overlay | Exit Trap |
|--------|-------------------|-----------------|---------------|-----------|
| `restore-database` | This cluster's bucket, from `arangodb-restore/Pulumi.<stack>.yaml` | `dictycr` (from `resticSecret.name`) | `database` and `overwrite` only | Clears `database` and `overwrite` |
| `import-database` | A bucket in another GCP project, from `--bucket` | `dictycr-source` (read-only, `noLock: true`) | `bucket`, three secret names, `noLock`, `database`, `overwrite` | Resets all keys to DR defaults |

The backup job does not change. The daily CronJob and `deploy-backup` run `arangodump --all-databases` and write each database into `/arangodump/<db>` inside the snapshot ([backup details](backup.md)). You do not need a separate per-database backup job.

Both recipes limit the restore in two places:

- `restic` runs with `--include /arangodump/<database>` and restores only that subdirectory onto the scratch volume.
- `arangorestore` runs with `--server.database <database>` and writes only into that database.

Users and grants stay in the `_system` database. A single-database restore does **not** copy application users. If the application user is missing after the restore, run [`create-databases` or `configure-app-credentials`](databases.md) for a same-cluster restore, or run [`finalize-bootstrap`](bootstrap.md#finalize-import) for a cross-project import.

---

## Restore From This Cluster (`restore-database`)

Use `restore-database` for a point-in-time restore of one database from this cluster's own backup bucket.

```bash
just arangodb restore-database --namespace <ns> --database <db>
```

### Behavior

1. Resolves the stack from `--stack` or `$PULUMI_STACK`. The recipe stops if neither value is set.
2. Discovers the coordinator Service in `--namespace` when `--server` is omitted, or falls back to `arangodb`.
3. Reads `bucket` and `resticSecret.name` from `arangodb-restore/Pulumi.<stack>.yaml`. If the stack still holds a cross-project overlay (`dictycr-source` or `noLock: true`), the recipe stops and tells you to run `reset-restore-config`.
4. Resolves an omitted or `latest` `--snapshot` to the newest snapshot ID in the bucket. The preflight checks and the restore Job use that exact snapshot ID.
5. Stops if another `arangodb-restore` Job has an active container in the namespace.
6. Runs the read-only [preflight checks](#preflight-checks) before any stack change.
7. Registers an exit trap that removes `properties.database` and `properties.overwrite` when the command exits.
8. Writes `namespace`, `server`, `restoreId`, `snapshot`, `confirmTarget`, `database`, and `overwrite` to the stack config with `configure-restore`.
9. Runs `apply-restore` to create the Job, waits for the `restic-restore` init container, prints its log, waits for `arangorestore`, and prints its log.
10. Verifies that the restored database contains at least one non-system collection. If the database is empty, the recipe fails.

### `restore-database` Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace`, `-n` | Yes | — | Target namespace in this cluster |
| `--database`, `-d` | Yes | — | Database name. Must match `^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$` |
| `--snapshot`, `-p` | No | `latest` | Restic snapshot ID or `latest`. The recipe pins `latest` to the newest snapshot ID before preflight |
| `--overwrite`, `-w` | No | `no` | `yes` or `no` (`true` or `false` also work). Required when the target database already exists |
| `--server`, `-v` | No | Auto-discovered, else `arangodb` | Coordinator Service name |
| `--restore-id`, `-i` | No | `dbrestore-<UTC timestamp>` | DNS-1123 label, maximum 46 characters so the Job name stays under 63 characters |
| `--stack`, `-k` | No | `$PULUMI_STACK` | Pulumi stack name |
| `--retries`, `-r` | No | `180` | Probe attempts per wait phase in `apply-restore` |
| `--interval`, `-t` | No | `10` | Seconds between probes (30 minutes per phase at the defaults) |

---

## Import From Another Project (`import-database`)

Use `import-database` to copy one database from a restic bucket in another GCP project into this cluster. Create Secret `dictycr-source` first ([cross-project bootstrap](bootstrap.md#3-create-the-source-secret)).

```bash
just arangodb import-database --namespace <ns> --bucket <source-bucket> --database <db>
```

### Behavior

`import-database` runs the same flow as `restore-database`, with these differences:

1. Requires `--bucket` for the source project's restic bucket and defaults `--secret` to `dictycr-source`.
2. Pins an omitted or `latest` `--snapshot` against the source repository before preflight.
3. Defaults `--restore-id` to `import-<UTC timestamp>`.
4. Registers an exit trap that runs `reset-restore-config` on every exit path.
5. Calls `configure-bootstrap` instead of `configure-restore`. This step sets the source bucket, points the three secret names at `dictycr-source`, sets `noLock: true`, and sets `database` and `overwrite`.

### `import-database` Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace`, `-n` | Yes | — | Target namespace in this cluster |
| `--bucket`, `-b` | Yes | — | Source project's restic bucket |
| `--database`, `-d` | Yes | — | Database name. Use `bootstrap-from-snapshot` for a full-instance load |
| `--snapshot`, `-p` | No | `latest` | Restic snapshot ID or `latest`, pinned from the source repository |
| `--overwrite`, `-w` | No | `no` | `yes` or `no`. Required when the target database already exists |
| `--secret`, `-c` | No | `dictycr-source` | Secret with the source `resticPass`, `gcsProject`, and `gcsCredentials` |
| `--server`, `-v` | No | Auto-discovered, else `arangodb` | Coordinator Service name |
| `--restore-id`, `-i` | No | `import-<UTC timestamp>` | DNS-1123 label, maximum 46 characters |
| `--stack`, `-k` | No | `$PULUMI_STACK` | Pulumi stack name |
| `--retries`, `-r` | No | `180` | Probe attempts per wait phase |
| `--interval`, `-t` | No | `10` | Seconds between probes |

---

## Step-by-Step Flags (`configure-restore` and `configure-bootstrap`)

Prefer `restore-database` and `import-database`. If you want to inspect the Pulumi stack configuration before you run `apply-restore`, pass `--database` and `--overwrite` to `configure-restore` or `configure-bootstrap`.

**CAUTION: Neither `configure-restore` nor `configure-bootstrap` registers an exit trap.** Run `just arangodb reset-restore-config` after `apply-restore` so the single-database keys do not stay on the stack.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--database`, `-d` | No | Empty (full instance) | Runs the read-only preflight and limits the restore to one database |
| `--overwrite`, `-w` | No | `no` | Rejected when `--database` is not set |

---

## Preflight Checks

Both recipes run a read-only preflight check **before** any stack config change or Job creation. If any check fails, the recipe stops and changes nothing.

1. **Database name syntax:** `--database` must match `^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`. An initial underscore is allowed for `_system`.
2. **Overwrite flag syntax:** `--overwrite` must be `yes`, `true`, `no`, `false`, or empty. Any other value stops the run. Also, `--overwrite yes` without `--database` is rejected.
3. **Snapshot content check:** A temporary `restic/restic:0.17.0` pod runs `restic --no-lock ls <snapshot> /arangodump/<db>` with credentials from the restic Secret. If the snapshot has no `/arangodump/<db>` directory, the recipe stops.
4. **Target database check:** A temporary `arangodb` pod queries the coordinator to check whether `<db>` already exists. The pod uses the ArangoDB version from `arangodb-cluster/Pulumi.<stack>.yaml`.
5. **Overwrite gate:** If the database exists and `--overwrite` is not `yes` or `true`, the recipe stops. If the coordinator probe fails for any network or authentication reason, the recipe also stops. An unreachable coordinator does not count as a missing database.

---

## Overwrite Semantics

**CAUTION: Overwrite replaces the collections in the target database.** By default, the preflight check refuses to touch an existing database. Pass `--overwrite yes` only when you want to replace the collections in that database.

When you pass `--overwrite yes`, the recipe sets `properties.overwrite: true` and passes `--overwrite true` to `arangorestore`. Do not set `properties.database` by hand in the stack file. The preflight check in the `just` recipe enforces the overwrite gate before the Job starts.

The `_system` database always exists on the cluster, so a restore of `_system` always requires `--overwrite yes`. A restore of `_system` also replaces the cluster's users and root password with the values from the snapshot. If you restore `_system`, run `just arangodb reset-root-password` afterwards so Secret `arangodb-pass` matches the live root password.

---

## Config Keys and Cleanup

Both recipes use the `arangodb-restore` stack. They set two temporary keys on the stack and remove them when the command exits.

| Key | Type | Effect When Set | Effect When Absent |
|-----|------|-----------------|--------------------|
| `properties.database` | string | Restores only `/arangodump/<database>` into `<database>` | Restores the full instance (`--all-databases true`) |
| `properties.overwrite` | bool | Appends `--overwrite true` to `arangorestore` | Runs `arangorestore` without `--overwrite true` |

Neither key is stored in `arangodb-restore/Pulumi.<stack>.yaml`. The committed stack template stays in full-instance disaster-recovery mode.

Cleanup occurs automatically on exit:

- `restore-database` removes `properties.database` and `properties.overwrite` on exit and leaves `bucket`, the secret names, and `noLock` unchanged.
- `import-database` runs `just arangodb reset-restore-config` on exit. This command sets `bucket` back to `restic-arangodb-backup-prod`, sets the three secret names back to `dictycr`, and sets `noLock` to `false`. It also removes `snapshot`, `restoreId`, `confirmTarget`, `database`, and `overwrite`.
- A full-instance `configure-restore` or `configure-bootstrap` run also removes `properties.database` and `properties.overwrite`, so a stale key cannot limit a later disaster-recovery drill to one database.

---

## Restore Arguments Per Mode

The `arangodb-restore` program builds these container arguments for the two modes:

| Mode | Init Container (`restic-restore`) | Main Container (`arangorestore`) |
|------|-----------------------------------|----------------------------------|
| Full instance | `-r gs:<bucket>:/ restore <snapshot> --target /restore` | `--input-directory /restore/arangodump --all-databases true --include-system-collections --create-database true` |
| Single database | Same, plus `--include /arangodump/<database>` | `--input-directory /restore/arangodump/<database> --server.database <database> --include-system-collections --create-database true` (plus `--overwrite true` when `overwrite: true`) |

When `noLock: true` is set (every `import-database` run), `--no-lock` is placed before the `restore` subcommand. Both `arangorestore` modes connect as `root` with the password from Secret `arangodb-pass`.

`restic --include` matches the path `/arangodump/<db>` recorded inside the snapshot. `restic restore --target /restore` writes that path under `/restore`, so `arangorestore` reads from `/restore/arangodump/<db>`.

---

## Failure Modes

| Symptom | Cause | Action |
|---------|-------|--------|
| `snapshot '<id>' in gs://<bucket> contains no '/arangodump/<db>'` | Wrong database name, or the snapshot was taken before the database existed | Run `just arangodb list-snapshots-in-cluster --namespace <ns>` (or `list-source-snapshots` for an import) and pick a snapshot that contains the database |
| `database '<db>' already exists on '<server>'` | The target database exists and `--overwrite yes` was not given | Re-run with `--overwrite yes` if you want to replace the database's collections |
| `the arangodb-restore stack still carries a cross-project bootstrap overlay` | A previous import did not reset the stack config | Run `just arangodb reset-restore-config` before you run `restore-database` |
| `could not determine whether database '<db>' exists (probe exit N)` | The coordinator is unreachable, Secret `arangodb-pass` is missing, or `arangodb-cluster/Pulumi.<stack>.yaml` is missing | Check the coordinator Service and `arangodb-pass` in the target namespace |
| `apply-restore` fails | `restic` or `arangorestore` returned a non-zero exit code (`BackoffLimit: 0`) | Read the container logs printed by the recipe. The exit trap still resets the stack keys |
| `database '<db>' exists but has no non-system collection` | The restore finished, but the target database has no user collections | Inspect the snapshot contents and the `arangorestore` log |
| `arangorestore` version error | The restore container image tag does not match the server's minor version | Match the image tag in `arangodb-restore` to `properties.version` in `arangodb-cluster/Pulumi.<stack>.yaml` |

---

## Lifecycle and Teardown

- The restore Job and its scratch PVC delete themselves one hour after completion (`ttlSecondsAfterFinished: 3600`). Read the logs before the timer expires.
- `arangodb-restore` is a one-off action, not a persistent service. Remove the stack resources after you check the Job logs:

  ```bash
  just gcp-pulumi remove-resource --folder arangodb-restore
  ```

---

## Related

- [Restore details](restore.md) — full-instance disaster-recovery drill and configure/apply mechanics.
- [Cross-project bootstrap](bootstrap.md) — `dictycr-source` setup and full-instance first load.
- [Backup details](backup.md) — snapshot layout and backup CronJob.
- [Troubleshooting](troubleshooting.md) — error messages for the restore Job and cross-project access.
