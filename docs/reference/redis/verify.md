# Redis Verify Details

Back to: [Redis Deploy Guide](../../redis-deploy.md)

## What It Does

Read-only post-install check of the running standalone Redis: node pool, pod
readiness, data PVC, Service port, and one `PING` handshake. Changes nothing;
safe to re-run at any time.

## Command

```bash
just redis verify
```

## Behavior

Each check prints PASS/FAIL (plain text — colors are disabled in this recipe);
the recipe exits non-zero if any of them fails.

1. **Pool** — `kubectl get nodes -l pool=database` returns exactly `--node-count` nodes (default 3)
2. **Pod** — at least one `app=<name>` pod is `Running` with every container `ready`
3. **PVC** — `<name>-data` is `Bound`
4. **Service** — `<name>` exposes port `6379`
5. **PING handshake** — `redis-cli PING` inside the pod returns `PONG`; the server is unauthenticated, so no credentials are needed and a failure is a real connectivity problem

The handshake step is skipped when no ready pod was found (steps 2 and 5 then
fail and pass respectively; the pod failure is the one to act on).

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
