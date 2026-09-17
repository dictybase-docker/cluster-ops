# Production Logto on the Production Cluster

Provisioning guide for production **Logto** (`svhd/logto`), backed by the PostgreSQL cluster created in [`postgres-deploy.md`](postgres-deploy.md).

**Status**: Production procedure. The `log-to` project ships only the lab `dev`/`experiments` stacks — **no production `log-to` stack file exists in this repository yet**. Sections 1–4 are read-only and safe today; section 5 cannot run until `log-to/Pulumi.<prod-cluster>.yaml` is written and reviewed. Never apply a lab stack against production.

## Table of Contents

- [Table of Contents](#table-of-contents)
- [Quick Reference](#quick-reference)
- [1. Verify PostgreSQL](#1-verify-postgresql)
- [2. Add Production Config](#2-add-production-config)
- [3. Pin Logto Version](#3-pin-logto-version)
- [4. Link Logto to PostgreSQL](#4-link-logto-to-postgresql)
- [5. Deploy Logto](#5-deploy-logto)
- [6. Verify](#6-verify)
- [7. Troubleshooting](#7-troubleshooting)
- [8. Related Documents](#8-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below. PostgreSQL is a **prerequisite** — install it with [`postgres-deploy.md`](postgres-deploy.md), never from this guide.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Verify the PostgreSQL prerequisite (Cluster logto in namespace prod)
just postgres verify

# 2. Select the production log-to stack — fails until
#    log-to/Pulumi.<prod-cluster>.yaml exists
just gcp-pulumi ensure-stack --folder log-to

# 3. Confirm the pinned image tag (never latest)
yq '.config."log-to:properties".image' "log-to/Pulumi.${PULUMI_STACK}.yaml"

# 4. Confirm the database Secret and write Service Logto binds to
kubectl -n prod get secret logto-app service/logto-rw

# 5. Preview, then apply — create-resource runs `pulumi up -f -y`, no prompt
just gcp-pulumi preview --folder log-to
just gcp-pulumi create-resource --folder log-to

# 6. Verify the rollout
kubectl -n prod rollout status deployment/logto
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env prod --cluster <prod-cluster>`. The sub-shell sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Recipes default `--stack` to `$PULUMI_STACK`, so no recipe below needs the flag.
- Outside the sub-shell recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Verify PostgreSQL

PostgreSQL is installed by its own guide, not here. Logto needs the CloudNativePG Cluster `logto` already healthy in namespace `prod`, with Secret `logto-app` and Service `logto-rw`; `verify` exits non-zero if the pool, operator, Cluster, pods, PVC, or Services are not in place.
→ [PostgreSQL deployment guide](postgres-deploy.md) · [Prerequisites](reference/logto/deployment.md#prerequisites)

```bash
just postgres verify
```

---

## 2. Add Production Config

**Production config is not in the repository.** Write `log-to/Pulumi.<prod-cluster>.yaml` from the reference template first — `ensure-stack` refuses to initialize a stack with no matching config file, because an empty stack fails at preview with `missing required configuration variable`.
→ [Production configuration](reference/logto/deployment.md#production-configuration) · [Stack config recipes](reference/pulumi/stack-config.md)

```bash
just gcp-pulumi ensure-stack --folder log-to
```

---

## 3. Pin Logto Version

Pin an immutable tag — the container runs `npm run cli db alteration deploy <tag>` with the configured tag, so the tag is the migration target as well as the image, and `latest` makes both unreproducible. Reference pin: **1.43.0**.
→ [Version and image](reference/logto/deployment.md#version-and-image)

```bash
yq '.config."log-to:properties".image' "log-to/Pulumi.${PULUMI_STACK}.yaml"
```

---

## 4. Link Logto to PostgreSQL

Logto reads `username`/`password` from Secret `logto-app` and builds `DB_URL` from the `LOGTO_RW_SERVICE_HOST`/`_PORT` variables Kubernetes injects for Service `logto-rw`. Both workloads must live in namespace `prod`, and the stack must set `databaseSecret: logto-app`.
→ [PostgreSQL connection](reference/logto/deployment.md#postgresql-connection)

```bash
kubectl -n prod get secret logto-app service/logto-rw
```

---

## 5. Deploy Logto

Creates PVC `logto-claim`, Deployment `logto`, Services `logto-api` (3001) and `logto-admin` (3002), and Ingress `logto-ingress` routed to `logto-api` only — the admin Service stays internal. Preview and read every resource change first: `create-resource` runs `pulumi up -f -y` and does not prompt.
→ [Deployment](reference/logto/deployment.md#deployment)

```bash
just gcp-pulumi preview --folder log-to
just gcp-pulumi create-resource --folder log-to
```

---

## 6. Verify

There is no `just logto verify` recipe. Wait for the rollout, then read the pod logs — a healthy rollout does not prove the seed and alteration steps finished, and a failed migration surfaces only in the logs.
→ [Verification checks](reference/logto/deployment.md#verification)

```bash
kubectl -n prod rollout status deployment/logto
```

---

## 7. Troubleshooting

→ [Full troubleshooting table](reference/logto/deployment.md#troubleshooting) · [PostgreSQL troubleshooting](reference/postgres/troubleshooting.md)

Common issues:
- **No stack name error**: Enter `just cluster-env` first, or pass `--stack <name>`
- **`ensure-stack` refuses to init**: `log-to/Pulumi.<prod-cluster>.yaml` missing — see [production configuration](reference/logto/deployment.md#production-configuration)
- **`secret "logto-app" not found`**: PostgreSQL not deployed in `prod`, or `databaseSecret` points elsewhere — run `just postgres verify`
- **Pod fails during database alteration**: Migration or database reachability, not the rollout — read `kubectl -n prod logs deployment/logto`

---

## 8. Related Documents

**Reference details for this guide:**
- [Logto deployment details](reference/logto/deployment.md)
- [Stack config recipes](reference/pulumi/stack-config.md)

**Other documentation:**
- PostgreSQL prerequisite: [`postgres-deploy.md`](postgres-deploy.md)
- PostgreSQL cluster details: [`reference/postgres/cluster.md`](reference/postgres/cluster.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- [Official Logto OSS deployment documentation](https://docs.logto.io/logto-oss/deploy)
