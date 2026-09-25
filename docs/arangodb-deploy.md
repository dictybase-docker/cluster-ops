# Production ArangoDB on the Stateful Database Pool

Provisioning guide for production ArangoDB **Cluster** on kOps `stateful-db`.

**Status**: Production procedure. Use `Pulumi.dcr-kube1.yaml` configs only. Do not edit lab `arangodb-single` stacks.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Pool Check](#1-pool-check)
- [2. Prerequisites](#2-prerequisites)
- [3. Install ArangoDB](#3-install-arangodb)
  - [3.1 Operator](#31-operator)
  - [3.2 Cluster](#32-cluster)
- [4. Import Data](#4-import-data)
  - [4.1 Bootstrap from Snapshot](#41-bootstrap-from-snapshot)
  - [4.2 Finalize Import](#42-finalize-import)
  - [4.3 Alternative: Loaders](#43-alternative-loaders)
- [5. Backup & Restore](#5-backup--restore)
  - [5.1 Deploy Backup](#51-deploy-backup)
  - [5.2 Restore Drill](#52-restore-drill)
  - [5.3 Single Database](#53-single-database)
- [6. Verify](#6-verify)
- [7. Teardown](#7-teardown)
- [8. Troubleshooting](#8-troubleshooting)
- [9. Optional: Databases by Hand](#9-optional-databases-by-hand)
- [10. Related Documents](#10-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# 0. Enter this cluster's environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Pool check — must pass before anything is installed
just arangodb check-pool

# 2. Backup secrets — backup SA + key, then Secret dictycr
just arangodb configure-backup-secrets --restic-password '<restic-pass>'

# 3.1 Deploy operator
just arangodb deploy-operator

# 3.2 Deploy cluster
just arangodb deploy-cluster --root-password '<root-password>'

# 4.1a In the SOURCE cluster's environment: grant reader
exit   # leave this cluster's sub-shell first
just cluster-env --env <source-env> --cluster <source-cluster>
just arangodb grant-source-bucket-reader

# -> prints --restic-password for configure-source-secrets
just arangodb source-secret-value --namespace <source-namespace>

# -> prints --app-user for finalize-bootstrap
just arangodb source-app-user --namespace <source-namespace>

# 4.1b Back in THIS cluster's environment: first load
exit   # leave the source sub-shell
just cluster-env --env prod --cluster <prod-cluster>
just arangodb configure-source-secrets --restic-password '<source-restic-pass>' --gcs-project <source-project-id> --gcs-key-file <source-key.json>
just arangodb list-source-snapshots --namespace prod --bucket <source-bucket>   # pick an id to pin
just arangodb bootstrap-from-snapshot --namespace prod --bucket <source-bucket>

# 4.2 Finalize — rotate root + imported app password (also verifies app login)
just arangodb finalize-bootstrap --app-user '<existing-app-user>' --app-password '<new-destination-app-password>'

# 5. Deploy backup (immediate job + cronjob), then confirm a snapshot landed
just arangodb deploy-backup
just arangodb list-snapshots-in-cluster --namespace prod

# 6. Verify installation
just arangodb verify
```

Optional paths, each in its own run order:

```bash
# Alternative loader path, instead of §4 (§4.3 — reserved for a future revision)
just arangodb create-databases --app-user '<user>' --app-password '<password>'
just arangodb deploy-loader --folder arangodb-dataloader
```

```bash
# Clone cluster only: restore drill, then dispose of the clone (destructive)
just arangodb configure-restore --namespace <ns>
just arangodb apply-restore
just arangodb teardown --namespace prod --delete-pvcs yes
```

```bash
# One database only: point-in-time from own bucket, or cross-cluster import
just arangodb restore-database --namespace <ns> --database <db>
just arangodb import-database --namespace <ns> --bucket <source-bucket> --database <db>
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env prod --cluster <prod-cluster>`. The sub-shell sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Recipes default `--stack` to `$PULUMI_STACK`, so no recipe below needs the flag.
- Outside the sub-shell recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Pool Check

Verify 3 Ready amd64 nodes with `dedicated=database:NoSchedule` taint.
→ [Pool requirements](reference/arangodb/pool-requirements.md)

```bash
just arangodb check-pool
```

---

## 2. Prerequisites

Creates the backup service account + key, and Secret `dictycr`. StorageClasses, the `prod`/`operators` namespaces, and each project's `Pulumi.<cluster>.yaml` must already exist — see the [prerequisite list](reference/arangodb/pool-requirements.md#prerequisites).
→ [Backup secret details](reference/arangodb/backup.md#configure-backup-secrets)

```bash
just arangodb configure-backup-secrets --restic-password '<restic-pass>'
```

---

## 3. Install ArangoDB

### 3.1 Operator

Installs `kube-arangodb` Helm chart into `prod` app namespace.
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

---

## 4. Import Data

Sections 1–3 give you a running, **empty** cluster — no application databases. This section creates and fills them in one shot.

### 4.1 Bootstrap from Snapshot

Production first load is a cross-project restic bootstrap: the in-cluster restore Job (restic → arangorestore) reads a snapshot straight out of a GCS bucket owned by a **different GCP project**.
→ [Bootstrap details](reference/arangodb/bootstrap.md)

**In the SOURCE cluster's environment** — grant the read-only identity, then print the two values the destination consumes. `grant-source-bucket-reader` prints the source project id and key-file path; both feed `configure-source-secrets` in the next block.

```bash
just cluster-env --env <source-env> --cluster <source-cluster>
just arangodb grant-source-bucket-reader
```

Prints the SOURCE restic password — use as `--restic-password` for `configure-source-secrets`:

```bash
just arangodb source-secret-value --namespace <source-namespace>
```

Prints the imported application username — use as `--app-user` for `finalize-bootstrap` in [4.2](#42-finalize-import):

```bash
just arangodb source-app-user --namespace <source-namespace>
```

---

**Back in THIS cluster's environment** — everything below runs here. `list-source-snapshots` is the read-only companion of `bootstrap-from-snapshot`: run it first to pick an id; omitting `--snapshot` takes the newest.

```bash
just cluster-env --env prod --cluster <prod-cluster>
just arangodb configure-source-secrets \
  --restic-password '<SOURCE-restic-password>' \
  --gcs-project <source-project-id> \
  --gcs-key-file <path-to-source-key.json>
just arangodb list-source-snapshots --namespace prod --bucket <source-bucket>
just arangodb bootstrap-from-snapshot --namespace prod --bucket <source-bucket>
```

### 4.2 Finalize Import

One required setup after import: reset root, rotate imported app password to a new destination-only value, update Secret `backend`, and verify app database access (it runs `verify-app-credentials` itself). `--app-user` is the username `source-app-user` printed in [4.1](#41-bootstrap-from-snapshot).
→ [Post-import details](reference/arangodb/bootstrap.md#finalize-import)

```bash
just arangodb finalize-bootstrap --app-user '<existing-app-user>' --app-password '<new-destination-app-password>'
```

### 4.3 Alternative: Loaders

**Reserved for a future revision — skip it.** Loaders are the fallback when there is no restic snapshot to bootstrap from.
→ [Import details](reference/arangodb/import.md#current-state)

```bash
just arangodb deploy-loader --folder arangodb-dataloader
```

---

## 5. Backup & Restore

### 5.1 Deploy Backup

Creates GCS bucket, immediate Job, and daily CronJob. `list-snapshots-in-cluster` is the read-only check that the immediate Job actually wrote a snapshot.
→ [Backup details](reference/arangodb/backup.md)

```bash
just arangodb deploy-backup
just arangodb list-snapshots-in-cluster --namespace prod
```

### 5.2 Restore Drill

Run on **clone cluster only**. In-cluster Job: restic restore → arangorestore.
→ [Restore details](reference/arangodb/restore.md)

```bash
just arangodb configure-restore --namespace <target-namespace>
just arangodb apply-restore
```

### 5.3 Single Database

Restores one database instead of the whole instance: `restore-database` from this cluster's own bucket, `import-database` from another project's bucket. Both are fail-closed and clear the stack's single-database keys when they exit.
→ [Single-database restore details](reference/arangodb/restore-database.md)

```bash
just arangodb restore-database --namespace <target-namespace> --database <db>
just arangodb import-database --namespace <target-namespace> --bucket <source-bucket> --database <db>
```

---

## 6. Verify

Read-only audit of pool, operator, storage, members, Service and Jobs. Exits non-zero if any required check fails.
→ [Verify details](reference/arangodb/verify.md)

```bash
just arangodb verify
```

---

## 7. Teardown

**Destructive. Clone only.**
→ [Teardown details](reference/arangodb/teardown.md)

```bash
just arangodb teardown --namespace prod --delete-pvcs yes
```

---

## 8. Troubleshooting

→ [Full troubleshooting table](reference/arangodb/troubleshooting.md), including the [cross-project bootstrap failures](reference/arangodb/troubleshooting.md#cross-project-bootstrap-deploy-guide-4) from section 4.

---

## 9. Optional: Databases by Hand

**Not part of the normal flow — skip on a first install.** Run only when a logical database is missing or using the loader path. Post-import user-password rotation and Secret `backend` updates belong to [Finalize Import](reference/arangodb/bootstrap.md#finalize-import).
→ [Databases details](reference/arangodb/databases.md)

```bash
just arangodb create-databases --app-user '<user>' --app-password '<password>'
```

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
- [Verify details](reference/arangodb/verify.md)
- [Troubleshooting](reference/arangodb/troubleshooting.md)

**Other documentation:**
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Implementation plan (historical): [`plans/arangodb-production.md`](plans/arangodb-production.md)
