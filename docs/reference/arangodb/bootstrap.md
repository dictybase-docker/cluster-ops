# Cross-Project Restic Bootstrap

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

Production first load is a **cross-project restic bootstrap**. The in-cluster restore Job (restic → arangorestore) reads a snapshot straight out of a GCS bucket owned by a **different GCP project**. 

The data never touches a laptop, the snapshot is never copied, and the source credentials never mix with this cluster's own backups.

```text
source GCP project                         this cluster's GCP project
┌─────────────────────────────┐            ┌──────────────────────────────────┐
│ GCS bucket (restic repo)    │  HTTPS     │ ns prod                          │
│ snapshot <id>               │◄───────────│ Secret dictycr-source (READ-ONLY)│
│ SA arangodb-restic-reader   │  restic    │   resticPass / gcsProject / JSON │
│ roles/storage.objectViewer  │  restore   │ Job arangodb-restore-bootstrap-* │
└─────────────────────────────┘  --no-lock │   init: restic restore           │
                                           │   main: arangorestore → coord    │
                                           │ Secret dictycr (THIS cluster's   │
                                           │   ongoing backups — untouched)   │
                                           └──────────────────────────────────┘
```

## 1. Before You Start

- **No database setup is needed.** The restore runs with `--create-database true --all-databases`, so every database in the dump is created on the target with the source's collections, indexes, and users. The restore Job reads only Secret `arangodb-pass` from §3.2 — it never reads Secret `backend`.
- **Safety check:** if databases were already created on this cluster for some reason, confirm they hold **no documents you need to keep**, since collections matching the dump get overwritten by name.

You also need three facts about the source, from whoever owns that project:
- source GCP **project id**
- source **bucket** name holding the restic repository
- source **restic repository password** — this is very likely **not** the same password as this cluster's `dictycr` restic password

## 2. Grant Read Access in the Source Project

The restore needs an identity in the **source** project that can read that bucket and nothing else. This repo never manages IAM in a foreign project through Pulumi — it is plain gcloud, wrapped in one recipe for convenience.

