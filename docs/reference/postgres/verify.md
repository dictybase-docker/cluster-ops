# PostgreSQL Verify Details

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Read-only post-install check across every layer the deploy touches — pool, operator, storage, Cluster CR, pods, PVCs, Services, ScheduledBackup. Reads nothing but Kubernetes API objects, changes nothing, and exits non-zero if any check fails.

## Command

```bash
just postgres verify
```

## Checks

| # | Check | Pass condition |
|---|-------|----------------|
| 1 | Pool size | 3 nodes labeled `pool=database` |
| 2 | Pool taint | every one of those nodes carries `dedicated=database:NoSchedule` |
| 3 | Operator | at least one Running pod `app.kubernetes.io/name=cloudnative-pg` in `operators` |
| 4 | Storage | StorageClasses `dictycr-balanced` **and** `dictycr-ssd` exist |
| 5 | Cluster CR | `cluster.postgresql.cnpg.io/logto` present in `prod` |
| 6 | Cluster phase | status phase is exactly `Cluster in healthy state` |
| 7 | Instance pods | 1 pod `cnpg.io/cluster=logto` Running with all containers ready |
| 8 | Data PVC | 1 PVC `cnpg.io/cluster=logto` in phase `Bound` |
| 9 | Services | `logto-rw`, `logto-ro`, `logto-r` each expose port 5432 |
| 10 | ScheduledBackup | at least one `scheduledbackup.postgresql.cnpg.io` in `prod` |

Every failure is counted, not fatal on the spot — the run prints all results, then the failure count and a pointer to [troubleshooting](troubleshooting.md) before exiting non-zero.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--cluster` | No | `logto` | Cluster CR name; also the `cnpg.io/cluster` selector and Service prefix |
| `--namespace` | No | `prod` | Namespace holding the Cluster |
| `--operator-namespace` | No | `operators` | Namespace holding the operator |
| `--instances` | No | `1` | Expected ready pod count **and** expected Bound PVC count |
| `--pool` | No | `database` | Value of the node label `pool` and of the `dedicated` taint |
| `--node-count` | No | `3` | Expected node count in that pool |

No `--stack`: every check runs against `$KUBECONFIG`, not against Pulumi state.

## Scope

`verify` proves the *installation* is healthy, not that the *data* is right. It never connects to PostgreSQL and never inspects databases, tables, or row counts. After an import ([import details](import.md)), confirm the data yourself:

```bash
kubectl cnpg psql logto -n prod    # \l, \dt, counts
```

See [kubectl cnpg plugin](troubleshooting.md#kubectl-cnpg-plugin) for the install.
