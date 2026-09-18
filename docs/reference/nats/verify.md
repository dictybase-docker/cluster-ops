# NATS Verify Details

Back to: [NATS Deploy Guide](../../nats-deploy.md)

## What It Does

Read-only post-install check of the running NATS server: StatefulSet
readiness, Service port, auth Secret, one authenticated `rtt`, and one
negative unauthenticated check. Changes nothing; safe to re-run at any time.

## Command

```bash
just nats verify
```

## Behavior

Each check prints PASS/FAIL (plain text — colors are disabled in this recipe);
the recipe exits non-zero if any of them fails.

1. **StatefulSet** — `statefulset/nats` reports at least one ready replica
2. **Service** — `nats` exposes the client port `4222`
3. **Secret** — `<secret>` exists in the namespace
4. **Auth handshake** — `nats --server ... --token <token from Secret> rtt` inside the chart's `nats-box` pod succeeds
5. **Negative check** — the same `rtt` without a token fails with an authorization violation, proving auth is actually on (any other failure is reported as-is, not counted as proof)

The handshake steps are skipped when the StatefulSet has no ready replica
(that failure is the one to act on), and fail with a named message when
`nats-box` itself is missing.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | |
| `--secret` / `-e` | No | `nats-auth` | Token is read from key `token` |

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
TOKEN=$(kubectl get secret nats-auth -n prod -o jsonpath='{.data.token}' | base64 -d)
kubectl -n prod exec deploy/nats-box -- \
  nats --server nats://nats.prod.svc.cluster.local:4222 --token "$TOKEN" rtt
```
