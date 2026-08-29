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
- [4. Import Data](#4-import-data)
  - [4.1 Bootstrap from Snapshot](#41-bootstrap-from-snapshot)
  - [4.2 Fix Authentication](#42-fix-authentication)
  - [4.3 Alternative: Loaders](#43-alternative-loaders)
- [5. Backup & Restore](#5-backup--restore)
  - [5.1 Deploy Backup](#51-deploy-backup)
  - [5.2 Restore Drill](#52-restore-drill)
- [6. Teardown](#6-teardown)
- [7. Verify](#7-verify)
- [8. Troubleshooting](#8-troubleshooting)
- [9. Optional: Databases by Hand](#9-optional-databases-by-hand)
- [10. Related Documents](#10-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Configure backup secrets (creates namespaces + Secret dictycr)
#    Project and key file default from the cluster env: $PROJECT_ID and
#    credentials/$PROJECT_ID/backup-gcs-sa.json
just arangodb configure-backup-secrets --restic-password '<restic-pass>'

# 2. Deploy operator
just arangodb deploy-operator

# 3. Deploy cluster
just arangodb deploy-cluster --root-password '<root-password>'

# 4. Import data — first load from a restic snapshot in another GCP project.
#    This creates the databases; nothing needs to pre-create them.
#    (Run the grant recipe from inside the source cluster's environment first)
just arangodb grant-source-bucket-reader --bucket <source-bucket>
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
# the import creates the databases. Available if you ever need it (see §9).
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

**Everything below runs inside the cluster-env sub-shell.** `just cluster-env --env prod --cluster <prod-cluster>` sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID` and `KUBECONFIG` into that shell, so no recipe needs `--stack` — each one falls back to `$PULUMI_STACK`. Outside the sub-shell recipes fail with `Error: no stack name`. Leave it with `exit` or Ctrl-D.

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
just arangodb configure-backup-secrets --restic-password '<restic-pass>'
```

Only the restic password has to be typed. `--gcs-project` defaults to `$PROJECT_ID` (exported by `cluster-env` from the env file) and `--gcs-key-file` defaults to `credentials/$PROJECT_ID/backup-gcs-sa.json`. That key must be a service account with write access to the backup bucket, and it must exist on **this** machine — `backup_secrets/main.go` reads it locally at `pulumi up` time. Override either flag if your layout differs:

```bash
just arangodb configure-backup-secrets \
  --restic-password '<restic-pass>' \
  --gcs-project '<other-project-id>' \
  --gcs-key-file /path/to/sa.json
```

Also required: CSI + StorageClasses from [`pulumi-setup.md` §6](pulumi-setup.md#6-first-apply--storageclass).

---

## 3. Install ArangoDB

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

---

## 4. Import Data

Sections 1–3 give you a running, **empty** cluster — no application databases. This section creates and fills them in one shot.

### 4.1 Bootstrap from Snapshot

Production first load is a cross-project restic bootstrap: the in-cluster restore Job (restic → arangorestore) reads a snapshot straight out of a GCS bucket owned by a **different GCP project**.
→ [Bootstrap details](reference/arangodb/bootstrap.md)

```bash
# 1. Temporarily switch to the source cluster's environment to grant access
just cluster-env --env <source-env> --cluster <source-cluster>
just arangodb grant-source-bucket-reader --bucket <source-bucket>

# 2. Switch back to this cluster's environment
just cluster-env --env prod --cluster <prod-cluster>

# 3. Create the read-only identity
just arangodb configure-source-secrets \
  --restic-password '<SOURCE-restic-password>' \
  --gcs-project '<SOURCE-project-id>' \
  --gcs-key-file credentials/<source-project-id>/arangodb-restic-reader.json

# 4. List and pin a snapshot
just arangodb list-source-snapshots --namespace prod --bucket <source-bucket>

# 5. Restore the snapshot
just arangodb bootstrap-from-snapshot \
  --namespace prod \
  --bucket <source-bucket> \
  --snapshot <pinned-snapshot-id>
```

### 4.2 Fix Authentication

The restore includes `_users` (`--include-system-collections`), so `root` now carries the source's password. Reset it to this cluster's `arangodb-pass`:

```bash
just arangodb reset-root-password
```

The restore also brought the source's application user. If it is missing or its password is not what your services expect, (re)create it with the [§9](#9-optional-databases-by-hand) recipe:

```bash
just arangodb create-databases --app-user '<user>' --app-password '<password>'
```

Then delete the local source SA JSON and continue to [§5 Backup & Restore](#5-backup--restore). Ongoing backups use `dictycr` and this cluster's own bucket; `deploy-backup` aborts if it finds `dictycr-source` configured.

### 4.3 Alternative: Loaders

Reserved for a future revision. No loader project has a `Pulumi.prod.yaml` in this repo yet, and `deploy-loader` refuses to run without one.
→ [Import details](reference/arangodb/import.md)

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

## 9. Optional: Databases by Hand

**Not part of the normal flow — skip it on a first install.** [§4 Import Data](#4-import-data) creates the databases, their collections, and their users from the dump (`arangorestore --create-database true --all-databases`).

Run it only when something is missing afterwards: a database the dump did not contain, or an application user / Secret `backend` you need to (re)create after `--include-system-collections` restored the source's `_users`.

```bash
just arangodb create-databases --app-user '<user>' --app-password '<password>'
```

Creates the application user, Secret `backend`, and the 7 logical databases (`annotation`, `order`, `stock`, `content`, `cgm_ddb`, `chado`, `annofeature`) with `rw` grants. Both flags are required — `Pulumi.prod.yaml` ships no `user`/`pass`, so omitting them writes an empty `backend` Secret. Safe to re-run against databases that already exist.
→ [Databases details](reference/arangodb/databases.md)

---

## 10. Related Documents

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
