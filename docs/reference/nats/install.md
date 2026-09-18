# NATS Install Details

Back to: [NATS Deploy Guide](../../nats-deploy.md)

## What It Does

Applies the `nats` stack (`Pulumi.dcr-kube1.yaml`) which creates:

1. Helm release `nats` (chart `nats` **2.14.6**, [nats-io/k8s](https://github.com/nats-io/k8s)) — StatefulSet `nats`, 1 replica, image `nats:2.15.0-alpine` (chart default `2.14.6-alpine` overridden)
2. Service `nats` on 4222 (client port) plus the headless service for the StatefulSet
3. `nats-box` deployment (chart default) — the `nats` CLI pod for ad-hoc checks

No Secret, no PVC: core pub/sub keeps no message persistence — no JetStream, no resolver, in-flight messages are lost on a pod restart. JetStream persistence and token auth are reserved for a future revision.

## Command

```bash
just nats deploy
```

## Behavior

- Runs `ensure-stack` → `preview` → `create-resource`
- Validates `--namespace` against `properties.namespace` in the stack config before touching state
- Waits for `statefulset/nats` to report a ready replica
- Prints the `nats` Service

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` | No | `prod` | Must match `properties.namespace` in the stack config — the release deploys there and `deploy` aborts on drift |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` / `--interval` | No | `60` / `10` | 10-minute wait budget |

## No Authentication

The stack sets no `authorization` block — the server **accepts every in-cluster connection**. Any pod in the cluster can publish to any subject, read any subscriber's stream, and delete JetStream state (none exists today). This is a deliberate trade: zero client-side changes for existing apps.

What keeps the exposure bounded:
- Port 4222 is ClusterIP-only — no external ingress exists
- The monitor port 8222 is pod-local, not exposed via the Service

Adding token auth later means one `gnats.Token(...)`-style change per client plus a Secret wiring — the [redis install](../redis/install.md#persistence-and-auth) shows the house pattern.

## Probes and Shutdown

- **Probes**: the chart wires startup/readiness/liveness `httpGet` checks against the monitor endpoint (`:8222/healthz`) — enabled by chart default
- **Graceful shutdown**: lame-duck grace 10s + eviction 30s (chart defaults), `terminationGracePeriodSeconds: 60`
- **Reloader**: file-backed config changes hot-reload via the `nats-server-config-reloader` sidecar; no pod restart needed for config edits

## Service

| Item | Value |
|------|-------|
| Address | `nats.prod.svc.cluster.local:4222` |
| Monitor | pod-local `:8222/healthz` (not exposed via the Service) |

## Single Server, No Clustering

`config.cluster.enabled` stays **false** (chart default) — one server, no replication: a pod/node failure means downtime until Kubernetes reschedules it. High availability (3-server cluster + JetStream file store) is reserved for a future revision — do not flip `cluster.enabled` without a dedicated review, the route-auth and storage layout change with it.
