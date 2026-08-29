# Pool Requirements for ArangoDB

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## ArangoDB Resource Shape

Stack: `arangodb-cluster`, image `arangodb:3.12.10.1`, amd64, `externalAccess: None`

| Role | Count | CPU | Memory | Disk |
|------|-------|-----|--------|------|
| Agents | 3 | 250m | 1Gi | 20Gi `dictycr-ssd` |
| DBServers | 3 | 2 | 12Gi | 150Gi `dictycr-balanced` |
| Coordinators | 3 | 500m | 2Gi | none |

Anti-affinity is **preferred** (hostname 100, zone 50), not required. No PDBs.

> **Lab vs Production**: Lab `arangodb-single` stays Single / 3.11 / arm64 — do not edit it. Use `Pulumi.prod.yaml` only.

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

1. **CSI + StorageClasses** — [`pulumi-setup.md` §6](../../pulumi-setup.md#6-first-apply--storageclass)
2. **Namespaces + Secrets** — `prod`, `operators`, and Secret `dictycr` (with `gcsCredentials`, `gcsProject`, `resticPass`), all from `backup_secrets` stack

Apply `backup_secrets` **first** — `arangodb-operator` does not create its own `operators` namespace.