If the source project is also managed by this repository, activate its environment first so `gcloud` authenticates as an admin there. (The recipe defaults `--source-project` from the active environment's `PROJECT_ID`):

```bash
# 1. Temporarily switch to the source cluster's environment
just cluster-env --env <source-env> --cluster <source-cluster>

# 2. Grant the role and mint the key
just arangodb grant-source-bucket-reader --bucket <source-bucket>

# 3. Read app username only (save output for finalize-bootstrap)
just arangodb source-app-user --namespace <source-namespace>

# 4. Print source restic repository password (save for configure-source-secrets)
just arangodb source-secret-value --namespace <source-namespace>

# 5. Return to this cluster's environment for the rest of the guide
just cluster-env --env prod --cluster <prod-cluster>
```

`--bucket` may be omitted — it defaults to `properties.bucket` from the
arangodb-backup stack in the active cluster env; the bucket's existence is
verified before any IAM change.

If you do not hold IAM admin on the source project, send these three gcloud commands to whoever does:

```bash
gcloud iam service-accounts create arangodb-restic-reader \
  --project "$SOURCE_PROJECT" \
  --display-name "ArangoDB restic cross-project reader"

gcloud storage buckets add-iam-policy-binding "gs://$SOURCE_BUCKET" \
  --member "serviceAccount:arangodb-restic-reader@${SOURCE_PROJECT}.iam.gserviceaccount.com" \
  --role roles/storage.objectViewer \
  --project "$SOURCE_PROJECT"

gcloud iam service-accounts keys create "$KEY_FILE" \
  --iam-account "arangodb-restic-reader@${SOURCE_PROJECT}.iam.gserviceaccount.com" \
  --project "$SOURCE_PROJECT"
```

Notes:
- **Least privilege:** bucket-level `roles/storage.objectViewer` (list + get). Never project-wide `roles/storage.admin`, never write.
- SA creation and the IAM binding are idempotent. **Key creation is not** — each run mints another key that keeps working until deleted. The recipe skips key creation when the key file already exists; audit with `gcloud iam service-accounts keys list`.

## 3. Create the Source Secret

Two Secrets now live side by side in `prod`, and mixing them up is the main hazard in this section:

| Secret | Identity | Used by | Bucket |
|--------|----------|---------|--------|
| `dictycr` | this cluster's project, read/write | ongoing backup Job + CronJob, DR drills | `restic-arangodb-backup-prod` |
| `dictycr-source` | **source** project, read-only | the bootstrap restore in this section, nothing else | the source bucket |

Both carry the **same three key names** (`resticPass`, `gcsProject`, `gcsCredentials`) because the restore Job reads those exact names — only the values differ.

```bash
just arangodb configure-source-secrets \
  --restic-password '<SOURCE restic repository password>' \
  --gcs-project '<SOURCE project id>' \
  --gcs-key-file credentials/<source-project-id>/arangodb-restic-reader.json
```

> **Two values people get wrong here.** `--restic-password` is the **source** repository's password, not this cluster's. `--gcs-project` is the **source** project id, not the project this cluster runs in. Both mistakes surface as an opaque restic failure later; see [Troubleshooting](troubleshooting.md).

## 4. Pick a Snapshot

```bash
just arangodb list-source-snapshots --namespace prod --bucket <source-bucket>
```

This runs restic inside the cluster with the source credentials, so a successful listing is also your proof that IAM, the password, and the bucket name are all correct — before anything writes to the database.

Pick one snapshot id from the output and write down its timestamp: **that timestamp is this cluster's recovery point.** Everything the source wrote after it is not in this load.

`--snapshot` is optional. If omitted (or given as `latest`), the bootstrap resolves the newest snapshot in the source repository with a read-only restic pod run, echoes the resolved id, and records it in the `arangodb-restore` stack config — the recovery point stays auditable after the fact. Pinning an explicit id is still recommended: it fixes the recovery point at the moment you choose it, not at the moment the restore starts.

## 5. Run the Bootstrap

```bash
just arangodb bootstrap-from-snapshot \
  --namespace prod \
  --bucket <source-bucket>
# or pin an explicit id: --snapshot <snapshot-id>
```

One command, four phases:

1. **Configure** — points the `arangodb-restore` stack at the source bucket and `dictycr-source`, sets `noLock=true`, generates a `bootstrap-<UTC timestamp>` restore id, and computes `confirmTarget` (`<namespace>/<server>/<restoreId>`).
2. **Restore** — the same Job the DR drill uses: init container `restic restore`, then `arangorestore --all-databases --include-system-collections --create-database true`.
3. **Follow** — waits for the init container, tails both container logs, waits for the Job, reports the scratch PVC. 30-minute budget per phase by default.
4. **Reset** — puts the restore stack back on `restic-arangodb-backup-prod` + `dictycr` + `noLock=false`. This runs from a shell `trap`, so it happens **even if the restore fails**. Without it, a later DR drill would silently read the foreign source bucket.

> **Why `--no-lock`:** restic normally takes an exclusive lock, which means writing a lock file into the repository. A read-only `objectViewer` identity cannot write it and the restore dies with HTTP 403. `--no-lock` is set only for this bootstrap path; DR drills keep locking because they use the full-access `dictycr` identity.

To run the two halves separately — for instance to review the config before applying — use `just arangodb configure-bootstrap ...` then `just arangodb apply-restore`, and run `just arangodb reset-restore-config` yourself afterwards. Add `--database <name>` to load one database instead of the whole instance (see [single-database restore](restore-database.md)); `import-database` is the composite that pairs that overlay with the reset trap.

## 6. After the Restore

Run [Finalize Import](#finalize-import) first. It resets restore config, rotates credentials, and verifies app access; then confirm the checks below.

1. **Confirm data landed.** The Job shows `Completed`, the `arangorestore` log lists databases created and restored, and a spot check finds documents:

   ```bash
   kubectl get jobs -n prod
   kubectl logs -n prod job/arangodb-restore-<restore-id> -c arangorestore --tail=50
   ```

2. **Confirm the restore config reset happened**, so the next DR drill cannot read the source bucket:

   ```bash
   pulumi -C arangodb-restore config get --path properties.bucket          # restic-arangodb-backup-prod
   pulumi -C arangodb-restore config get --path properties.resticSecret.name  # dictycr
   ```

3. **Do not run the loaders** after a successful bootstrap — they would overwrite restored graphs. The one exception is a database that was genuinely absent from the dump.
4. **Remove the local source SA JSON** if you no longer need it. After bootstrap, ongoing backups use `dictycr` and this cluster's own bucket; `deploy-backup` aborts if it finds `dictycr-source` configured, so the bootstrap identity cannot be shared back in.

## Finalize Import

The restore includes source `_users`, so root and the application user both carry source credentials. Run one required post-import recipe with the imported app username and a **new destination-only password**:

```bash
just arangodb finalize-bootstrap \
  --app-user '<existing-app-user>' \
  --app-password '<new-destination-app-password>'
```

The recipe runs these four steps, in order:

1. Reset `arangodb-restore` config to this cluster's own bucket, `dictycr`, and locking enabled.
2. Reset root password from this cluster's `arangodb-pass` Secret.
3. Keep imported app username, set new app password in ArangoDB, update Pulumi-owned Secret `backend`, and reapply configured `rw` grants. **Does not create databases** — import already restored them.
4. Authenticate as app user against every configured database.

Reruns are safe: each Pulumi Job gets a unique run id. Before creating the next one, recipes remove prior Jobs with no running containers and refresh Pulumi state; a Job with a running container stops preflight. Passwords remain in Pulumi secret config and Kubernetes Secrets, not container arguments.

`reset-root-password` and `configure-app-credentials` remain available as individual recovery steps. Use `create-databases` only for a missing logical database or the loader path in [databases.md](databases.md#when-to-run-this).
