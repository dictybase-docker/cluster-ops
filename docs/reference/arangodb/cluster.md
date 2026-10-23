# ArangoDB Cluster Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## What It Does

`arangodb-cluster/Pulumi.dcr-kube1.yaml` creates:
- Root-password Secret `arangodb-pass`
- `ArangoDeployment` Cluster CR matching the [resource shape](pool-requirements.md): 3 agents, 3 dbservers, 3 coordinators
- Image: `arangodb:3.12.11`
- `externalAccess: None`
- TLS: `caSecretName: "None"` (plain HTTP, internal only)

## Command

```bash
just arangodb deploy-cluster --root-password '<strong-root-password>'
```

## Behavior

1. Runs `ensure-stack` → `set-secret properties.secret.password` → `preview` → `create-resource`
2. Waits for `--members` (default 9) ready pods labelled `arango_deployment=arangodb`
3. Prints coordinator Services

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--root-password` | Yes | Never generated. Stored encrypted in stack config, becomes Secret `arangodb-pass` key `password` |
| `--namespace` | No | Defaults to `prod`. Must match `properties.namespace` in `Pulumi.dcr-kube1.yaml`. Only used for pod watching |

## Wait Budget

`--retries 90 --interval 10` (15 minutes). Readiness is all this waits on; PVC class/size and Service checks come from [`just arangodb verify`](verify.md).

## Coordinator Address

All subsequent steps, imports, and app services reach the database at:

```
http://arangodb.prod.svc.cluster.local:8529
```

If the operator names the Service differently:
```bash
kubectl get svc -n prod -l arango_deployment=arangodb
```

## Scope

Does not create the application user or any logical database.

## Update Cluster Configuration

Use `just arangodb update-cluster` to apply day-2 changes to `arangodb-cluster/Pulumi.<stack>.yaml` on a running instance without re-supplying `--root-password`.

### Command

```bash
# 1. Set or update a member startup argument or resource request
just gcp-pulumi set-config --folder arangodb-cluster \
  --key 'properties.dbservers.args[0]' \
  --value '--rocksdb.block-cache-shard-bits=4'

# 2. Optional: read the plan before applying
just gcp-pulumi preview --folder arangodb-cluster

# 3. Apply the change, wait for the rolling restart, and verify
just arangodb update-cluster
```

### Behavior

1. Resolves `--stack` from `$PULUMI_STACK` (no `dev` fallback).
2. Runs `ensure-stack` → `preview` → `create-resource` on `arangodb-cluster` without touching `properties.secret.password`.
3. Waits for `ArangoDeployment/arangodb` to report `status.phase=Running`.
4. Waits for all `--members` (default `9`) pods labelled `arango_deployment=arangodb` to return to `Running` with all containers ready.
5. Runs `just arangodb verify --namespace <namespace>`.

### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | Namespace holding the `ArangoDeployment` |
| `--stack` / `-s` | No | `$PULUMI_STACK` | No `dev` fallback |
| `--members` / `-m` | No | `9` | Expected ready member pods (3 agents + 3 dbservers + 3 coordinators) |
| `--retries` / `-r` | No | `90` | Readiness probe attempts |
| `--interval` / `-i` | No | `10` | Seconds between probes (15-minute wait budget with default retries) |

### Member Startup Arguments (`args`)

Each member role (`agents`, `dbservers`, `coordinators`) accepts an optional `args` string list under `arangodb-cluster:properties.<role>.args`. When `args` is non-empty, the program writes `spec.<role>.args` on the `ArangoDeployment` custom resource, and the `kube-arangodb` operator appends those flags to the `arangod` container command line. When `args` is omitted or empty, the program omits `spec.<role>.args`.

Production `Pulumi.dcr-kube1.yaml` sets one startup flag on `dbservers`:

| Role | Argument | Purpose |
|------|----------|---------|
| `dbservers` | `--rocksdb.block-cache-shard-bits=4` | Sets the RocksDB block cache to `16` shards (`2^4`) instead of the ArangoDB default (`-1`, auto-sized by CPU/memory). This setting reduces cache mutex contention on the 3 DBServer pods. |

### Rolling Restart Lifecycle

When you change `args`, `cpuRequest`, `memoryRequest`, or `version` on a member group:

1. Pulumi patches the `ArangoDeployment` custom resource in place.
2. The `kube-arangodb` operator rotates one member pod at a time within the changed role (`dbservers`, `agents`, or `coordinators`) and waits for each pod to become ready before the next pod restarts.
3. Client traffic through `http://arangodb.prod.svc.cluster.local:8529` stays up because 3 coordinators and replication across the 3 DBServers remain available during a single-member restart.
4. Confirm that the new flag reached the running pods:
   ```bash
   kubectl -n prod get pods -l arango_deployment=arangodb,role=dbserver \
     -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[0].command}{"\n"}{end}'
   ```
5. Commit and push `arangodb-cluster/Pulumi.<stack>.yaml` to `cluster-ops` so Git matches the live cluster state.
