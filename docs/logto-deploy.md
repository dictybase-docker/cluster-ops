# Production Logto with PostgreSQL

Production procedure for deploying Logto on this repository's kOps-managed Kubernetes cluster. The current Pulumi program has lab stack examples but no production Logto stack; do not run its apply step until production config exists.

## Table of Contents

- [Table of Contents](#table-of-contents)
- [Quick Reference](#quick-reference)
- [1. Prepare the Cluster](#1-prepare-the-cluster)
- [2. Deploy PostgreSQL](#2-deploy-postgresql)
- [3. Pin Logto Version](#3-pin-logto-version)
- [4. Link Logto to PostgreSQL](#4-link-logto-to-postgresql)
- [5. Deploy Logto](#5-deploy-logto)
- [6. Verify](#6-verify)
- [7. Troubleshooting](#7-troubleshooting)
- [8. Related Documents](#8-related-documents)

## Quick Reference

For experienced operators. Complete PostgreSQL details are in the linked guide. Logto apply remains blocked until production `log-to` stack configuration is added.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Prepare and deploy PostgreSQL
just postgres check-pool
just postgres deploy-operator
just postgres configure-backup
just postgres deploy-cluster --app-password '<app-password>'

# 2. Verify PostgreSQL
just postgres verify

# 3. After adding production log-to stack config, preview and apply Logto
pulumi -C log-to preview
pulumi -C log-to up

# 4. Verify Logto
kubectl -n prod rollout status deployment/logto
```

---

**Run commands inside the `cluster-env` sub-shell.** Recipes use `$PULUMI_STACK`, `$KUBECONFIG`, and production GCP credentials exported by that shell.

## 1. Prepare the Cluster

Confirm the production cluster environment, `stateful-db` pool, StorageClasses, and shared namespaces before installing either database or application workloads.

→ [Cluster and Pulumi setup](pulumi-setup.md) · [Pool requirements](reference/postgres/pool-requirements.md)

```bash
just cluster-env --env prod --cluster <prod-cluster>
```

## 2. Deploy PostgreSQL

Create the PostgreSQL 16 CloudNativePG cluster before Logto. The recipe creates database `logto`, role `logto`, Secret `logto-app`, and Service `logto-rw` in namespace `prod`.

→ [Production PostgreSQL guide](postgres-deploy.md) · [Cluster details](reference/postgres/cluster.md)

```bash
just postgres deploy-cluster --app-password '<app-password>'
```

## 3. Pin Logto Version

Use immutable image tag `1.43.0`, the latest release shown by the official Logto repository during this update. Do not use mutable `latest` in production.

→ [Logto version and configuration details](reference/logto/deployment.md#version-and-image)

```bash
rg -n "tag: 1\.43\.0" log-to/Pulumi.<production-stack>.yaml
```

## 4. Link Logto to PostgreSQL

Set `namespace: prod` and `databaseSecret: logto-app` in production Logto config. The current program builds `DB_URL` from Kubernetes service discovery and reads username/password from Secret `logto-app`.

→ [PostgreSQL connection wiring](reference/logto/deployment.md#postgresql-connection)

```bash
kubectl -n prod get secret logto-app service/logto-rw
```

## 5. Deploy Logto

**Production config is not present yet.** Add the production stack file from the reference configuration, then preview for unexpected resource changes before applying. The current Pulumi program creates Logto API and admin Services plus an API Ingress; it does not create a production config automatically.

→ [Production configuration](reference/logto/deployment.md#production-configuration)

```bash
pulumi -C log-to preview
```

After preview review, apply the stack:

```bash
pulumi -C log-to up
```

## 6. Verify

Wait for the deployment and confirm API Ingress, Services, and application logs. A successful rollout does not replace checking database migration output.

→ [Verification checks](reference/logto/deployment.md#verification)

```bash
kubectl -n prod rollout status deployment/logto
```

## 7. Troubleshooting

→ [Logto troubleshooting](reference/logto/deployment.md#troubleshooting) · [PostgreSQL troubleshooting](reference/postgres/troubleshooting.md)

## 8. Related Documents

- [PostgreSQL deployment](postgres-deploy.md)
- [PostgreSQL cluster details](reference/postgres/cluster.md)
- [Pulumi setup](pulumi-setup.md)
- [Official Logto OSS deployment documentation](https://docs.logto.io/logto-oss/deploy)
- [Official Logto v1.43.0 release](https://github.com/logto-io/logto/releases/tag/v1.43.0)
