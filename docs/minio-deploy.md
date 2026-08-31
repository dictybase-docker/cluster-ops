# Production MinIO on the Stateful Database Pool

Provisioning guide for production **standalone MinIO** (S3 API, Bitnami chart 17) on kOps `stateful-db`.

**Status**: Production procedure. Use `Pulumi.prod.yaml` configs only. Do not edit the lab `dev`/`experiments`/`local` stacks of `minio`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Pool Check](#1-pool-check)
- [2. Install MinIO](#2-install-minio)
- [3. Import Data](#3-import-data)
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
just minio check-pool

# 2. Deploy standalone MinIO (single pod, PVC on dictycr-balanced)
just minio deploy --root-user '<user>' --root-password '<password>'

# 3. Import a bucket from a source MinIO/S3 endpoint (repeatable; one
#    bucket per run)
just minio import-bucket \
  --bucket <bucket> \
  --source-url <https://source-endpoint:9000> \
  --source-user '<source-access-key>' \
  --source-password '<source-secret-key>'

# 4. Verify installation
just minio verify
```

Optional steps:
```bash
# Teardown (clone only, destructive — deletes the data PVC = all objects)
just minio teardown --namespace prod --delete-pvc yes
```

---

**Everything below runs inside the cluster-env sub-shell.** `just cluster-env --env prod --cluster <prod-cluster>` sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID` and `KUBECONFIG` into that shell, so no recipe needs `--stack` — each one falls back to `$PULUMI_STACK`. Outside the sub-shell recipes fail with `Error: no stack name`. Leave it with `exit` or Ctrl-D.

## 1. Pool Check

Verify 3 Ready nodes labeled `pool=database` with the `dedicated=database:NoSchedule` taint. The pod carries a matching nodeSelector + toleration — it stays Pending without them.
→ [Pool requirements](reference/minio/pool-requirements.md)

```bash
just minio check-pool
```

---

## 2. Install MinIO

Creates the root-credentials Secret `minio-root` and installs the Bitnami MinIO chart **17.0.23** (image `bitnamilegacy/minio:2025.7.23-debian-12-r3`) in standalone mode with a 150Gi `dictycr-balanced` PVC.
→ [Install details](reference/minio/install.md)

```bash
just minio deploy --root-user '<user>' --root-password '<password>'
```

S3 API address: `http://minio.prod.svc.cluster.local:9000` — credentials in Secret `minio-root` (keys `rootUser`/`rootPassword`).

**Standalone means no node-level HA.** One pod, one PVC — if the pod or node dies, MinIO is down until Kubernetes reschedules it (PVC reattach, typically minutes). There is no erasure-code replication across nodes. Bucket-level copies of critical data live outside this cluster (the source you imported from, or another `import-bucket` run reversed).

---

## 3. Import Data

Sections 1–2 give a running, **empty** MinIO. This section mirrors one bucket at a time from a source MinIO/S3 endpoint (another cluster, cloud storage, anywhere S3-compatible) using `mc mirror`.
→ [Import details](reference/minio/import.md)

```bash
just minio import-bucket \
  --bucket <bucket> \
  --source-url <https://source-endpoint:9000> \
  --source-user '<source-access-key>' \
  --source-password '<source-secret-key>'
```

Re-runnable — `mc mirror --preserve --overwrite` copies only what changed. `--remove` (delete target objects missing at the source) exists but is **off by default**.

---

## 4. Teardown

**Destructive. Clone only.** Deletes the data PVC — every object in every bucket.
→ [Teardown details](reference/minio/teardown.md)

```bash
just minio teardown --namespace prod --delete-pvc yes
```

---

## 5. Verify

Read-only checks: pool, MinIO pod, PVC, Service on 9000, Secret. Exits non-zero if any required check fails.

```bash
just minio verify
```

---

## 6. Troubleshooting

→ [Full troubleshooting table](reference/minio/troubleshooting.md)

Common issues:
- **No stack name error**: Enter `just cluster-env` first, or pass `--stack <name>`
- **Pod Pending (taint)**: Node pool missing or `placement.pool` mismatch — see [pool requirements](reference/minio/pool-requirements.md)
- **Import stalls**: Port-forward to `svc/minio` not established, or source credentials rejected — see [import details](reference/minio/import.md)

---

## 7. Related Documents

**Reference details for this guide:**
- [Pool requirements](reference/minio/pool-requirements.md)
- [Install details](reference/minio/install.md)
- [Import details](reference/minio/import.md)
- [Teardown details](reference/minio/teardown.md)
- [Troubleshooting](reference/minio/troubleshooting.md)

**Other documentation:**
- PostgreSQL on the same pool: [`postgres-deploy.md`](postgres-deploy.md)
- ArangoDB on the same pool: [`arangodb-deploy.md`](arangodb-deploy.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
