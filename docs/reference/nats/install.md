# NATS Install Details

Back to: [NATS Deploy Guide](../../nats-deploy.md)

## What It Does

Applies the `nats` stack (`Pulumi.dcr-kube1.yaml`) which creates:

1. Secret `nats-auth` — key `token`
2. Helm release `nats` (chart `nats` **2.14.6**, [nats-io/k8s](https://github.com/nats-io/k8s)) — StatefulSet `nats`, 1 replica, image `nats:2.15.0-alpine` (chart default `2.14.6-alpine` overridden)
3. Service `nats` on 4222 (client port) plus the headless service for the StatefulSet
4. `nats-box` deployment (chart default) — the `nats` CLI pod for ad-hoc checks

No persistent volume: core pub/sub keeps no message persistence — no JetStream, no resolver, in-flight messages are lost on a pod restart. JetStream persistence is reserved for a future revision.

## Command

```bash
just nats deploy --token '<token>'
```

## Behavior

- Runs `ensure-stack` → sets the encrypted `properties.auth.token` → `preview` → `create-resource`
- Restarts `statefulset/nats` so the pod re-resolves the Secret-backed `TOKEN` env variable — env-backed values never refresh in a running pod
- Waits for `statefulset/nats` to report a ready replica
- Prints the `nats` Service

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--token` | Yes | — | The auth token; stored encrypted as `properties.auth.token`; never generated or defaulted |
| `--namespace` | No | `prod` | Must match `properties.namespace` in the stack config — the release deploys there and `deploy` aborts on drift |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` / `--interval` | No | `60` / `10` | 10-minute wait budget |

## Token Auth

The chart's `authorization.token` is set to the special value `<< $TOKEN >>` — the chart unquotes it into the rendered `nats.conf` (`"token": $TOKEN`), and the **NATS server expands `$VARIABLE` references from its process environment at startup**. `TOKEN` itself comes from Secret `nats-auth` (key `token`) via `secretKeyRef`. Clients must present the token: `nats://<token>@nats.prod.svc.cluster.local:4222` or `--token` in the CLI. Traced in the rendered ConfigMap of chart 2.14.6.

**No unauthenticated fallback exists** — without the token the server answers `-ERR 'authorization violation'`. The `verify` recipe checks both directions (authenticated `rtt` succeeds, unauthenticated `rtt` fails with an authorization violation).

## Probes and Shutdown

- **Probes**: the chart wires startup/readiness/liveness `httpGet` checks against the monitor endpoint (`:8222/healthz`) — enabled by chart default, no auth on the monitor path
- **Graceful shutdown**: lame-duck grace 10s + eviction 30s (chart defaults), `terminationGracePeriodSeconds: 60`
- **Reloader**: file-backed config changes hot-reload via the `nats-server-config-reloader` sidecar — no pod restart needed for config edits. Secret-backed **env variables do not refresh** in a running pod: token rotation always requires the StatefulSet restart that `deploy` performs

## Service and Credentials

| Item | Value |
|------|-------|
| Address | `nats.prod.svc.cluster.local:4222` |
| URL form | `nats://<token>@nats.prod.svc.cluster.local:4222` |
| Token | Secret `nats-auth`, key `token` |
| Monitor | pod-local `:8222/healthz` (not exposed via the Service) |

Read the token with `kubectl get secret nats-auth -n prod -o jsonpath='{.data.token}' \| base64 -d`.

## Single Server, No Clustering

`config.cluster.enabled` stays **false** (chart default) — one server, no replication: a pod/node failure means downtime until Kubernetes reschedules it. High availability (3-server cluster + JetStream file store) is reserved for a future revision — do not flip `cluster.enabled` without a dedicated review, the route-auth and storage layout change with it.
