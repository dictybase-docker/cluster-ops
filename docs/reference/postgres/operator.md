# CloudNativePG Operator Details

Back to: [PostgreSQL Deploy Guide](../../postgres-deploy.md)

## What It Does

Installs the `cloudnative-pg` Helm chart (chart **0.29.0**, operator image **1.30.0**, `cloudnative-pg-operator/Pulumi.prod.yaml`) into namespace `operators`.

Version floor: operator **1.30.x** is the oldest line that officially supports Kubernetes **1.36** (supported: 1.34/1.35/1.36; PostgreSQL 14–18). Do not downgrade — the 1.24 line pinned in the lab stacks does not support this cluster's Kubernetes. The operator watches for `Cluster` / `ScheduledBackup` / `Backup` custom resources under `postgresql.cnpg.io/v1` and manages instance pods, PVCs, failover, and the `-rw`/`-ro`/`-r` Services.

## Command

```bash
just postgres deploy-operator
```

## Behavior

- Runs `ensure-stack` → `preview` → `create-resource` on `cloudnative-pg-operator`
- Waits for a ready `app.kubernetes.io/name=cloudnative-pg` pod
- Asserts the `clusters.postgresql.cnpg.io` CRD exists

## Configuration

- **No secrets required**
- `--namespace` defaults to `operators`
- The Helm release does **not** create the namespace — `configure-backup` (or the `backup_secrets` stack, when ArangoDB is installed) must have ensured it

## Wait Budget

`--retries 60 --interval 10` (10 minutes) before giving up and printing pods.

## Scope

Touches nothing else — no Cluster CR, no databases, no backups. One operator instance serves every CloudNativePG cluster in the k8s cluster, so a second PostgreSQL cluster does not re-run this step.
