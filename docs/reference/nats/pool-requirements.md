# Instance Group Requirements for NATS

Back to: [NATS Deploy Guide](../../nats-deploy.md)

## NATS Resource Shape

Stack: `nats`, image `nats:2.15.0-alpine` (official), single server pod + `nats-box` utility pod

| Component | Count | Disk |
|-----------|-------|------|
| NATS server (standalone, core pub/sub) | 1 | none — no message persistence |

Core pub/sub keeps no state: no JetStream, no resolver, no persistent volume. Message durability is a client concern — republish after a pod restart. The chart still creates a PodDisruptionBudget and the `nats-box` utility deployment.

## Instance Group

The prod config sets no `placement.pool`, so the server schedules on the general **`nodes`** instance group (`e2-standard-2`, `minSize: 2`/`maxSize: 3`, no taints) — the same group the rest of the cluster's stateless workloads use. No StorageClass requirement.

To pin NATS elsewhere (e.g. the dedicated `stateful-db` pool), set `placement.pool` in `nats/Pulumi.dcr-kube1.yaml` — the program merges a `nodeSelector pool=<pool>` plus a `dedicated=<pool>:NoSchedule` toleration into the StatefulSet pod template through the chart's `podTemplate.merge` values. That is a locality choice only; NATS gains nothing from the database pool without JetStream.

> **Lab vs Production**: Lab `experiments`/`local` stacks run chart 1.2.4 / image `2.10.20-alpine` with `namespace: dev` and no auth or placement — do not edit them. Use `Pulumi.dcr-kube1.yaml` only.

## Prerequisites

Before installing NATS ([§1](../../nats-deploy.md#1-install-nats)):

1. **Namespace `prod`** — owned by the `namespace-bootstrap` stack; `deploy` aborts if it is missing, and `--namespace` must match `properties.namespace` on the stack
2. **No StorageClass requirement** — the stack creates no persistent volume
