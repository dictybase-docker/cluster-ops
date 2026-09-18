# Production NATS

Provisioning guide for production **NATS 2.15** on kOps, via the official `nats-io/k8s` Helm chart. Schedules on the general `nodes` instance group.

**Status**: Production procedure. Use `Pulumi.dcr-kube1.yaml` configs only. Do not edit the lab `experiments`/`local` stacks of `nats`.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Install NATS](#1-install-nats)
- [2. Teardown](#2-teardown)
- [3. Verify](#3-verify)
- [4. Troubleshooting](#4-troubleshooting)
- [5. Related Documents](#5-related-documents)

---

## Quick Reference

For experienced users. Full details in sections below.

```bash
# Enter cluster environment first
just cluster-env --env prod --cluster <prod-cluster>

# 1. Deploy NATS 2.15 (single server, unauthenticated, core pub/sub — no
#    message persistence; schedules on the general nodes group)
just nats deploy

# 2. Verify installation
just nats verify
```

Optional steps:
```bash
# Teardown (clone only — removes the server, nats-box, and the config)
just nats teardown
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env prod --cluster <prod-cluster>`. The sub-shell sources `.env.prod.<prod-cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Recipes default `--stack` to `$PULUMI_STACK`, so no recipe below needs the flag.
- Outside the sub-shell recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Install NATS

Creates a single-server NATS **2.15.0** StatefulSet (Helm chart `nats` 2.14.6): unauthenticated, monitor probes on 8222. Core pub/sub only — no JetStream, no PVC, in-flight messages are lost on a pod restart. Any pod in the cluster can connect.
→ [Install details](reference/nats/install.md) · [address](reference/nats/install.md#service) · [instance group requirements](reference/nats/pool-requirements.md)

```bash
just nats deploy
```

---

## 2. Teardown

Removes the Helm release: the server StatefulSet, `nats-box`, the Services, and the ConfigMap. No message persistence — nothing else to delete.
→ [Teardown details](reference/nats/teardown.md)

```bash
just nats teardown
```

---

## 3. Verify

Read-only checks plus a client `rtt` handshake. Exits non-zero if any check fails.
→ [Verify details](reference/nats/verify.md)

```bash
just nats verify
```

---

## 4. Troubleshooting

→ [Full troubleshooting table](reference/nats/troubleshooting.md)

---

## 5. Related Documents

**Reference details for this guide:**
- [Instance group requirements](reference/nats/pool-requirements.md)
- [Install details](reference/nats/install.md)
- [Teardown details](reference/nats/teardown.md)
- [Verify details](reference/nats/verify.md)
- [Troubleshooting](reference/nats/troubleshooting.md)

**Other documentation:**
- PostgreSQL on the stateful-db pool: [`postgres-deploy.md`](postgres-deploy.md)
- Redis on the stateful-db pool: [`redis-deploy.md`](redis-deploy.md)
- Architecture: [`kops-gcp-architecture.md`](kops-gcp-architecture.md)
- Cluster bootstrap: [`kops-setup.md`](kops-setup.md)
- Pulumi backend + StorageClass: [`pulumi-setup.md`](pulumi-setup.md)
