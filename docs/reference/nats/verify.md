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

Each check prints PASS/FAIL (plain text — colors are disabled in this recipe);
the recipe exits non-zero if any of them fails.

1. **StatefulSet** — `statefulset/nats` reports at least one ready replica
2. **Service** — `nats` exposes the client port `4222`
3. **Handshake** — `nats --server nats...:4222 rtt` inside the chart's `nats-box` pod succeeds; the server is unauthenticated, so no credentials are needed and a failure is a real connectivity problem

The handshake is skipped when the StatefulSet has no ready replica (that
failure is the one to act on), and reported by name when `nats-box` itself
is missing.

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
