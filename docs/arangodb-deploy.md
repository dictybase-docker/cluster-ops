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
  - [3.3 Databases](#33-databases)
- [4. Import Data (Optional)](#4-import-data-optional)
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

# 4. Create databases
just arangodb create-databases --app-user '<user>' --app-password '<password>'

# 5. Deploy backup (immediate job + cronjob)
just arangodb deploy-backup

# 6. Verify installation
just arangodb verify
```

Optional steps:
```bash
# Import data (requires Pulumi.prod.yaml for loader — doesn't exist yet)
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

### 3.3 Databases

Creates application user, Secret `backend`, and 7 logical databases.
→ [Databases details](reference/arangodb/databases.md)

```bash
just arangodb create-databases --app-user '<user>' --app-password '<password>'
```

---

## 4. Import Data (Optional)

No loader has `Pulumi.prod.yaml` yet — recipe refuses without it.
→ [Import details](reference/arangodb/import.md)

```bash
just arangodb deploy-loader --folder arangodb-dataloader
just arangodb deploy-loader --folder load-content-from-s3
just arangodb deploy-loader --folder load-uniprot-mapping
```

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
- Cluster bootstrap: [`README.md`](../README.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Implementation plan (historical): [`plans/arangodb-production.md`](plans/arangodb-production.md)
