# Production Redis on the Stateful Database Pool

Provisioning guide for production **standalone Redis 8** on kOps `stateful-db`.

**Status**: Production procedure. Use `Pulumi.prod.yaml` configs only. Do not edit the lab `dev`/`experiments` stacks of `redis-standalone`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Pool Check](#1-pool-check)
- [2. Install Redis](#2-install-redis)
- [3. Teardown](#3-teardown)
- [4. Verify](#4-verify)
- [5. Troubleshooting](#5-troubleshooting)
- [6. Related Documents](#6-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Verify the stateful-db pool
just redis check-pool

# 2. Deploy standalone Redis 8 (single pod, AOF on, PVC on
#    dictycr-balanced, --requirepass from Secret redis-auth)
just redis deploy --password '<password>'

# 3. Verify installation
just redis verify
```

Optional steps:
```bash
# Teardown (clone only, destructive — deletes the data PVC = all Redis data)
just redis teardown --namespace prod --delete-pvc yes
```

---

**Everything below runs inside the cluster-env sub-shell.** `just cluster-env --env prod --cluster <prod-cluster>` sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID` and `KUBECONFIG` into that shell, so no recipe needs `--stack` — each one falls back to `$PULUMI_STACK`. Outside the sub-shell recipes fail with `Error: no stack name`. Leave it with `exit` or Ctrl-D.

## 1. Pool Check

Verify 3 Ready nodes labeled `pool=database` with the `dedicated=database:NoSchedule` taint. The pod carries a matching nodeSelector + toleration — it stays Pending without them.
→ [Pool requirements](reference/redis/pool-requirements.md)

```bash
just redis check-pool
```

---

## 2. Install Redis

Creates Secret `redis-auth` and a single-pod Redis **8.4.6** Deployment: AOF persistence on, TCP probes, 50Gi `dictycr-balanced` PVC, password auth from the Secret.
→ [Install details](reference/redis/install.md)

```bash
just redis deploy --password '<password>'
```

Address: `redis://:<password>@redis.prod.svc.cluster.local:6379` — password in Secret `redis-auth` (key `password`).

**Single instance by design — no failover.** If the pod or node dies, Redis is down until Kubernetes reschedules it (PVC reattach, typically minutes); data survives via the AOF on the PVC. There is no replica to promote, and **no data import path** — applications seed their own keys on first use.

---

## 3. Teardown

**Destructive. Clone only.** Deletes the data PVC — all Redis data.
→ [Teardown details](reference/redis/teardown.md)

```bash
just redis teardown --namespace prod --delete-pvc yes
```

---

## 4. Verify

Read-only checks plus one authenticated `PING`: pool, pod, PVC, Service on 6379, Secret. Exits non-zero if any required check fails.

```bash
just redis verify
```

---

## 5. Troubleshooting

→ [Full troubleshooting table](reference/redis/troubleshooting.md)

Common issues:
- **No stack name error**: Enter `just cluster-env` first, or pass `--stack <name>`
- **Pod Pending (taint)**: Node pool missing or `placement.pool` mismatch — see [pool requirements](reference/redis/pool-requirements.md)
- **Client `NOAUTH` errors**: Password not from Secret `redis-auth` — see [install details](reference/redis/install.md#service-and-credentials)

---

## 6. Related Documents

**Reference details for this guide:**
- [Pool requirements](reference/redis/pool-requirements.md)
- [Install details](reference/redis/install.md)
- [Teardown details](reference/redis/teardown.md)
- [Troubleshooting](reference/redis/troubleshooting.md)

**Other documentation:**
- PostgreSQL on the same pool: [`postgres-deploy.md`](postgres-deploy.md)
- MinIO on the same pool: [`minio-deploy.md`](minio-deploy.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
