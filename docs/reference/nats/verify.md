# NATS Verify Details

Back to: [NATS Deploy Guide](../../nats-deploy.md)

## What It Does

Read-only post-install check of the running NATS server: StatefulSet
readiness, Service port, and a client `rtt` handshake. Changes nothing; safe
to re-run at any time.

## Command

```bash
just nats verify
```

## Behavior

The recipe runs `cluster-ops preflight nats`, which prints one PASS/FAIL line
per check and exits non-zero if any fails.

1. **StatefulSet** — `nats` reports ≥ 1 ready replica
2. **Service** — `nats` exposes the client port 4222
3. **RTT handshake** — `nats rtt` runs inside the chart's nats-box deployment via the API server exec; the server is unauthenticated, so a successful rtt proves the wire end to end. Only runs when the server is ready

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | |

No `--stack` flag: every check is a live `kubectl` read, so the recipe does not
touch Pulumi and does not need `$PULUMI_STACK`.

## When Checks Fail

The recipe prints the failing check and points at
[troubleshooting](troubleshooting.md). Fix the cause, re-run `deploy` if the
stack config changed, then re-run `verify`.

## Client Smoke Test

`nats-box` (deployed by the chart) carries the `nats` CLI — the same command
the verify recipe runs, for interactive use:

```bash
kubectl -n prod exec deploy/nats-box -- \
  nats --server nats://nats.prod.svc.cluster.local:4222 rtt
```
