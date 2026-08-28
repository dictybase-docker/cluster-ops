# ArangoDB Data Import Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

> **Production first load is not a loader.** It is the cross-project restic bootstrap in [deploy guide §4](../../arangodb-deploy.md#4-import-data), which restores a pinned snapshot from another GCP project's bucket through the existing restore Job. This page documents the loader Jobs, which remain the fallback for data that is not available as a restic snapshot — and the exception case where one database was absent from the dump.
>
> Do not run loaders after a successful bootstrap: they would overwrite restored graphs.

## Prerequisites

- Cluster Ready
- Databases created
- Production object storage configured

> **No loader has a `Pulumi.prod.yaml` in this repo** — only `Pulumi.experiments.yaml` / `Pulumi.dev.yaml` / `Pulumi.staging.yaml`. Author the production stack config first; the recipe refuses to run without it.

## Graph Dumps (arangodb-dataloader)

Imports MinIO `export/chado` → `chado`, `export/cgm_ddb` → `cgm_ddb`.

- Namespace: `prod`
- Host: from [cluster step](cluster.md)
- Prefer `stateless-web` pool; add database toleration only if Job must sit on `stateful-db`

```bash
just arangodb deploy-loader --folder arangodb-dataloader
```

## Content and UniProt

```bash
just arangodb deploy-loader --folder load-content-from-s3
just arangodb deploy-loader --folder load-uniprot-mapping
```

Neither project has `Pulumi.prod.yaml` — only `dev`, `experiments`, and `staging` configs.

## Recipe Behavior

1. Checks `<folder>/Pulumi.yaml` and `<folder>/Pulumi.<stack>.yaml` both exist
2. Runs `ensure-stack` → `preview` → `create-resource`
3. Lists Jobs in `--namespace` (default `prod`)

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--folder` | Yes | Loader project directory |
| `--stack` | No | From `$PULUMI_STACK` or explicit, no `dev` fallback |

### Current State

Exits with "author it before deploying" error because `arangodb-dataloader/Pulumi.prod.yaml` does not exist.

### Job Monitoring

Does not wait on import Job — run times vary by dataset:
```bash
kubectl logs -n prod job/<name> -f
```
