# Redis Verify Details

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## What It Does

Read-only post-install check of the running standalone Redis: node pool, pod
readiness, data PVC, Service port, and one `PING` handshake (executed inside
the pod by the Go check). Changes nothing; safe to re-run at any time.

## Command

```bash
just redis verify
```

## Behavior

The recipe runs `cluster-ops preflight redis`, which prints one PASS/FAIL
line per check and exits non-zero if any fails (exit 1 timeout-class,
2 terminal — see [waiting](../kubernetes/waiting.md) is unrelated; verify
uses the check exit contract: 0 met, any FAIL → non-zero).

1. **Pool** — nodes with label `pool=database` number exactly `--node-count` (default 3)
2. **Pod** — at least one `app=<name>` pod is `Running` with every container `ready`
3. **PVC** — `<name>-data` is `Bound`
4. **Service** — `<name>` exposes port `6379`
5. **PING handshake** — `redis-cli PING` runs inside the first ready pod via the API server exec; `PONG` passes. The server is unauthenticated, so a failure is a real connectivity problem

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--name` | No | `redis` | Deployment/Service name; the PVC checked is `<name>-data` |
| `--namespace` / `-n` | No | `prod` | |
| `--pool` / `-p` | No | `database` | Value of the node label `pool` |
| `--node-count` / `-c` | No | `3` | Expected node count in that pool |

No `--stack` flag: every check is a live `kubectl` read, so the recipe does not
touch Pulumi and does not need `$PULUMI_STACK`.

## When Checks Fail

The recipe prints the failing check and points at
[troubleshooting](troubleshooting.md), which maps each symptom to its cause.
