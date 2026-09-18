# PostgreSQL Logical Import Details (Cross-Major Dump/Restore)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Moves one database between two clusters that **cannot** exchange a physical backup — most often because their PostgreSQL majors differ. Two recipes, one durable artifact between them:

1. `dump-logical` — port-forwards the **source** cluster, exports the database with `pg_dump --format=custom`, writes the archive plus a `.sha256` and a `.metadata` sidecar
2. `restore-logical` — validates the archive and the target's current state **before mutating anything**, then recreates an **empty** target (physical recovery config removed) and loads it with `pg_restore`

The target is always recreated: `restore-logical` never loads into a live database. Everything the recipe can check — archive integrity, major compatibility, whether the target already carries state — is checked first, so a run that is going to fail fails while the target is still intact.

Logical means SQL-level: schema DDL and row data, replayed by the target server. Nothing about the source's on-disk layout, WAL, or page format crosses over, which is exactly why it survives a major-version change that [physical recovery](import.md) cannot.

## Physical vs Logical — Pick One

| | Physical ([`configure-source`](import.md) + [`reset-cluster`](import.md#reset-re-import-into-a-running-cluster)) | Logical (this doc) |
|---|---|---|
| Mechanism | Operator replays base backup + WAL into the PVC | `pg_dump` archive replayed as SQL |
| Major versions | **Source and target must match** | Source ≤ dump client (checked by `dump-logical`, before the export) ≤ restore client = target major (checked by `restore-logical`, before any mutation) |
| Data reached | Whole PostgreSQL instance, byte-identical, PITR-capable | One database; no roles, no ACLs, no other databases |
| Network path | Target pods read the source **GCS bucket** | Operator workstation port-forwards **both** clusters |
| Artifact | None (streamed by the operator) | A file on disk you can keep, checksum, and re-restore |
| Cost of a retry | Full re-bootstrap from the bucket | Re-run `restore-logical` on the same archive |

Current concrete case: source `dcr-experiments` runs `ghcr.io/cloudnative-pg/postgresql:14.13-8` (`cloudnative-pg-cluster/Pulumi.experiments.yaml`), target `dcr-kube1` runs `16.15-202609101440-standard-trixie` (`Pulumi.dcr-kube1.yaml`). A PG 14 base backup into a PG 16 instance never starts — the instance dies with `database files are incompatible with server` / *"The data directory was initialized by PostgreSQL version 14, which is not compatible with this version 16"*. No flag fixes that; the format changes with the major.

## Commands

Both recipes run from the **target** cluster's sub-shell (`restore-logical` needs `$PULUMI_STACK`). `dump-logical` never uses the ambient `KUBECONFIG` — it resolves the source kubeconfig itself from `--source-cluster`, so the shell stays pointed at the target the whole time.

**Export from the source:**

```bash
just postgres dump-logical --source-cluster <source-cluster-name>
```

---

**Load into the target:**

```bash
just postgres restore-logical --archive scratch/postgres/<archive>.dump --app-password '<app-password>' --replace-data yes
```

`--replace-data yes` is required for **any** pre-existing target state, not just a live `Cluster`: an existing Cluster CR, physical import config left on the stack by [`configure-source`](import.md), or objects under this cluster's own backup prefix each trip the guard. Drop the flag only for a target that has never been deployed and whose backup prefix is empty — in practice, a first import on a stack where only [`configure-backup`](backup.md) has run.

## Behavior — dump-logical

Runs under `umask 077`: the archive, both sidecars, and any directory the recipe creates under `scratch/postgres/` are owner-only. The file still holds production rows in the clear — see [Warnings](#warnings).

1. Resolves the source kubeconfig: `--source-kubeconfig` > `KUBECONFIG` in `--source-env-file` > `KUBECONFIG` in the first `.env.*.<source-cluster>` match. `${PWD}` and `${CLUSTER_NAME}` inside that value are expanded, relative paths resolve against the repo root. No kubeconfig found → the run stops before touching anything
2. Aborts if local `--port` (default `15432`) is already bound, then port-forwards `svc/<source-service>` in `--source-namespace` and waits up to 30s for the tunnel
3. Reads `username`/`password` from the **source** `logto-app` Secret — the source app owner, not a superuser and not the target's credentials
4. Resolves **both** versions and refuses an impossible pair before exporting anything: the client version from `pg_dump --version` run inside `--client-image`, the source version from `psql -Atqc 'show server_version'`. A non-numeric major on either side, or `source_major > client_major`, aborts with `source PostgreSQL version <source> is not supported by client <client>`. `pg_dump` may read a server older than itself, never newer. Both values are printed — the source one is what proves which major the archive came from
5. Runs `pg_dump --format=custom --no-owner --no-acl --quote-all-identifiers` inside a `--client-image` container (pinned `postgres:16.15` by default), writing `<archive>.partial` and renaming only on success. An interrupted export leaves no file that looks complete; the `EXIT` trap removes the partial and kills the port-forward
6. Writes `<archive>.sha256` (`<digest>  <basename>`) and `<archive>.metadata` (`source_cluster`, `source_database`, `source_server_version`, `client_version`, `client_image`, `created_at`)

`client_version` is the version `pg_dump --version` actually reported, recorded alongside — not instead of — `client_image`. `restore-logical` compares that number, so a floating, re-tagged, or hand-overridden image cannot misrepresent which client build wrote the archive; the image string alone could.

Neither password reaches a command line. `PGPASSWORD` is set on the environment of the `docker run` invocation and forwarded into the container **by name** (`-e PGPASSWORD`, no `=value`), so the credential is absent from the container's argv and from the host process list — `ps` shows the image and the `pg_dump` flags, nothing else. `restore-logical` passes the target password the same way.

The three files travel together and `restore-logical` requires all three: the `.sha256` for the integrity check, the `.metadata` for the source and dump-client majors. Losing a sidecar means re-running the dump — neither can be reconstructed from the archive alone.

The client image is **pinned to a patch version** (`postgres:16.15`, not `postgres:16`) so the same dump run twice produces archives written by the same `pg_dump` build. `restore-logical` pins the same default, which keeps dump and restore on one client build unless someone overrides it on purpose — and if someone does, the recorded `client_version` is what the restore's compatibility check reads, not the tag.

## Behavior — restore-logical

The recipe runs in two phases. Nothing in the first phase writes to Pulumi config, GCS, or Kubernetes — an abort there leaves the target byte-for-byte as it was.

**Phase 1 — validation, no mutation.** The order below is the order the recipe runs the checks, and it is deliberate: the purely local ones (files, checksum, required flags) come first, then the ones that need the client image, then the ones that need the stack and the cluster. Every check the recipe is capable of making runs before the first destructive call — it fails closed, never "probably fine":

1. The archive exists (a relative `--archive` resolves against the current directory) and **both** sidecars sit next to it. A missing `.metadata` aborts as hard as a missing `.sha256`: `archive sidecars are required`
2. SHA-256 of the archive equals the first field of the `.sha256` sidecar, else `archive checksum mismatch`
3. `--app-password` is set: `--app-password is required before target mutation`. Checked here rather than at deploy time because the target is always recreated — discovering the missing password after the old Cluster is gone would strand the restore
4. Three version facts are collected: the **restore client** from `pg_restore --version` run inside `--client-image`, the **source server** from `source_server_version` in the `.metadata`, and the **dump client** from `client_version` in the `.metadata`. Any of the three missing or non-numeric aborts with `archive metadata or client version is invalid (source=… dump-client=… client=…)` — including an archive written by a `dump-logical` old enough to predate the `client_version` field, which has to be re-dumped
5. `pg_restore --list` must read the archive inside the client image. Catches a file this client cannot parse at all (wrong format, unsupported archive version) before anything is destroyed — the checksum only proves the file is unchanged, not that it is loadable
6. Major compatibility, checked once the stack config has been read. The **target** major is the leading number of `properties.clusters[0].cluster.image.tag` (`16.15-202609101440-standard-trixie` → `16`). Two conditions, both mandatory: target major **equals** the restore client's major, and the dump client's major is ≤ the restore client's. Otherwise `target/client/archive PostgreSQL majors are incompatible (target=… dump-client=… client=…)`; a missing or non-numeric `image.tag` aborts the same way.

   Equality, not "client newer or equal": a `pg_restore` ahead of the server it writes into can emit syntax that server rejects, mid-restore, after the old target is already gone. The source major is deliberately *not* re-compared here — `dump-logical` already refused `source > dump client`, so `source ≤ dump client ≤ client = target` holds by construction, and the archive cannot claim a source the target cannot take
7. Target-state probe. **Any** of the following counts as existing state and requires `--replace-data yes`:
   - the target `Cluster` CR exists in `--namespace`
   - `properties.clusters[0].cluster.bootstrap.recovery.sourceCluster` is on the stack
   - `properties.sourceSecret.name` is on the stack
   - `gs://<backup.bucket>/<bucketPath>/<cluster>/` lists at least one object

   Without the flag: `target state requires --replace-data yes (existing Cluster, physical import config, or own backup archive)`. The probe fails closed at every step, and every tolerated "absent" answer is matched on its exact message: `error: configuration key '<path>' not found for stack '<stack>'` from `pulumi config get`, an `Error from server (NotFound):` line from `kubectl get cluster`, output containing `matched no objects` from `gcloud storage ls`. Any other failure aborts the run instead of being read as a clean target. A backup bucket, bucket path, or key filepath that is only *partly* set on the stack, or a key file that is not on disk, aborts with `cannot safely inspect the target backup archive`: the recipe will not guess that a prefix it cannot list is empty

**Phase 2 — mutation:**

8. Removes the physical recovery config from the stack — `properties.clusters[0].cluster.bootstrap.recovery` and `properties.sourceSecret`. Only the exact `error: configuration key '<path>' not found for stack '<stack>'` reply is tolerated; any other `pulumi config rm` failure aborts rather than being swallowed. Left in place, the recreated cluster would retry the cross-major base-backup replay and fail identically
9. Clears this cluster's **own** backup archive (`gs://<backup.bucket>/<bucketPath>/<cluster>/`, with the `postgres-backup-sa` key) — on every run, replacement or not. A new incarnation whose WAL-archive destination still holds the previous one's `base/` + `wals/` is rejected ([`Expected empty archive`](troubleshooting.md)). Only a `gcloud storage rm` whose output contains `matched no objects` is read as "nothing to clear"; any other failure aborts. Bucket, bucket path, and backup key must be on the stack — i.e. [`configure-backup`](backup.md) has run
10. When the Cluster CR existed: deletes it (`--wait --timeout=300s`), removes leftover `cnpg.io/jobRole=full-recovery` Jobs/pods from earlier failed recoveries, then waits for every `cnpg.io/cluster=<cluster>` pod to disappear — 30 probes, 2s apart, a fixed **60-second** budget with no flag to raise it (unlike [`reset-cluster`](import.md#reset-re-import-into-a-running-cluster), which exposes `--retries`/`--interval`). A pod still present at the end: `target pods still present after 60 seconds; refusing to delete PVCs`, the offending pods are printed, and the PVCs are left intact so the data is still there when the stuck pod is cleared. Only after the wait passes are the data PVCs deleted
11. `pulumi refresh --yes` — unconditionally, not only when replacing, so the apply below reconciles against real cluster state instead of diffing a phantom
12. Chains [`deploy-cluster`](cluster.md) with `--app-password`, `--cluster`, `--namespace`, `--stack` — an empty `logto` database from `initdb`, owned by the app role, with the password you passed
13. Port-forwards target `svc/logto-rw` to `127.0.0.1:<port>` (default `15433`) and reads the **target** `logto-app` Secret
14. Runs `pg_restore --clean --if-exists --exit-on-error --single-transaction --no-owner --no-acl` with the target-major client. One transaction: any single object failure rolls the entire restore back, leaving an empty database rather than a half-loaded one
15. Runs `ANALYZE VERBOSE` (`ON_ERROR_STOP=1`). `pg_restore` loads rows but no planner statistics — without this the first production queries plan against an empty `pg_statistic`

## Flags — dump-logical

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--source-cluster` | Yes | — | Source **Kubernetes cluster** name (e.g. `dcr-experiments`) — the `.env.<env>.<name>` suffix. Not the CNPG Cluster name; contrast [`--source-cnpg-cluster`](import.md#flags), which names the backup folder in the physical path |
| `--source-kubeconfig` | No | `KUBECONFIG` from the source env file | Use when no env file exists for the source |
| `--source-env-file` | No | first `.env.*.<source-cluster>` match | Must contain a `KUBECONFIG` line |
| `--source-namespace` | No | `dev` | Namespace of the source Cluster — the lab source runs in `dev`, the prod target in `prod` |
| `--source-service` | No | `logto-rw` | Source read/write Service to port-forward |
| `--source-database` | No | `logto` | Database to export — one per archive |
| `--source-secret` | No | `logto-app` | Source Secret read for `username`/`password` |
| `--output` / `-o` | No | `scratch/postgres/<source-cluster>-<database>-<UTC timestamp>.dump` | Relative paths resolve against the current directory. `scratch` is gitignored — archives never reach the repo. Written under `umask 077` |
| `--client-image` | No | `postgres:16.15` | Pinned to the target's patch version. Its major must be ≥ the **source** major (enforced here) and ≤ the **target** major (enforced by `restore-logical`) — for a PG 14 source into a PG 16 target, only 14–16 are valid and 16 is the one that needs no second image at restore time. Both the tag and the reported version land in the `.metadata` |
| `--port` | No | `15432` | Local port-forward port; the recipe aborts if it is already bound |

## Flags — restore-logical

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--archive` / `-f` | Yes | — | Custom-format archive from `dump-logical`; **both** the `.sha256` and `.metadata` sidecars must be next to it |
| `--app-password` / `-p` | Yes | — | Password for the target owner role, handed to `deploy-cluster`. Never generated, and checked before any mutation — the target is always recreated, so there is no path that skips it |
| `--replace-data` | For **any** existing target state | `no` | Required when the Cluster CR exists, when `bootstrap.recovery`/`sourceSecret` are still on the stack, or when this cluster's backup prefix holds objects. `yes` deletes the Cluster CR, its data PVCs, and this cluster's own backup archive |
| `--cluster` / `-c` | No | `logto` | Target Cluster name — must match `properties.clusters[0].cluster.name` |
| `--namespace` / `-n` | No | `prod` | Namespace holding the target Cluster |
| `--service` | No | `logto-rw` | Target read/write Service to port-forward |
| `--database` | No | `logto` | Target database, created by the target's `bootstrap.initdb` |
| `--target-secret` | No | `logto-app` | Target Secret read for the restore connection |
| `--client-image` | No | `postgres:16.15` | Pinned. Its `pg_restore --version` major must **equal** the target's `image.tag` major and be ≥ the archive's recorded `client_version` major; both are checked before any mutation |
| `--port` | No | `15433` | Local port-forward port, distinct from the dump's `15432` so both can be open |
| `--stack` / `-s` | No | `$PULUMI_STACK` | No dev fallback |

## Quiesce the Source Before the Final Export

`pg_dump` takes a consistent snapshot at the moment it starts ([pg_dump](https://www.postgresql.org/docs/current/app-pgdump.html)) — every commit the source accepts afterwards is **not** in the archive and is lost at cutover. A rehearsal dump against a live source is fine; the cutover dump is not.

**In the source cluster** (its own kubeconfig, not the target shell):

```bash
kubectl --kubeconfig <source-kubeconfig> scale deployment -n <source-namespace> -l app=<app> --replicas=0
```

---

**Confirm no writers remain before the cutover dump:**

```bash
kubectl --kubeconfig <source-kubeconfig> exec -n <source-namespace> logto-1 -c postgres -- psql -U postgres -d logto -c "select usename, state, query from pg_stat_activity where datname = 'logto' and pid <> pg_backend_pid()"
```

## Roles and ACLs Are Not Imported

`pg_dump` exports a single database, never cluster-wide objects: roles, tablespaces, and other databases stay behind ([pg_dump](https://www.postgresql.org/docs/current/app-pgdump.html)). On top of that the recipes pass `--no-owner --no-acl` on both ends, so no `ALTER ... OWNER TO` and no `GRANT`/`REVOKE` statements are written or replayed.

Consequence, and it is deliberate: every restored object ends up owned by the **target's** app role with the target's default privileges. The target's `logto-app` password is the one passed to `restore-logical --app-password`, not the source's. A source that had several roles with differentiated grants must have them recreated by hand on the target — the archive cannot tell you they existed.

`--quote-all-identifiers` is the other cross-major guard: identifiers are emitted quoted, so a name that became a reserved word in the newer major still restores.

## Why Not CNPG's bootstrap.initdb.import

CloudNativePG has a first-class logical import — [`bootstrap.initdb.import`](https://cloudnative-pg.io/docs/devel/database_import), in `microservice` (one database) or `monolith` (several databases plus roles) mode. It is the official model for a cross-major move and it runs `pg_dump`/`pg_restore` for you, inside the target pod, at bootstrap time.

It does not fit here: the operator's import streams straight from the source server declared in `externalClusters`, so the **target pods must reach the source PostgreSQL over the network**. Source and target live in separate Kubernetes clusters in separate GCP projects, reachable only through separate kubeconfigs and local port-forwards — there is no routable path from a `dcr-kube1` pod to the `dcr-experiments` Service. The recipes therefore land the dump as a durable local archive and push it in, which also buys three things the in-pod import does not offer: the export is re-restorable without re-reading the source, it is checksum-verified, and the source only has to be quiesced once.

Reserved for a future revision: if the two clusters ever get a routable path (VPC peering or a source-side load balancer), `bootstrap.initdb.import` becomes the better option and these recipes become the offline fallback.

## After the Restore

- `just postgres verify` — pool, operator, Cluster phase, pods, PVCs, Services, ScheduledBackup
- Backups restart on their own: the recreated Cluster archives WAL and the ScheduledBackup runs daily against the now-empty bucket prefix ([backup](backup.md))
- Row counts are the only real proof the import worked. Compare a few tables against the source before pointing the app at `logto-rw.prod.svc.cluster.local:5432`
- Remove the source reader key if [`configure-source`](import.md) had been run earlier for the failed physical attempt — the logical path does not use it

## Warnings

- **`--replace-data yes` is destructive.** It deletes the target Cluster CR, its data PVCs, **and** this cluster's own base + WAL archive in GCS. There is no in-place merge — the restore always lands in a freshly created empty database, with or without the flag. The flag only decides whether the recipe is allowed to destroy pre-existing state to get there.
- **Physical recovery config is stripped, not restored.** After a logical import the stack no longer carries `bootstrap.recovery` or `sourceSecret`. To go back to the physical path, re-run [`configure-source`](import.md).
- **Untrusted extensions fail.** The restore connects as the app owner, not a superuser, so a `CREATE EXTENSION` for an untrusted extension aborts — and with `--single-transaction --exit-on-error` that rolls back the whole restore. Check `select extname from pg_extension` on the source first and install what is missing before retrying.
- **The dump is scoped to what the app role can read.** It runs as the source's app owner, not a superuser: an object owned by another role that it cannot select from fails the export outright — loudly, not as a silently empty table.
- **Archives are credentials-adjacent.** `scratch/postgres/` holds production rows in the clear. `umask 077` keeps the archive and both sidecars owner-only on the workstation, but the contents are gitignored, not encrypted, and a copy made elsewhere carries no such protection — delete the archive when the migration is signed off.
- **One database per archive.** `--source-database` dumps exactly one. A source with several application databases needs one dump/restore pair each.

## Official References

- [CloudNativePG — Importing Postgres databases](https://cloudnative-pg.io/docs/devel/database_import)
- [PostgreSQL — `pg_dump`](https://www.postgresql.org/docs/current/app-pgdump.html)
- [PostgreSQL — `pg_restore`](https://www.postgresql.org/docs/current/app-pgrestore.html)
