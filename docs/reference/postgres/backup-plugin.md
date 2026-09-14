# Barman Cloud Plugin — Backup Architecture

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Is

The CNPG-I plugin that owns WAL archiving and base backups for CloudNativePG clusters, replacing the deprecated in-tree `barmanObjectStore` support (deprecated since CNPG 1.26, removed in 1.31). Deployed as Helm chart `plugin-barman-cloud` ([program](#the-stack)) into the operator namespace — the same namespace as the operator, as the plugin requires.

## The Stack

`cnpg-backup-plugin/` — deploys the chart (pinned **0.8.0**, `cnpg-backup-plugin/Pulumi.dcr-kube1.yaml`) into namespace `operators`. Requires:

- The `namespace-bootstrap` stack (`operators` namespace) — the program probes it (`internal/nsprobe`)
- cert-manager — pre-configured by the kops bootstrap
- No secrets

The cloudnative-pg-cluster program **probes this stack** (`nsprobe.ProbePlugin`) — a missing plugin fails the cluster stack's `preview` instead of silently breaking WAL archiving.

## Command

```bash
just postgres deploy-backup-plugin
```

## Behavior

- Runs `ensure-stack` → `preview` → `create-resource` on `cnpg-backup-plugin`
- Waits for the `plugin-barman-cloud` deployment to roll out
- Asserts the `objectstores.barmancloud.cnpg.io` CRD exists

Runs standalone, or as the composite tail of [`configure-backup`](backup.md) — one command wires identity, config, and plugin.

## Configuration

- **No secrets required**
- Chart version 0.8.0 tracks plugin image `v0.15.x` ([chart releases](https://github.com/cloudnative-pg/charts/releases?q=plugin-barman-cloud))
- Plugin resources: docs at [cloudnative-pg.github.io/plugin-barman-cloud](https://cloudnative-pg.github.io/plugin-barman-cloud)

## Where the Wiring Lands

| Piece | Owner |
|-------|-------|
| Plugin deployment + CRDs | `cnpg-backup-plugin` stack (this one) |
| `ObjectStore` CR (barmancloud.cnpg.io/v1) | `cloudnative-pg-cluster` — created per cluster, references Secret `postgres-backup-credentials` |
| Backup retention | `ObjectStore.spec.retentionPolicy` — moved off the Cluster |
| WAL compression / parallelism | `ObjectStore.spec.configuration.wal` |
| `ScheduledBackup` | unchanged resource, but `method: plugin` + `pluginConfiguration` |

## Cluster Config References

The cluster stack's `plugins` block references the store by the derived name `<cluster>-store`; the recovery path creates a second, read-only ObjectStore for the source cluster's bucket (`externalClusters[].plugin`). Both are built by [`cloudnative-pg-cluster`](cluster.md) — no manual wiring.