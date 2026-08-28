# Production ArangoDB on the Stateful Database Pool

Provisioning guide for production ArangoDB **Cluster** on kOps `stateful-db`.

**Status**: Production procedure. Use `Pulumi.prod.yaml` configs only. Do not edit lab `arangodb-single` stacks.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Pool Check](#1-pool-check)
- [2. Prerequisites](#2-prerequisites)
- [3. Install ArangoDB](#3-install-arangodb)
  - [3.1 Operator](#31-operator)
  - [3.2 Cluster](#32-cluster)
  - [3.3 Databases (Optional)](#33-databases-optional)
- [4. Import Data](#4-import-data)
  - [4.1 Before You Start](#41-before-you-start)
  - [4.2 Grant Read Access in the Source Project](#42-grant-read-access-in-the-source-project)
  - [4.3 Create the Source Secret](#43-create-the-source-secret)
  - [4.4 Pick a Snapshot](#44-pick-a-snapshot)
  - [4.5 Run the Bootstrap](#45-run-the-bootstrap)
  - [4.6 After the Restore](#46-after-the-restore)
  - [4.7 Alternative: Loaders](#47-alternative-loaders)
- [5. Backup & Restore](#5-backup--restore)
  - [5.1 Deploy Backup](#51-deploy-backup)
  - [5.2 Restore Drill](#52-restore-drill)
- [6. Teardown](#6-teardown)
- [7. Verify](#7-verify)
- [8. Troubleshooting](#8-troubleshooting)
- [9. Related Documents](#9-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Configure backup secrets (creates namespaces + Secret dictycr)
just arangodb configure-backup-secrets \
  --restic-password '<restic-pass>' \
  --gcs-project '<gcp-project-id>' \
  --gcs-key-file credentials/<project-id>/backup-gcs-sa.json

# 2. Deploy operator
just arangodb deploy-operator

# 3. Deploy cluster
just arangodb deploy-cluster --root-password '<root-password>'

# 4. Import data — first load from a restic snapshot in another GCP project.
#    This creates the databases; nothing needs to pre-create them.
just arangodb grant-source-bucket-reader --source-project <source-id> --bucket <source-bucket>
just arangodb configure-source-secrets \
  --restic-password '<source-restic-pass>' \
  --gcs-project '<source-project-id>' \
  --gcs-key-file credentials/<source-project-id>/arangodb-restic-reader.json
just arangodb list-source-snapshots --namespace prod --bucket <source-bucket>
just arangodb bootstrap-from-snapshot --namespace prod --bucket <source-bucket> --snapshot <id>

# 5. Deploy backup (immediate job + cronjob)
just arangodb deploy-backup

# 6. Verify installation
just arangodb verify
```

Optional steps:
```bash
# Create databases / application user by hand. Not part of the normal flow —
# the import creates the databases. Available if you ever need it (see §3.3).
just arangodb create-databases --app-user '<user>' --app-password '<password>'

# Loaders, if there is no restic snapshot to import from
# (require Pulumi.prod.yaml — doesn't exist yet)
just arangodb deploy-loader --folder arangodb-dataloader

# Restore drill (on clone cluster)
just arangodb configure-restore --namespace <ns>
just arangodb apply-restore

# Teardown (clone only, destructive)
just arangodb teardown --namespace prod --delete-pvcs yes
```

---

## 1. Pool Check

Verify 3 Ready amd64 nodes with `dedicated=database:NoSchedule` taint.
→ [Pool requirements](reference/arangodb/pool-requirements.md)

```bash
just arangodb check-pool
```

Prints PASS/FAIL for: node count, architecture (amd64), taint, Ready status. Shows zone spread.

---

## 2. Prerequisites

Before installing ArangoDB, apply `backup_secrets` — creates namespaces `prod`/`operators` and Secret `dictycr`. The operator needs the `operators` namespace to exist.
→ [Backup details](reference/arangodb/backup.md)

```bash
just arangodb configure-backup-secrets \
  --restic-password '<restic-pass>' \
  --gcs-project '<gcp-project-id>' \
  --gcs-key-file credentials/<project-id>/backup-gcs-sa.json
```

Also required: CSI + StorageClasses from [`pulumi-setup.md` §6](pulumi-setup.md#6-first-apply--storageclass).

---

## 3. Install ArangoDB

Work in `just cluster-env --env prod --cluster <prod-cluster>`. Each recipe picks up `$PULUMI_STACK` automatically.

### 3.1 Operator

Installs `kube-arangodb` Helm chart into `operators` namespace.
→ [Operator details](reference/arangodb/operator.md)

```bash
just arangodb deploy-operator
```

### 3.2 Cluster

Creates root-password Secret and 9-member Cluster (3 agents, 3 dbservers, 3 coordinators).
→ [Cluster details](reference/arangodb/cluster.md)

```bash
just arangodb deploy-cluster --root-password '<strong-root-password>'
```

Coordinator address: `http://arangodb.prod.svc.cluster.local:8529`

### 3.3 Databases (Optional)

**Nothing here needs running in the normal flow.** [§4 Import Data](#4-import-data) creates the databases, their collections, and their users from the dump.

The recipe exists for the cases where something is missing afterwards — a database the dump did not contain, or an application user you need to (re)create:

```bash
just arangodb create-databases --app-user '<user>' --app-password '<password>'
```

It creates the application user, Secret `backend`, and the 7 logical databases with `rw` grants, and is safe to run against databases that already exist.
→ [Databases details](reference/arangodb/databases.md)

---

## 4. Import Data

Sections 1–3 give you a running, **empty** cluster — no application databases. This section creates and fills them in one shot.

**Production first load is a cross-project restic bootstrap:** the in-cluster restore Job (restic → arangorestore) reads a snapshot straight out of a GCS bucket owned by a **different GCP project**. The data never touches a laptop, the snapshot is never copied, and the source credentials never mix with this cluster's own backups.

Use the loaders in [§4.7](#47-alternative-loaders) only when there is no restic snapshot to bootstrap from.

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

### 4.1 Before You Start

| Requirement | Comes from |
|-------------|------------|
| Namespace `prod` exists | [§2 Prerequisites](#2-prerequisites) (`configure-backup-secrets`) |
| Secret `dictycr` exists (this cluster's own backup identity) | [§2 Prerequisites](#2-prerequisites) |
| Cluster Ready, Secret `arangodb-pass` exists | [§3.2 Cluster](#32-cluster) |

**No database setup is needed first.** The restore runs with `--create-database true --all-databases`, so every database in the dump is created on the target with the source's collections, indexes, and users. The restore Job reads only Secret `arangodb-pass` from §3.2 — it never reads Secret `backend`.

If databases were already created on this cluster for some reason, that is recoverable, not fatal — just confirm they hold **no documents you need to keep**, since collections matching the dump get overwritten by name.

You also need three facts about the source, from whoever owns that project:

- source GCP **project id**
- source **bucket** name holding the restic repository
- source **restic repository password** — this is very likely **not** the same password as this cluster's `dictycr` restic password

### 4.2 Grant Read Access in the Source Project

The restore needs an identity in the **source** project that can read that bucket and nothing else. This repo never manages IAM in a foreign project through Pulumi — it is plain gcloud, wrapped in one recipe for convenience.

If you hold IAM admin on the source project:

```bash
just arangodb grant-source-bucket-reader \
  --source-project <source-project-id> \
  --bucket <source-bucket>
```

If you do not, send these three commands to whoever does:

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
- Keep the JSON key off git. Delete it from your machine once §4.3 has stored it in the cluster.

### 4.3 Create the Source Secret

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

> **Two values people get wrong here.** `--restic-password` is the **source** repository's password, not this cluster's. `--gcs-project` is the **source** project id, not the project this cluster runs in. Both mistakes surface as an opaque restic failure later; see [§8 Troubleshooting](#8-troubleshooting).

This creates Secret `dictycr-source` only. It creates no namespaces — if `prod` is missing the recipe stops and points you back at §2.

### 4.4 Pick a Snapshot

```bash
just arangodb list-source-snapshots --namespace prod --bucket <source-bucket>
```

This runs restic inside the cluster with the source credentials, so a successful listing is also your proof that IAM, the password, and the bucket name are all correct — before anything writes to the database.

Pick one snapshot id from the output and write down its timestamp: **that timestamp is this cluster's recovery point.** Everything the source wrote after it is not in this load.

`--snapshot latest` is rejected by the next step on purpose. `latest` can mean a different snapshot between the moment you list and the moment you restore, which would make the restore unreproducible and the recovery point unknowable.

### 4.5 Run the Bootstrap

```bash
just arangodb bootstrap-from-snapshot \
  --namespace prod \
  --bucket <source-bucket> \
  --snapshot <pinned-snapshot-id>
```

One command, four phases:

1. **Configure** — points the `arangodb-restore` stack at the source bucket and `dictycr-source`, sets `noLock=true`, generates a `bootstrap-<UTC timestamp>` restore id, and computes `confirmTarget` (`<namespace>/<server>/<restoreId>`).
2. **Restore** — the same Job the DR drill uses: init container `restic restore`, then `arangorestore --all-databases --include-system-collections --create-database true`.
3. **Follow** — waits for the init container, tails both container logs, waits for the Job, reports the scratch PVC. 30-minute budget per phase by default.
4. **Reset** — puts the restore stack back on `restic-arangodb-backup-prod` + `dictycr` + `noLock=false`. This runs from a shell `trap`, so it happens **even if the restore fails**. Without it, a later DR drill would silently read the foreign source bucket.

> **Why `--no-lock`:** restic normally takes an exclusive lock, which means writing a lock file into the repository. A read-only `objectViewer` identity cannot write it and the restore dies with HTTP 403. `--no-lock` is set only for this bootstrap path; DR drills keep locking because they use the full-access `dictycr` identity.

To run the two halves separately — for instance to review the config before applying — use `just arangodb configure-bootstrap ...` then `just arangodb apply-restore`, and run `just arangodb reset-restore-config` yourself afterwards.

### 4.6 After the Restore

1. **Confirm data landed.** The Job shows `Completed`, the `arangorestore` log lists databases created and restored, and a spot check finds documents:

   ```bash
   kubectl get jobs -n prod
   kubectl logs -n prod job/arangodb-restore-<restore-id> -c arangorestore --tail=50
   ```

2. **Reset the root password.** The restore brought the source's `_users` with it (`--include-system-collections`), so `root` is now the source's root password. Fix it back to your destination's `arangodb-pass` so your deploy pipelines keep working:

   ```bash
   just arangodb reset-root-password
   ```

3. **Check the application user works.** With `root` fixed, check if your application user exists and has the password your services expect (the restore brought the source's app user too). If missing or wrong, use the [§3.3](#33-databases-optional) recipe to (re)create it:

   ```bash
   just arangodb create-databases --app-user '<user>' --app-password '<password>'
   ```

4. **Confirm the restore config reset happened**, so the next DR drill cannot read the source bucket:

   ```bash
   pulumi -C arangodb-restore config get --path properties.bucket          # restic-arangodb-backup-prod
   pulumi -C arangodb-restore config get --path properties.resticSecret.name  # dictycr
   ```

4. **Do not run the loaders** ([§4.7](#47-alternative-loaders)) after a successful bootstrap — they would overwrite restored graphs. The one exception is a database that was genuinely absent from the dump.

5. **Remove the local source SA JSON** if you no longer need it.

Then continue to [§5 Backup & Restore](#5-backup--restore). Ongoing backups use `dictycr` and this cluster's own bucket; `deploy-backup` aborts if it ever finds `dictycr-source` configured.

### 4.7 Alternative: Loaders

Only when there is no restic snapshot to bootstrap from. These build databases from source files rather than a dump.
→ [Import details](reference/arangodb/import.md)

Loaders write into databases that already exist, so on this path the [§3.3](#33-databases-optional) recipe does become necessary — run it before the loaders.

```bash
just arangodb deploy-loader --folder arangodb-dataloader
just arangodb deploy-loader --folder load-content-from-s3
just arangodb deploy-loader --folder load-uniprot-mapping
```

No loader has a `Pulumi.prod.yaml` in this repo yet — the recipe refuses to run without one.

---

## 5. Backup & Restore

### 5.1 Deploy Backup

Creates GCS bucket, immediate Job, and daily CronJob.
→ [Backup details](reference/arangodb/backup.md)

```bash
just arangodb deploy-backup
```

### 5.2 Restore Drill

Run on **clone cluster only**. In-cluster Job: restic restore → arangorestore.
→ [Restore details](reference/arangodb/restore.md)

```bash
# Configure
just arangodb configure-restore --namespace <target-namespace>

# Apply and follow
just arangodb apply-restore

# List snapshots if needed
just arangodb list-snapshots-in-cluster --namespace <target-namespace>
```

---

## 6. Teardown

**Destructive. Clone only.**
→ [Teardown details](reference/arangodb/teardown.md)

```bash
just arangodb teardown --namespace prod --delete-pvcs yes
```

---

## 7. Verify

Read-only checks: pool, operator, storage, members, jobs. Exits non-zero if any required check fails.

```bash
just arangodb verify
```

Checks: 3 `pool=database` nodes with taint, Running operator, StorageClasses, ArangoDeployment, 9 ready pods, correct PVCs, Service on 8529.

---

## 8. Troubleshooting

→ [Full troubleshooting table](reference/arangodb/troubleshooting.md)

Common issues:
- **No stack name error**: Enter `just cluster-env` first, or pass `--stack <name>`
- **Pod Pending (taint)**: Check CR toleration vs node taint
- **App cannot connect**: Verify `arangodb.prod.svc.cluster.local:8529` and Secret `backend`

---

## 9. Related Documents

**Reference details for this guide:**
- [Pool requirements](reference/arangodb/pool-requirements.md)
- [Operator details](reference/arangodb/operator.md)
- [Cluster details](reference/arangodb/cluster.md)
- [Databases details](reference/arangodb/databases.md)
- [Import details](reference/arangodb/import.md)
- [Backup details](reference/arangodb/backup.md)
- [Restore details](reference/arangodb/restore.md)
- [Teardown details](reference/arangodb/teardown.md)
- [Troubleshooting](reference/arangodb/troubleshooting.md)

**Other documentation:**
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Implementation plan (historical): [`plans/arangodb-production.md`](plans/arangodb-production.md)
