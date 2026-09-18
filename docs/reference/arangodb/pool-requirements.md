# Pool Requirements for ArangoDB

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## ArangoDB Resource Shape

Stack: `arangodb-cluster`, image `arangodb:3.12.11`, amd64, `externalAccess: None`

| Role | Count | CPU | Memory | Disk |
|------|-------|-----|--------|------|
| Agents | 3 | 250m | 1Gi | 20Gi `dictycr-ssd` |
| DBServers | 3 | 2 | 12Gi | 150Gi `dictycr-balanced` |
| Coordinators | 3 | 500m | 2Gi | none |

Anti-affinity is **preferred** (hostname 100, zone 50), not required. No PDBs.

> **Lab vs Production**: Lab `arangodb-single` stays Single / 3.11 / arm64 — do not edit it. Use `Pulumi.dcr-kube1.yaml` only.

## Kubernetes Pool Requirements

The `stateful-db` pool should already exist if `config/kops/<prod-cluster>/instancegroups.yaml` matches [README §3](../../kops-setup.md#3-cluster-bootstrap-git-native-flow) **plus** the production taint.

| Field | Expected Value |
|-------|----------------|
| `machineType` | `n2-standard-4` (or `n2-highmem-4` if RAM tight) |
| `minSize` / `maxSize` | `3` / `3`, zones `us-central1-a/b/c` |
| `nodeLabels.pool` | `database` |
| `taints` | `dedicated=database:NoSchedule` |

### Verification

```bash
just arangodb check-pool
```

Checks and prints PASS/FAIL for:
- Node count (expect 3)
- Architecture (must be amd64)
- Taint (`dedicated=database:NoSchedule`)
- Ready status
- Zone spread (informational)

### Missing Pool or Taint

Edit YAML and apply via [README Day-2](../../kops-setup.md#4-day-2-operations-git-first-workflow). Do not use `dictycr-dev-staging-dcr-experiments`.

### HA Sizing Background

See [`kops-gcp-architecture.md` §4.2](../../kops-gcp-architecture.md#2-statefuldatabase-node-pool-static).

## Prerequisites

Before installing ArangoDB ([§3](../../arangodb-deploy.md#3-install-arangodb)):

| # | Prerequisite | Owner | Why it blocks the install |
|---|--------------|-------|---------------------------|
| 1 | StorageClasses `dictycr-ssd` and `dictycr-balanced` (PD CSI driver) | `storage_class` stack — `just gcp-pulumi apply-storageclass`, [`pulumi-setup.md` §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces) | Agent and DBServer PVCs name these classes; without them members stay Pending |
| 2 | Namespaces `prod` and `operators` | `namespace-bootstrap` stack — `just gcp-pulumi apply-namespaces`, same §5 | Nothing in the ArangoDB stacks creates a namespace |
| 3 | Secret `dictycr` (`resticPass`, `gcsProject`, `gcsCredentials`) | `backup_secrets` stack — [configure-backup-secrets](backup.md#configure-backup-secrets) | Backup and restore Jobs resolve it at apply time, so it must exist before them |
| 4 | A `Pulumi.<cluster>.yaml` in every project this guide deploys | the repo — [stack names](../pulumi/stack-names.md#guard) | `ensure-stack` refuses to initialize a stack with no template and points at `fork-stack` |

**Namespaces are never created as a side effect.** `backup_secrets/main.go` and `arangodb-operator` both probe the `namespace-bootstrap` stack (`nsprobe.Probe`) and take their target from its `appNamespace` / `operatorsNamespace` exports; a missing bootstrap fails at preview, and an explicit `--namespace` that disagrees with the export stops the recipe before any Pulumi work.
