# PostgreSQL Logical Import Details (Cross-Major Dump/Restore)

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Moves one database between two clusters that **cannot** exchange a physical backup — most often because their PostgreSQL majors differ. Two recipes, one durable artifact between them:

1. `dump-logical` — port-forwards the **source** cluster, exports the database with `pg_dump --format=custom`, writes the archive plus a `.sha256` and a `.metadata` sidecar
2. `restore-logical` — verifies the archive, prepares an **empty** target (physical recovery config removed), then loads it with `pg_restore`

Logical means SQL-level: schema DDL and row data, replayed by the target server. Nothing about the source's on-disk layout, WAL, or page format crosses over, which is exactly why it survives a major-version change that [physical recovery](import.md) cannot.

## Physical vs Logical — Pick One

| | Physical ([`configure-source`](import.md) + [`reset-cluster`](import.md#reset-re-import-into-a-running-cluster)) | Logical (this doc) |
|---|---|---|
| Mechanism | Operator replays base backup + WAL into the PVC | `pg_dump` archive replayed as SQL |
| Major versions | **Source and target must match** | Source major ≤ client/target major |
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

`--replace-data yes` is required only when the target `Cluster` CR already exists. Drop it for a target that was never created.

## Behavior — dump-logical

1. Resolves the source kubeconfig: `--source-kubeconfig` > `KUBECONFIG` in `--source-env-file` > `KUBECONFIG` in the first `.env.*.<source-cluster>` match. `${PWD}` and `${CLUSTER_NAME}` inside that value are expanded, relative paths resolve against the repo root. No kubeconfig found → the run stops before touching anything
2. Aborts if local `--port` (default `15432`) is already bound, then port-forwards `svc/<source-service>` in `--source-namespace` and waits up to 30s for the tunnel
3. Reads `username`/`password` from the **source** `logto-app` Secret — the source app owner, not a superuser and not the target's credentials
4. Records the source `server_version` (`psql -Atqc 'show server_version'`) and prints it — the one value that proves which major the archive came from
5. Runs `pg_dump --format=custom --no-owner --no-acl --quote-all-identifiers` inside a `--client-image` container, writing `<archive>.partial` and renaming only on success. An interrupted export leaves no file that looks complete; the `EXIT` trap removes the partial and kills the port-forward
6. Writes `<archive>.sha256` (`<digest>  <basename>`) and `<archive>.metadata` (`source_cluster`, `source_database`, `source_server_version`, `client_image`, `created_at`)

The three files travel together. `restore-logical` reads the `.sha256`; the `.metadata` is for the human deciding whether this archive is the one.

## Behavior — restore-logical

1. Verifies the archive exists, the `.sha256` sidecar sits next to it, and the digest matches. A mismatch aborts **before** anything in the target is touched
2. Refuses when the target `Cluster` CR exists and `--replace-data yes` was not passed
3. Removes the physical recovery config from the stack — `properties.clusters[0].cluster.bootstrap.recovery` and `properties.sourceSecret`. Left in place, the recreated cluster would try the cross-major base-backup replay again and fail identically
4. Clears this cluster's **own** backup archive (`gs://<backup.bucket>/<bucketPath>/<cluster>/`, with the `postgres-backup-sa` key). A new incarnation whose WAL-archive destination still holds the previous one's `base/` + `wals/` is rejected ([`Expected empty archive`](troubleshooting.md))
5. When replacing: deletes the Cluster CR (300s timeout), removes leftover `cnpg.io/jobRole=full-recovery` Jobs/pods from earlier failed recoveries, waits for every instance pod to disappear, deletes the data PVCs, then `pulumi refresh` so the next apply recreates rather than diffs a phantom
6. Chains [`deploy-cluster`](cluster.md) with `--app-password` — an empty `logto` database from `initdb`, owned by the app role, with the password you passed
7. Port-forwards target `svc/logto-rw` to `127.0.0.1:<port>` (default `15433`) and reads the **target** `logto-app` Secret
8. Runs `pg_restore --clean --if-exists --exit-on-error --single-transaction --no-owner --no-acl`. One transaction: any single object failure rolls the entire restore back, leaving an empty database rather than a half-loaded one
9. Runs `ANALYZE VERBOSE`. `pg_restore` loads rows but no planner statistics — without this the first production queries plan against an empty `pg_statistic`

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
| `--output` / `-o` | No | `scratch/postgres/<source-cluster>-<database>-<UTC timestamp>.dump` | Relative paths resolve against the current directory. `scratch` is gitignored — archives never reach the repo |
| `--client-image` | No | `postgres:16` | Must be the **target** major or newer. `pg_dump` may be newer than the server it reads, never older than the server that will restore it |
| `--port` | No | `15432` | Local port-forward port; the recipe aborts if it is already bound |

## Flags — restore-logical

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--archive` / `-f` | Yes | — | Custom-format archive from `dump-logical`; the `.sha256` sidecar must be next to it |
| `--app-password` / `-p` | Yes | — | Password for the target owner role, handed to `deploy-cluster`. Never generated — the recipe aborts without it, even when the target already exists (it is recreated) |
| `--replace-data` | When the target Cluster exists | `no` | `yes` deletes the Cluster CR, its data PVCs, and this cluster's own backup archive |
| `--cluster` / `-c` | No | `logto` | Target Cluster name — must match `properties.clusters[0].cluster.name` |
| `--namespace` / `-n` | No | `prod` | Namespace holding the target Cluster |
| `--service` | No | `logto-rw` | Target read/write Service to port-forward |
| `--database` | No | `logto` | Target database, created by the target's `bootstrap.initdb` |
| `--target-secret` | No | `logto-app` | Target Secret read for the restore connection |
| `--client-image` | No | `postgres:16` | `pg_restore` must be the target major |
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

- **`--replace-data yes` is destructive.** It deletes the target Cluster CR, its data PVCs, **and** this cluster's own base + WAL archive in GCS. There is no in-place merge; the restore always lands in a freshly created empty database.
- **Physical recovery config is stripped, not restored.** After a logical import the stack no longer carries `bootstrap.recovery` or `sourceSecret`. To go back to the physical path, re-run [`configure-source`](import.md).
- **Untrusted extensions fail.** The restore connects as the app owner, not a superuser, so a `CREATE EXTENSION` for an untrusted extension aborts — and with `--single-transaction --exit-on-error` that rolls back the whole restore. Check `select extname from pg_extension` on the source first and install what is missing before retrying.
- **The dump is scoped to what the app role can read.** It runs as the source's app owner, not a superuser: an object owned by another role that it cannot select from fails the export outright — loudly, not as a silently empty table.
- **Archives are credentials-adjacent.** `scratch/postgres/` holds production rows in the clear. It is gitignored, not encrypted — delete the archive when the migration is signed off.
- **One database per archive.** `--source-database` dumps exactly one. A source with several application databases needs one dump/restore pair each.

## Official References

- [CloudNativePG — Importing Postgres databases](https://cloudnative-pg.io/docs/devel/database_import)
- [PostgreSQL — `pg_dump`](https://www.postgresql.org/docs/current/app-pgdump.html)
- [PostgreSQL — `pg_restore`](https://www.postgresql.org/docs/current/app-pgrestore.html)
