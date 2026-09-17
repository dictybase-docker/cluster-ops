# Production PostgreSQL on the Stateful Database Pool

Provisioning guide for production PostgreSQL **16** on kOps `stateful-db`, via the CloudNativePG operator.

**Status**: Production procedure. Use `Pulumi.dcr-kube1.yaml` configs only. Do not edit the lab `dev`/`experiments` stacks of `cloudnative-pg-operator` / `cloudnative-pg-cluster`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Pool Check](#1-pool-check)
- [2. Operator](#2-operator)
- [3. Backup Wiring](#3-backup-wiring)
- [4. Import Source (Optional)](#4-import-source-optional)
- [5. Create the Cluster](#5-create-the-cluster)
- [6. Re-import Into an Existing Cluster](#6-re-import-into-an-existing-cluster)
- [7. Backup & Restore](#7-backup--restore)
- [8. Teardown](#8-teardown)
- [9. Verify](#9-verify)
- [10. Troubleshooting](#10-troubleshooting)
- [11. Related Documents](#11-related-documents)

---

## Quick Reference

| Case | Situation | Steps | Outcome |
|------|-----------|-------|---------|
| **1.** | Fresh deploy, no import | 1 → 2 → 3 → 5 → 9 | Database **empty** (initdb) |
| **2.** | Fresh deploy, import from a source cluster | 1 → 2 → 3 → **4** → 5 → 9 | Database **holds the source cluster's data** (recovery) |
| **3.** | Cluster already deployed, want the source cluster's data | 4 (if not on the stack, or to change it) → **6** | Source data **replaces the current data** |

Case 1 does not turn into case 2 by re-running `deploy-cluster` — data moves only at first-instance creation. Case 3 is the only way in afterwards. Step 4 is picked by the case, never by cluster or database state — see the [§4 decision diagram](#4-import-source-optional).

```bash
# Enter the cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Verify the stateful-db pool
just postgres check-pool

# 2. Deploy the CloudNativePG operator
just postgres deploy-operator

# 3. Backup identity, stack config, and Barman Cloud plugin
just postgres configure-backup

# 4. IMPORT ONLY — register where to import from; skip only if you want an empty database
just postgres configure-source --source-cluster <cluster-name>

# 5. Create the PostgreSQL 16 cluster — imports when step 4 ran, else empty
just postgres deploy-cluster --app-password '<app-password>'

# 9. Verify the installation
just postgres verify
```

Optional steps:
```bash
# Point-in-time import — replaces step 4, still before step 5
just postgres configure-source --source-cluster <cluster-name> --target-time '<rfc3339>'

# Re-import into a cluster that already exists (section 6) — destroys current data
just postgres reset-cluster --reset-data yes --app-password '<app-password>'

# Teardown (clone only) — deletes the data PVCs and every backup in the bucket
just postgres teardown --namespace prod --delete-pvcs yes --delete-backups yes
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env prod --cluster <prod-cluster>` → [cluster env](reference/pulumi/cluster-env.md#activating-the-shell).
- Recipes default `--stack` to `$PULUMI_STACK`, so no command below needs the flag.

## 1. Pool Check

Verifies the `stateful-db` pool: 3 Ready nodes labeled `pool=database`, carrying the `dedicated=database:NoSchedule` taint.
→ [Pool requirements](reference/postgres/pool-requirements.md)

```bash
just postgres check-pool
```

---

## 2. Operator

Installs the CloudNativePG operator into the `operators` namespace. Must run before §3 — the Barman Cloud plugin needs a running operator.
→ [Operator details](reference/postgres/operator.md)

```bash
just postgres deploy-operator
```

---

## 3. Backup Wiring

Creates the `postgres-backup-sa` identity and key, records the backup bucket and key path on the cluster stack, and deploys the Barman Cloud plugin.
→ [Backup details](reference/postgres/backup.md) · [Plugin details](reference/postgres/backup-plugin.md)

```bash
just postgres configure-backup
```

---

## 4. Import Source (Optional)

Registers another cluster's CloudNativePG backup as this stack's recovery source and mints the source reader key. Copies nothing, never touches a running Cluster — the data moves later, when the first instance is created.
**Gated on the case, not on state** — runnable whether or not the Cluster exists. Skip it in case 1, run it before §5 in case 2, before §6 in case 3.

```text
  case 1  fresh deploy, no import          --> skip 4 --> 5 deploy-cluster (initdb)   --> empty database; to fill it, go to case 3

  case 2  fresh deploy, source import      --> 4      --> 5 deploy-cluster (recovery) --> database holds the source cluster's data

  case 3  cluster exists, want source data --> 4 (*)  --> 6 reset-cluster (recovery)  --> source data replaces the current data

  (*) set it once, re-run 4 to change the source — 6 aborts only when none is set
```

→ [Import details](reference/postgres/import.md) · [flags](reference/postgres/import.md#flags)

```bash
just postgres configure-source --source-cluster <cluster-name>
```

---

## 5. Create the Cluster

Creates the Secrets, backup bucket, `ObjectStore`, PostgreSQL **16** Cluster, and daily ScheduledBackup. With §4 configured the first instance replays the source backup; without it, initdb creates an empty `logto` database.
→ [Cluster details](reference/postgres/cluster.md)

```bash
just postgres deploy-cluster --app-password '<app-password>'
```

---

## 6. Re-import Into an Existing Cluster

**Destructive.** Deletes the Cluster CR and its data PVCs, then re-bootstraps from the recovery source registered in §4.
→ [Reset details](reference/postgres/import.md#reset-re-import-into-a-running-cluster)

```bash
just postgres reset-cluster --reset-data yes --app-password '<app-password>'
```

---

## 7. Backup & Restore

WAL archiving and the daily base backup are created by §5 and run themselves — nothing to deploy. In-place restore of this cluster's own backups has no recipe yet; re-importing from a source cluster is §6.
→ [Backup details](reference/postgres/backup.md) · [restore](reference/postgres/backup.md#restore)

```bash
kubectl get scheduledbackups.postgresql.cnpg.io -n prod
```

---

## 8. Teardown

**Destructive. Clone only.** Destroys the cluster stack: the data PVCs and every backup in the bucket. To replace only the data, use §6.
→ [Teardown details](reference/postgres/teardown.md)

```bash
just postgres teardown --namespace prod --delete-pvcs yes --delete-backups yes
```

---

## 9. Verify

Read-only check of pool, operator, storage, Cluster phase, pods, PVCs, Services, and ScheduledBackup. Exits non-zero on any failure.
→ [Verify details](reference/postgres/verify.md)

```bash
just postgres verify
```

---

## 10. Troubleshooting

→ [Full troubleshooting table](reference/postgres/troubleshooting.md)

For day-to-day cluster ops (status, `psql`, on-demand backup, restart, diagnostics), use the [`kubectl cnpg` plugin](reference/postgres/troubleshooting.md#kubectl-cnpg-plugin).

---

## 11. Related Documents

**Reference details for this guide:**
- [Pool requirements](reference/postgres/pool-requirements.md)
- [Operator details](reference/postgres/operator.md)
- [Cluster details](reference/postgres/cluster.md)
- [Import details](reference/postgres/import.md)
- [Backup details](reference/postgres/backup.md)
- [Barman Cloud plugin](reference/postgres/backup-plugin.md)
- [Teardown details](reference/postgres/teardown.md)
- [Verify details](reference/postgres/verify.md)
- [Troubleshooting](reference/postgres/troubleshooting.md)

**Other documentation:**
- ArangoDB on the same pool: [`arangodb-deploy.md`](arangodb-deploy.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
