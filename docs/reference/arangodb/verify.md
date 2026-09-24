# ArangoDB Verify Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## What It Does

Read-only post-install audit of the whole installation — pool, operator, storage, members, Service and Jobs. It creates, patches and deletes nothing; every check is a `kubectl get`. One line per check, `PASS` / `FAIL` / `WARN`, then a summary.

## Command

```bash
just arangodb verify
```

## Behavior

| # | Check | Passes when | On miss |
|---|-------|-------------|---------|
| 1 | Pool size | 3 nodes labelled `pool=database` | FAIL |
| 2 | Pool taint | every pool node carries `dedicated=database:NoSchedule` | FAIL |
| 3 | Operator | at least one Running pod `app.kubernetes.io/name=kube-arangodb` in operator namespace (default `prod`) | FAIL |
| 4 | StorageClasses | both `dictycr-balanced` and `dictycr-ssd` exist | FAIL |
| 5 | Cluster CR | `ArangoDeployment/arangodb` exists in `prod` | FAIL |
| 6 | Members | 9 pods `arango_deployment=arangodb` Running with all containers ready | FAIL |
| 7 | Agent PVCs | 3 Bound PVCs, 20Gi, `dictycr-ssd` | FAIL |
| 8 | DBServer PVCs | 3 Bound PVCs, 150Gi, `dictycr-balanced` | FAIL |
| 9 | Coordinator Service | at least one Service in `prod` exposing port 8529 | FAIL |
| 10 | create-databases Job | a Job `app=arangodb-create-databases` with `succeeded >= 1` | WARN when no Job exists at all (15-minute TTL removed it — check Secret `backend` instead), FAIL when one exists but never succeeded |
| 11 | Backup CronJob | `arangodb-backup-cronjob` exists in `prod` | WARN — expected only after [deploy-backup](backup.md#deploy-backup) |

`WARN` never changes the exit status. Any `FAIL` prints `Error: <n> check(s) failed.` and exits non-zero, which makes the recipe usable as a gate in a script.

Checks 7 and 8 assert the [resource shape](pool-requirements.md#arangodb-resource-shape) exactly: a member running on the wrong StorageClass or at the wrong size is reported even though the pod is Ready.

## Not Checked

- **Zone spread.** Anti-affinity is preferred, not required, so a legal cluster can put two members in one zone. The recipe prints that caveat instead of asserting it; `just arangodb check-pool` reports the actual spread as INFO.
- **Architecture and node Ready status.** [`check-pool`](pool-requirements.md#verification) covers those before the install; `verify` re-checks only pool size and taint.
- **Data.** Nothing here proves a database holds documents — that is the spot check in [bootstrap §6](bootstrap.md#6-after-the-restore).

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` | No | `prod` | Namespace holding the ArangoDeployment, member pods, PVCs, Service and Jobs |
| `--operator-namespace` | No | `prod` | Namespace holding the kube-arangodb pod; override for custom installs |
| `--members` | No | `9` | Expected ready member pods (3 agents + 3 dbservers + 3 coordinators) |
| `--pool` | No | `database` | Value of the node label `pool` and of the `dedicated` taint |
| `--node-count` | No | `3` | Expected nodes in that pool |

Output is colorized by default; the recipe sets `CHECK_COLOR=0`, so `just arangodb verify` prints plain `PASS`/`FAIL`/`WARN` words that survive a pipe or a log file.

## Failure Triage

Every failing line maps to a row in [troubleshooting](troubleshooting.md) — pool and taint failures to [pool requirements](pool-requirements.md), PVC failures to [`pulumi-setup.md` §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces).
