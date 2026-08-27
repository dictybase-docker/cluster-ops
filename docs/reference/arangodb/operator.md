# ArangoDB Operator Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## What It Does

Installs the `kube-arangodb` Helm chart (pinned **1.2.42**, `arangodb-operator/Pulumi.prod.yaml`) into namespace `operators`. The operator watches for `ArangoDeployment` custom resources and manages the Cluster's pods, PVCs, and internal Service.

## Command

```bash
just arangodb deploy-operator
```

## Behavior

- Runs `ensure-stack` → `preview` → `create-resource` on `arangodb-operator`
- Waits for a ready `app.kubernetes.io/name=kube-arangodb` pod
- Asserts the `arangodeployments.database.arangodb.com` CRD exists

## Configuration

- **No secrets required**
- `--namespace` defaults to `operators`
- The Helm release does **not** create the namespace — `backup_secrets` must already be applied

## Wait Budget

`--retries 60 --interval 10` (10 minutes) before giving up and printing pods.

## Scope

Touches nothing else — no Cluster CR, no databases, no backup.
