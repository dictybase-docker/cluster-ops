# Production Logto on the Production Cluster

Provisioning guide for production **Logto** (`svhd/logto`), backed by the PostgreSQL cluster created in [`postgres-deploy.md`](postgres-deploy.md).

**Status**: Production procedure. Once the production stack config exists, `just logto install` is the entire installation — it folds check → `ensure-stack` → preview → apply → rollout wait → verify into one run. The `log-to` project ships only the lab `dev`/`experiments` stacks: **no production `log-to/Pulumi.<prod-cluster>.yaml` exists in this repository yet**, so `just logto install` stops at `Error: stack config missing` until that file is written and reviewed. Never apply a lab stack against production.

## Table of Contents

- [Table of Contents](#table-of-contents)
- [Quick Reference](#quick-reference)
- [1. Write the Production Config](#1-write-the-production-config)
- [2. Install Logto](#2-install-logto)
- [3. Diagnostics and Config Review](#3-diagnostics-and-config-review)
  - [3.1 PostgreSQL Prerequisite](#31-postgresql-prerequisite)
  - [3.2 Preflight Check](#32-preflight-check)
  - [3.3 Review the Plan](#33-review-the-plan)
  - [3.4 Re-verify a Running Install](#34-re-verify-a-running-install)
- [4. Troubleshooting](#4-troubleshooting)
- [5. Related Documents](#5-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below. PostgreSQL is a **prerequisite** — install it with [`postgres-deploy.md`](postgres-deploy.md), never from this guide.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Write and review the production stack config — not in the repository yet
${EDITOR:-vi} "log-to/Pulumi.${PULUMI_STACK}.yaml"

# 2. Install: check -> ensure-stack -> preview -> apply -> rollout -> verify
just logto install
```

Diagnostics and config review — **not** steps of the normal path, see [§3](#3-diagnostics-and-config-review):

```bash
just postgres verify                      # PostgreSQL prerequisite on its own
just logto check                          # read-only preflight, applies nothing
just gcp-pulumi preview --folder log-to   # read the plan before a first or changed apply
just logto verify                         # re-check an already running install
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env prod --cluster <prod-cluster>`. The sub-shell sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PROJECT_ID`, and `KUBECONFIG`.
- Recipes default `--stack` to `$PULUMI_STACK` and `--namespace` to `prod`, so no recipe below needs a flag.
- Outside the sub-shell recipes fail with `Error: no stack name` — there is no `dev` fallback.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Write the Production Config

**The production config does not exist in this repository.** Write `log-to/Pulumi.<prod-cluster>.yaml` from the reference template before anything else — `install` refuses to continue without it, and `ensure-stack` refuses to initialize a stack with no matching config file, because an empty stack fails at preview with `missing required configuration variable`. Keep `databaseSecret`, `endpoint`, and the ingress host/TLS values **plaintext**: `check` and `verify` read this file with `yq`, and an encrypted `secure:` value reads back as a map, not a string.
→ [Production configuration](reference/logto/deployment.md#production-configuration) · [Stack config recipes](reference/pulumi/stack-config.md)

```bash
${EDITOR:-vi} "log-to/Pulumi.${PULUMI_STACK}.yaml"
```

---

## 2. Install Logto

**Applies unattended.** One composite run: `check` → `ensure-stack` → `preview` → `create-resource` (`pulumi up -f -y`) → `kubectl rollout status deployment/logto` (10-minute budget from `--retries 60 --interval 10`) → `verify`. The preview inside this run is a record, not a prompt — on a first production apply or after a config change, read the standalone preview first ([§3.3](#33-review-the-plan)). Creates PVC `logto-claim`, Deployment `logto`, Services `logto-api` (3001) and `logto-admin` (3002), and Ingress `logto-ingress` routed to `logto-api` only — the admin Service stays internal. The rollout wait clears only once a pod is **Ready**, and readiness is an HTTP GET on `/api/status`, so a successful install means Logto answered a request, not merely that the container started. Re-run the same command for upgrades after changing `image.tag`.
→ [`just logto install`](reference/logto/deployment.md#just-logto-install) · [Readiness and rollout](reference/logto/deployment.md#readiness-and-rollout)

```bash
just logto install
```

---

## 3. Diagnostics and Config Review

`install` already runs the prerequisite check, the preview, and the verification internally. Use the recipes below to review config before the first apply, or to diagnose a failure afterwards — none of them is a required step of the normal path.

### 3.1 PostgreSQL Prerequisite

PostgreSQL is installed by its own guide, not here. Logto needs the CloudNativePG Cluster `logto` healthy in namespace `prod`, with Secret `logto-app` and Service `logto-rw`. Run this on its own only to fix the prerequisite before touching Logto config — `just logto check` re-runs the same recipe internally.
→ [PostgreSQL deployment guide](postgres-deploy.md) · [Prerequisites](reference/logto/deployment.md#prerequisites)

```bash
just postgres verify
```

### 3.2 Preflight Check

Read-only gate, and the first step of `install`. Validates the cluster-env exports and credentials file, the stack config (`name: logto`, namespace, `databaseSecret: logto-app`, a pinned non-`latest` image tag, no `<placeholder>` values), that `namespace-bootstrap` owns the same app namespace, that PostgreSQL verifies, that Secret `logto-app` carries `username`/`password`, and that Service `logto-rw` exposes 5432. Nothing is created or applied.
→ [`just logto check`](reference/logto/deployment.md#just-logto-check)

```bash
just logto check
```

### 3.3 Review the Plan

`install` prints a preview and then applies it without pausing — `create-resource` runs `pulumi up -f -y`. Run the standalone preview first on any first apply or config change, and read every resource in it.
→ [`just logto install`](reference/logto/deployment.md#just-logto-install)

```bash
just gcp-pulumi preview --folder log-to
```

### 3.4 Re-verify a Running Install

Runs as the tail of `install`; stands alone for re-checks after a restart, an upgrade, or a cluster event. Asserts the Deployment has an available replica, a Running and fully ready `app=logto` pod exists, PVC `logto-claim` is `Bound`, both Services expose their configured ports, and the Ingress carries the host and TLS Secret from the stack config. Prints the last 100 log lines — a Ready pod proves `/api/status` answers, not that the seed and alteration steps logged clean.
→ [`just logto verify`](reference/logto/deployment.md#just-logto-verify)

```bash
just logto verify
```

---

## 4. Troubleshooting

→ [Full troubleshooting table](reference/logto/deployment.md#troubleshooting) · [PostgreSQL troubleshooting](reference/postgres/troubleshooting.md)

Common issues:
- **`Error: no stack name`**: Enter `just cluster-env` first, or pass `--stack <name>`
- **`Error: stack config missing`**: `log-to/Pulumi.<prod-cluster>.yaml` not written yet — see [production configuration](reference/logto/deployment.md#production-configuration)
- **`must use databaseSecret logto-app, got '{ "secure": ... }'`**: The value is Pulumi-encrypted; `check` reads plaintext YAML — see [production configuration](reference/logto/deployment.md#production-configuration)
- **`namespace-bootstrap exports app namespace ...`**: Wrong stack selected, or `--namespace` does not match the bootstrap export — see [shared namespaces](reference/pulumi/namespaces.md)
- **`secret "logto-app" not found`**: PostgreSQL not deployed in `prod`, or `databaseSecret` points elsewhere — run `just postgres verify`
- **Rollout wait times out with the pod `Running` but `0/1` ready**: `/api/status` never answered — the container is still in `db seed` / `db alteration deploy`, or the process exited; read `kubectl -n prod logs deployment/logto`
- **Admin console unreachable**: Expected. `logto-admin` has no Ingress and the program sets no `ADMIN_ENDPOINT` — see [admin console access](reference/logto/deployment.md#admin-console-access)

---

## 5. Related Documents

**Reference details for this guide:**
- [Logto deployment details](reference/logto/deployment.md)
- [Stack config recipes](reference/pulumi/stack-config.md)
- [Shared namespaces](reference/pulumi/namespaces.md)

**Other documentation:**
- PostgreSQL prerequisite: [`postgres-deploy.md`](postgres-deploy.md)
- PostgreSQL cluster details: [`reference/postgres/cluster.md`](reference/postgres/cluster.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- [Official Logto OSS deployment documentation](https://docs.logto.io/logto-oss/deploy)
