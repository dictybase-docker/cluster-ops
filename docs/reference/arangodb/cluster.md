# ArangoDB Cluster Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## What It Does

`arangodb-cluster/Pulumi.prod.yaml` creates:
- Root-password Secret `arangodb-pass`
- `ArangoDeployment` Cluster CR matching the [resource shape](pool-requirements.md): 3 agents, 3 dbservers, 3 coordinators
- Image: `arangodb:3.12.10.1`
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
| `--namespace` | No | Defaults to `prod`. Must match `properties.namespace` in `Pulumi.prod.yaml`. Only used for pod watching |

## Wait Budget

`--retries 90 --interval 10` (15 minutes). Full PVC/zone checks come in [verify step](../../arangodb-deploy.md#7-verify).

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
