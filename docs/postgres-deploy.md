# Production PostgreSQL on the Stateful Database Pool

Provisioning guide for production PostgreSQL **16** on kOps `stateful-db`, via the CloudNativePG operator.

**Status**: Production procedure. Use `Pulumi.prod.yaml` configs only. Do not edit the lab `dev`/`experiments` stacks of `cloudnative-pg-operator` / `cloudnative-pg-cluster`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Pool Check](#1-pool-check)
- [2. Prerequisites](#2-prerequisites)
- [3. Install PostgreSQL](#3-install-postgresql)
  - [3.1 Operator](#31-operator)
  - [3.2 Cluster](#32-cluster)
- [4. Import Data](#4-import-data)
- [5. Backup & Restore](#5-backup--restore)
  - [5.1 Backup](#51-backup)
  - [5.2 Restore](#52-restore)
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

# 1. Verify the stateful-db pool
just postgres check-pool

# 2. Backup wiring — creates the postgres-backup-sa service account + key,
#    ensures namespaces prod/operators, and stores the backup bucket +
#    key path on the cluster stack. Project/bucket default from the
#    cluster env: $PROJECT_ID, bucket cloudnative-pg-backup-$PROJECT_ID,
#    key credentials/$PROJECT_ID/postgres-backup-sa.json
just postgres configure-backup

# 3. Deploy the CloudNativePG operator
just postgres deploy-operator

# 4. First load — import from another cluster's CloudNativePG backup.
#    Creates a reader SA in the SOURCE project, grants it read on the
#    source bucket, and stores the recovery source on the stack.
just postgres configure-source \
  --source-cluster <source-cluster-name> \
  --bucket <source-bucket> \
  --source-project <source-project-id>

# 5. Deploy the PostgreSQL 16 cluster (single instance on the database
#    pool, GCS backup bucket + daily ScheduledBackup included).
#    With the recovery source set, the first bootstraps from the source
#    backup instead of an empty database.
just postgres deploy-cluster --app-password '<app-password>'

# 6. Verify installation
just postgres verify
```

Optional steps:
```bash
# Teardown (clone only, destructive — deletes data PVCs AND every backup
# in the bucket)
just postgres teardown --namespace prod --delete-pvcs yes --delete-backups yes
```

---

**Everything below runs inside the cluster-env sub-shell.** `just cluster-env --env prod --cluster <prod-cluster>` sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID` and `KUBECONFIG` into that shell, so no recipe needs `--stack` — each one falls back to `$PULUMI_STACK`. Outside the sub-shell recipes fail with `Error: no stack name`. Leave it with `exit` or Ctrl-D.

## 1. Pool Check

Verify 3 Ready nodes labeled `pool=database` with the `dedicated=database:NoSchedule` taint. The Cluster CR carries a matching nodeSelector + toleration — pods stay Pending without them.
→ [Pool requirements](reference/postgres/pool-requirements.md)

```bash
just postgres check-pool
```

Prints PASS/FAIL for: node count, taint, Ready status. Shows zone spread.

---

## 2. Prerequisites

Creates the `postgres-backup-sa` service account + JSON key, grants it object-admin on the backup bucket only, ensures namespaces `prod`/`operators`, and stores the backup wiring on the cluster stack. This is a **different service account** from ArangoDB's `backup-gcs-sa` — its IAM condition is pinned to the CloudNativePG bucket, and the two are not interchangeable.
→ [Backup details](reference/postgres/backup.md)

```bash
just postgres configure-backup
```

Also required: CSI + StorageClasses from [`pulumi-setup.md` §6](pulumi-setup.md#6-first-apply--storageclass).

---

## 3. Install PostgreSQL

### 3.1 Operator

Installs the `cloudnative-pg` Helm chart (operator **1.30.0**, the floor for Kubernetes 1.36) into the `operators` namespace.
→ [Operator details](reference/postgres/operator.md)

```bash
just postgres deploy-operator
```

### 3.2 Cluster

Creates the app-user Secret, backup-credentials Secret, GCS backup bucket, single-instance PostgreSQL **16** Cluster, and daily ScheduledBackup.
→ [Cluster details](reference/postgres/cluster.md)

```bash
just postgres deploy-cluster --app-password '<app-password>'
```

Single instance by design — **no replicas means no failover**: if the pod or node dies, PostgreSQL is down until Kubernetes reschedules it (PVC reattach, typically minutes). Recovery beyond that is the daily base backup + WAL archive. Bump `instances` in `cloudnative-pg-cluster/Pulumi.prod.yaml` when HA becomes a requirement.

Application address: `postgres://<owner>@logto-rw.prod.svc.cluster.local:5432/<database>` — password from Secret `logto-app`. With one instance, `-rw`/`-ro`/`-r` all resolve to the same pod.

**First load**: to import from another cluster's backup instead of starting empty, run `configure-source` (next section) **before** `deploy-cluster`. Bootstrap is one-shot — the Cluster's first creation decides empty vs imported.

---

## 4. Import Data

Sections 1–3 give a running, **empty** cluster. This section fills it from a CloudNativePG backup living in a **different GCP project** — the source cluster's barman object store, written by the same operator version. The new Cluster bootstraps with `bootstrap.recovery` against that store; no dump/load, no extra tooling.
→ [Import details](reference/postgres/import.md)

**Destructive to run twice.** The import is the Cluster's bootstrap: once the first instance exists, re-running `deploy-cluster` does not re-import. To re-import, [teardown](#6-teardown) first.

Run the reader-SA setup in the SOURCE cluster's environment — it creates `postgres-source-reader` in the source project and grants it read on the source bucket:

```bash
just postgres configure-source \
  --source-cluster <source-cluster-name> \
  --bucket <source-bucket> \
  --source-project <source-project-id>
```

---

**Back in THIS cluster's environment** — the deploy reads the stored recovery source:

```bash
just postgres deploy-cluster --app-password '<app-password>'
```

`--source-cluster` must be the **source cluster's CNPG `Cluster` name** — CloudNativePG stores backups under a folder named after it, and the recovery source refers to that folder. Add `--target-time '<rfc3339>'` for point-in-time instead of latest.

---

## 5. Backup & Restore

### 5.1 Backup

Continuous WAL archiving plus a daily base backup to `gs://cloudnative-pg-backup-<project-id>` (barman object store), created by `deploy-cluster` — nothing extra to run.
→ [Backup details](reference/postgres/backup.md)

```bash
kubectl get scheduledbackups.postgresql.cnpg.io -n prod
```

### 5.2 Restore

Reserved for a future revision. No in-cluster restore recipe exists yet — recovery is the import path above (new cluster) or a manual `bootstrap.recovery` edit, not covered further here.

---

## 6. Teardown

**Destructive. Clone only.** Deletes the data PVCs and, because the backup bucket lives in the same stack with `ForceDestroy: true`, **every backup in the bucket**.
→ [Teardown details](reference/postgres/teardown.md)

```bash
just postgres teardown --namespace prod --delete-pvcs yes --delete-backups yes
```

---

## 7. Verify

Read-only checks: pool, operator, StorageClasses, Cluster CR phase, pods, PVCs, Services, ScheduledBackup. Exits non-zero if any required check fails.

```bash
just postgres verify
```

Checks: 3 `pool=database` nodes with taint, Running operator, Cluster phase `Cluster in healthy state`, 1 ready instance pod, 1 Bound PVC, Services `logto-rw`/`logto-ro`/`logto-r` on 5432.

---

## 8. Troubleshooting

→ [Full troubleshooting table](reference/postgres/troubleshooting.md)

Common issues:
- **No stack name error**: Enter `just cluster-env` first, or pass `--stack <name>`
- **Pod Pending (taint)**: Node pool missing or `placement.pool` mismatch — see [pool requirements](reference/postgres/pool-requirements.md)
- **Backup failing**: `postgres-backup-sa` key missing or IAM condition pinned to a different bucket — re-run `configure-backup`
- **Import finds no backup**: `--source-cluster` doesn't match the backup folder name in the source bucket — see [import details](reference/postgres/import.md)

---

## 9. Related Documents

**Reference details for this guide:**
- [Pool requirements](reference/postgres/pool-requirements.md)
- [Operator details](reference/postgres/operator.md)
- [Cluster details](reference/postgres/cluster.md)
- [Import details](reference/postgres/import.md)
- [Backup details](reference/postgres/backup.md)
- [Teardown details](reference/postgres/teardown.md)
- [Troubleshooting](reference/postgres/troubleshooting.md)

**Other documentation:**
- ArangoDB on the same pool: [`arangodb-deploy.md`](arangodb-deploy.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
