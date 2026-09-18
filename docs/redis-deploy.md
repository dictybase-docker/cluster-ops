# Production Redis on the Stateful Database Pool

Provisioning guide for production **standalone Redis 8** on kOps `stateful-db`.

**Status**: Production procedure. Use `Pulumi.dcr-kube1.yaml` configs only. Do not edit the lab `dev`/`experiments` stacks of `redis-standalone`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Pool Check](#1-pool-check)
- [2. Install Redis](#2-install-redis)
- [3. Backup & Restore](#3-backup--restore)
- [4. Teardown](#4-teardown)
- [5. Verify](#5-verify)
- [6. Troubleshooting](#6-troubleshooting)
- [7. Related Documents](#7-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Verify the stateful-db pool
just redis check-pool

# 2. Deploy standalone Redis 8 (single pod, AOF on, PVC on
#    dictycr-balanced, unauthenticated)
just redis deploy

# 3. Backup wiring — creates the redis-backup-sa identity + key, stores the
#    restic repository password and GCS wiring in Secret redis-backup-auth,
#    then deploys the backup bucket + CronJob (daily 1AM) and runs the
#    first backup immediately
just redis configure-backup-secrets --restic-password '<restic-pass>'
just redis deploy-backup

# 4. Verify installation
just redis verify
```

Optional steps:
```bash
# Teardown (clone only, destructive — deletes the data PVC = all Redis data)
just redis teardown --namespace prod --delete-pvc yes
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env prod --cluster <prod-cluster>`. The sub-shell sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Recipes default `--stack` to `$PULUMI_STACK`, so no recipe below needs the flag.
- Outside the sub-shell recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Pool Check

Verify 3 Ready nodes labeled `pool=database` with the `dedicated=database:NoSchedule` taint. The pod carries a matching nodeSelector + toleration — it stays Pending without them.
→ [Pool requirements](reference/redis/pool-requirements.md)

```bash
just redis check-pool
```

---

## 2. Install Redis

Single-pod Redis **8.4.6** Deployment: AOF persistence on, TCP probes, 50Gi `dictycr-balanced` PVC, unauthenticated — any in-cluster client can connect. Single instance by design — no failover, no replica, no data import path.
→ [Install details](reference/redis/install.md) · [address](reference/redis/install.md#service)

```bash
just redis deploy
```

---

## 3. Backup & Restore

Daily restic backup to `gs://restic-redis-backup-<project-id>` through the redis-scoped `redis-backup-sa` identity, plus an immediate first run. Run **after** `deploy` — the backup jobs read the live `redis` service.
→ [Backup details](reference/redis/backup.md)

```bash
just redis configure-backup-secrets --restic-password '<restic-pass>'
just redis deploy-backup
```

---

## 4. Teardown

**Destructive. Clone only.** Deletes the data PVC — all Redis data.
→ [Teardown details](reference/redis/teardown.md)

```bash
just redis teardown --namespace prod --delete-pvc yes
```

---

## 5. Verify

Read-only checks plus one `PING` handshake. Exits non-zero if any check fails.
→ [Verify details](reference/redis/verify.md)

```bash
just redis verify
```

---

## 6. Troubleshooting

→ [Full troubleshooting table](reference/redis/troubleshooting.md)

---

## 7. Related Documents

**Reference details for this guide:**
- [Pool requirements](reference/redis/pool-requirements.md)
- [Install details](reference/redis/install.md)
- [Backup details](reference/redis/backup.md)
- [Teardown details](reference/redis/teardown.md)
- [Verify details](reference/redis/verify.md)
- [Troubleshooting](reference/redis/troubleshooting.md)

**Other documentation:**
- PostgreSQL on the same pool: [`postgres-deploy.md`](postgres-deploy.md)
- MinIO on the same pool: [`minio-deploy.md`](minio-deploy.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
