# ArangoDB Single-Database Restore Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## Table of Contents

- [What It Does](#what-it-does)
- [Config Keys](#config-keys)
- [Restore Arguments Per Mode](#restore-arguments-per-mode)
- [Behavior](#behavior)
  - [Preflight Chain](#preflight-chain)
  - [`restore-database`](#restore-database)
  - [`import-database`](#import-database)
- [Flags](#flags)
  - [`restore-database` Flags](#restore-database-flags)
  - [`import-database` Flags](#import-database-flags)
  - [Added To `configure-restore` And `configure-bootstrap`](#added-to-configure-restore-and-configure-bootstrap)
- [Overwrite Semantics](#overwrite-semantics)
- [Failure Modes](#failure-modes)
- [Lifecycle and Teardown](#lifecycle-and-teardown)
- [Related](#related)

## What It Does

Two composite recipes restore exactly one ArangoDB database out of a restic
snapshot that holds the whole instance:

| Recipe | Source repository | Identity | Stack overlay | EXIT trap |
|--------|-------------------|----------|---------------|-----------|
| `restore-database` | this cluster's own bucket, read from `arangodb-restore/Pulumi.<stack>.yaml` | `dictycr` (whatever `resticSecret.name` says) | `database` + `overwrite` only | `_reset-single-database-config` |
| `import-database` | another GCP project's bucket, passed with `--bucket` | `dictycr-source`, read-only, `noLock: true` | bucket, three secret names, `noLock`, `database`, `overwrite` | `reset-restore-config` |

The export side is unchanged. The nightly CronJob and the immediate
`deploy-backup` Job already run `arangodump --all-databases` into
`/arangodump/<db>` subdirectories inside the snapshot
([backup details](backup.md)), so a single-database restore only selects one of
those subdirectories — there is no per-database export artifact to produce
first.

Narrowing happens at two independent levels, and both must agree:

- restic `--include /arangodump/<database>` reconstructs only that database's
  subtree onto the scratch volume.
- arangorestore `--server.database <database>` writes only into that database.

Users and grants live in `_system`. A single-database restore does **not**
carry app users: after restoring an application database, re-run
[`create-databases` / `configure-app-credentials`](databases.md) (same cluster)
or `finalize-bootstrap` (cross-project) if the app user is missing.

## Config Keys

Both recipes drive the existing `arangodb-restore` stack. They add two keys to
it and take them away again.

| Key | Type | Set by | Effect when absent |
|-----|------|--------|--------------------|
| `properties.database` | string | `restore-database`, `import-database`, `configure-restore --database`, `configure-bootstrap --database` | whole-instance restore: `--all-databases true`, every `/arangodump/*` subdirectory |
| `properties.overwrite` | bool (`"true"` / `"false"` as written by `config set-all`) | the same four recipes, from `--overwrite` | arangorestore runs without `--overwrite true`; every recipe refuses to reach that state for an existing database unless `--overwrite yes` was given ([Overwrite Semantics](#overwrite-semantics)) |

Both keys are recipe-managed and deliberately absent from
`arangodb-restore/Pulumi.dcr-kube1.yaml`. Existing stack templates therefore
keep their whole-instance disaster-recovery behavior unchanged, and no template
edit is needed to use these recipes.

They are removed on every exit path: `restore-database` traps
`_reset-single-database-config` (clears `database` and `overwrite`, leaves
bucket/secrets/`noLock` alone because that run never touched them),
`import-database` traps the broader `reset-restore-config`. A whole-instance
`configure-restore` or `configure-bootstrap` run additionally does
`pulumi config rm --path properties.database` / `properties.overwrite` before
finishing, so a stale filter left behind by any route cannot quietly narrow the
next DR drill to one database.

Validation is duplicated on purpose — once in the recipes so a bad flag fails
before anything is mutated, once in Go so a hand-edited stack config fails at
`pulumi up`:

- Database names must match `^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`
  (`arangodb-restore/types.go`, `databasePattern` / `validateDatabase()`; the
  recipes apply the same regex inline). The leading underscore is allowed
  because `_system` is a legitimate target. ArangoDB's extended naming mode is
  stricter about what the server accepts, but a name this pattern rejects is
  never one arangorestore can address.
- `overwrite: true` without `database` is rejected by `Validate()` and by all
  four recipes: overwrite only applies to a single-database restore.
- `--overwrite` accepts `yes`/`no` (operator spelling) and `true`/`false` (the
  spelling stack config stores); anything else aborts.

## Restore Arguments Per Mode

Built by `arangodb-restore/spec.go`, `buildResticRestoreArgs` and
`buildArangorestoreArgs`.

| Mode | restic (init container `restic-restore`) | arangorestore (main container `arangorestore`) |
|------|------------------------------------------|------------------------------------------------|
| Whole instance | `-r gs:<bucket>:/ restore <snapshot> --target /restore` | `--input-directory /restore/arangodump --all-databases true --include-system-collections --create-database true` |
| One database | same, plus `--include /arangodump/<database>` | `--input-directory /restore/arangodump/<database> --server.database <database> --include-system-collections --create-database true`, plus `--overwrite true` only with `--overwrite yes` |

Both arangorestore invocations are prefixed with
`--server.endpoint http+tcp://<server>:<port> --server.username root
--server.password $(ARANGO_PASSWORD)`, and `--no-lock` is prepended to the
restic args (ahead of the `restore` subcommand, where restic expects
repository-level options) whenever `noLock: true` — which is every
`import-database` run.

restic `--include` matches the **original absolute path recorded in the
snapshot** (`/arangodump/<db>`), never a path under `--target`. `restic restore
--target /restore` then reconstructs that absolute path below the target, which
is why the scratch input directory in single-database mode is
`/restore/arangodump/<db>` and not `/restore/<db>`
(`scratchInputDirectory()`).

## Behavior

### Preflight Chain

`_preflight-database-restore` is the shared read-only gate. Callers run it
**before their first mutation**, so a typo or a surprising target database
aborts before any stack config or Job exists; `scripts/test-arangodb-single-db-restore.sh`
asserts that ordering (`assert_order '_preflight-database-restore' 'config set-all'`).

1. Database name must match `^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`.
2. `--overwrite` must be `yes`, `true`, `no`, `false` or empty.
3. `_restic-ls` proves the snapshot holds `/arangodump/<db>`: a throwaway
   `restic/restic:0.17.0` pod runs `-r gs:<bucket>:/ --no-lock ls <snapshot>
   /arangodump/<db>`, credentials mounted from the restic secret, and only
   lines matching `^/arangodump/<db>(/|$)` are kept. No match aborts.
4. `_database-exists` decides the target gate through `_arangosh-probe`
   (a short-lived `arangodb:<version>` pod running `db._databases()` against
   `_system`). Exit status is the contract: **0 = exists, 3 = definitively
   absent, anything else = the probe itself failed**.
5. Fail closed when the probe returned 0 and `--overwrite` is not `yes`/`true`.
6. Print the resolved outcome (`does not exist yet; arangorestore
   --create-database true creates it`, or `exists and --overwrite yes was
   given: its collections are replaced`), plus the `_system` warning when the
   target is `_system`.

A probe exit other than 0 or 3 aborts the run. An unreachable coordinator is
never read as "database absent", because that reading would turn a network
blip into an unguarded restore.

### `restore-database`

Same-cluster point-in-time restore of one database from this cluster's own
restic bucket.

1. Resolve the stack with `_require-stack`: `--stack`, else `$PULUMI_STACK`,
   else abort with `Error: no stack name — set PULUMI_STACK (via cluster env)
   or pass --stack.` There is no `dev` fallback.
2. Require `--namespace` and `--database`; default `--snapshot` to `latest` and
   `--restore-id` to `dbrestore-<UTC YYYYMMDD-HHMMSS>`.
3. Discover the coordinator Service when `--server` is omitted: first Service
   labelled `arango_deployment=arangodb` in the namespace, excluding `-int` /
   `-ea` and per-member (`-agnt-`, `-crdn-`, `-prmr-`, `-sngl-`) Services;
   falls back to `arangodb` with a warning.
4. Read `bucket` and `resticSecret.name` out of
   `arangodb-restore/Pulumi.<stack>.yaml` with `yq`. A missing file or missing
   keys aborts and points at `reset-restore-config`. This run never takes a
   bucket from a flag — a stack still pointing at a foreign source bucket here
   means an earlier bootstrap skipped its reset, and that is a bug, not a mode.
5. `_assert-no-running-jobs --selector app=arangodb-restore` in the target
   namespace: queued or finished Jobs may be replaced, a Job with a running
   container may not.
6. `_preflight-database-restore` with the bucket/secret from step 4.
7. Install the EXIT trap (`_reset-single-database-config`) — from here on every
   exit path, failure included, leaves the stack in whole-instance DR posture.
8. `configure-restore --database <db> [--overwrite yes]` writes the stack
   config in one `pulumi config set-all`: `namespace`, `server`, `restoreId`,
   `snapshot`, `confirmTarget` (always computed as
   `<namespace>/<server>/<restoreId>`), `database`, `overwrite`. It re-runs the
   same preflight first; the repeat is read-only and costs two extra throwaway
   pods.
9. `apply-restore`: `preview` → `create-resource`, wait for the `restic-restore`
   init container to terminate `Completed`, print its log, wait for the Job,
   print the `arangorestore` log and the scratch PVC.
10. Post-verify with `_database-has-collections`: **0 = verified, 4 = the
    database exists but holds no non-system collection, anything else = the
    probe failed**. Exit 4 fails the composite, so a restore that produced an
    empty database is never reported as success.

### `import-database`

Same flow against another project's bucket. Differences only:

- `--bucket` is required and is used directly by the preflight (there is no
  stack config to read it from yet); `--secret` defaults to `dictycr-source`.
- `--restore-id` defaults to `import-<UTC YYYYMMDD-HHMMSS>`.
- Step 8 calls `configure-bootstrap` instead of `configure-restore`. That
  overlays the source bucket, the three source secret **names** (keys keep
  their `dictycr`-compatible values `resticPass` / `gcsCredentials` /
  `gcsProject`) and `noLock: true`, on top of the target identity and the
  single-database pair.
- `--snapshot` omitted or `latest` is resolved read-only to a concrete snapshot
  id before anything is written, and the resolved id is echoed and stored in
  the stack config, so the effective RPO stays auditable.
  `restore-database` by contrast records the literal `latest` and lets restic
  resolve it when the Job runs.
- The EXIT trap is `reset-restore-config`, because this run moved
  bucket/secrets/`noLock` as well.

## Flags

### `restore-database` Flags

`just arangodb restore-database --namespace <ns> --database <db> [...]`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace`, `-n` | Yes | — | No safe default restore target exists |
| `--database`, `-d` | Yes | — | Use `configure-restore` + `apply-restore` for a whole-instance restore |
| `--snapshot`, `-p` | No | `latest` | restic snapshot id, or `latest`; recorded verbatim in stack config |
| `--overwrite`, `-w` | No | `no` | `yes`/`no` (`true`/`false` also accepted); required to touch an existing database |
| `--server`, `-v` | No | auto-discovered, else `arangodb` | Coordinator Service name |
| `--restore-id`, `-i` | No | `dbrestore-<UTC timestamp>` | DNS-1123 label, at most 46 chars so `arangodb-restore-<id>` stays under 63 |
| `--stack`, `-k` | No | `$PULUMI_STACK` | No `dev` fallback |
| `--retries`, `-r` | No | `180` | Probe attempts per phase in `apply-restore` |
| `--interval`, `-t` | No | `10` | Seconds between probes — 30 minutes per phase at the defaults |

### `import-database` Flags

`just arangodb import-database --namespace <ns> --bucket <source-bucket> --database <db> [...]`

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace`, `-n` | Yes | — | Target namespace in **this** cluster |
| `--bucket`, `-b` | Yes | — | SOURCE project's restic bucket; never defaulted |
| `--database`, `-d` | Yes | — | Use `bootstrap-from-snapshot` for a whole-instance first load |
| `--snapshot`, `-p` | No | `latest` | Empty or `latest` resolves to the newest snapshot in the source repo and is pinned in the stack config |
| `--overwrite`, `-w` | No | `no` | Same semantics as `restore-database` |
| `--secret`, `-c` | No | `dictycr-source` | Holds the SOURCE `resticPass` / `gcsProject` / `gcsCredentials` |
| `--server`, `-v` | No | auto-discovered, else `arangodb` | Coordinator Service name |
| `--restore-id`, `-i` | No | `import-<UTC timestamp>` | Same DNS-1123 and 46-char rules |
| `--stack`, `-k` | No | `$PULUMI_STACK` | No `dev` fallback |
| `--retries`, `-r` | No | `180` | Probe attempts per phase |
| `--interval`, `-t` | No | `10` | Seconds between probes |

### Added To `configure-restore` And `configure-bootstrap`

The two composites above are the recommended entry points; these flags exist so
the same narrowing is available when driving the stack step by step. Neither
recipe installs a trap, so a run that sets `--database` here leaves the keys on
the stack until `reset-restore-config` (or the next whole-instance
`configure-*`) clears them.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--database`, `-d` | No | empty = whole instance | Adds the read-only preflight; `configure-restore` reads bucket/secret from `arangodb-restore/Pulumi.<stack>.yaml`, `configure-bootstrap` uses its own `--bucket` / `--secret` |
| `--overwrite`, `-w` | No | `no` | Rejected without `--database` |

## Overwrite Semantics

Fail closed by default: a single-database restore replaces the collections of
an existing database, so the preflight aborts rather than guess. The abort
message names the database, the server, the namespace, states that nothing was
changed, and gives the exact remedy (`Re-run the same recipe with --overwrite
yes`).

`_system` always exists, so it always requires `--overwrite yes`. It also
restores the source's users and root password — the preflight prints a warning,
and `just arangodb reset-root-password` must run afterwards or the destination
cluster's `arangodb-pass` Secret no longer matches the live root password.

## Failure Modes

| Symptom | Cause | Where it stops |
|---------|-------|----------------|
| `snapshot '<id>' in gs://<bucket> contains no '/arangodump/<db>'` | wrong database name, or a snapshot predating that database | `_restic-ls`, before any mutation; the message prints the `list-snapshots-in-cluster` invocation to inspect the repository (use `list-source-snapshots` for an import) |
| `database '<db>' already exists on '<server>'` | target exists, `--overwrite yes` not given | preflight, before any mutation |
| `could not determine whether database '<db>' exists (probe exit N)` | unreachable coordinator, missing `arangodb-pass`, missing `arangodb-cluster/Pulumi.<stack>.yaml` | preflight; a failed probe is never read as "database absent" |
| `apply-restore` fails | restic or arangorestore error; `BackoffLimit: 0`, no retry | the EXIT trap still clears the single-database keys |
| `database '<db>' exists but has no non-system collection` | restore brought no data | `_database-has-collections` exits 4, the composite fails |
| arangorestore version error | restore image tag does not match the destination server's minor version | `arangodb-restore/types.go` `defaultArangoImageTag` (`3.12.11`) must track `arangodb-cluster:properties.version` in the destination cluster's stack config |

The arangosh probes read `arangodb-cluster:properties.version` from the
destination cluster's stack config to pick their own image tag, so they always
speak the destination's version even when the restore image is stale.

## Lifecycle and Teardown

- The restore Job and its scratch PVC self-delete one hour after finishing
  (`ttlSecondsAfterFinished: 3600`). Inspect logs before then.
- `arangodb-restore` is a one-off action, not a standing service. Destroy the
  stack when done: `just gcp-pulumi remove-resource --folder arangodb-restore`.
- `reset-restore-config` puts the stack back on the DR defaults: bucket
  `restic-arangodb-backup-prod`, secrets `dictycr` (restic/bucket/project),
  `noLock: false`, and clears `snapshot`, `restoreId`, `confirmTarget`,
  `database` and `overwrite`. Both composites reach this state on their own;
  run it by hand only after a manual `configure-restore --database` /
  `configure-bootstrap --database`.

## Related

- [Restore details](restore.md) — whole-instance drill and the shared
  configure/apply mechanics.
- [Cross-project bootstrap](bootstrap.md) — minting `dictycr-source` and the
  whole-instance first load.
- [Backup details](backup.md) — the per-database dump layout this relies on.
- [Troubleshooting](troubleshooting.md) — symptom-to-cause map for the restore
  Job.
