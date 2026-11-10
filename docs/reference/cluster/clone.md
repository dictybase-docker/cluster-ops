# Declarative Cluster Clone Details

Back to: [Declarative Cluster Clone Guide](../../cluster-clone.md)

Details for cloning a production `dcr-kube` (`dcr-kube1`) cluster into a new GCP project and cluster without Velero: the four architectural layers, the `clone-datastore-stacks` and `deploy-foundation` recipes, the cross-cluster GCS migration paths for ArangoDB, PostgreSQL, MinIO, and Redis, the application stack scaffolds, the `verify-clone` suite, and the troubleshooting table.

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Prerequisites](#prerequisites)
- [Bootstrap kOps Compute and Pulumi Backend](#bootstrap-kops-compute-and-pulumi-backend)
- [Clone Datastore Stack Configurations](#clone-datastore-stack-configurations)
  - [just gcp-pulumi clone-datastore-stacks](#just-gcp-pulumi-clone-datastore-stacks)
- [Deploy the Cluster Foundation](#deploy-the-cluster-foundation)
  - [just gcp-cluster deploy-foundation](#just-gcp-cluster-deploy-foundation)
- [Migrate Stateful Datastores](#migrate-stateful-datastores)
  - [ArangoDB Snapshot Import and URL Rewrite](#arangodb-snapshot-import-and-url-rewrite)
  - [PostgreSQL Recovery or Logical Import](#postgresql-recovery-or-logical-import)
  - [MinIO Snapshot Bootstrap and Backup](#minio-snapshot-bootstrap-and-backup)
  - [Redis Snapshot Bootstrap and Backup](#redis-snapshot-bootstrap-and-backup)
- [Scaffold and Deploy Application Services](#scaffold-and-deploy-application-services)
- [Deploy the Gateway and Cut Over DNS](#deploy-the-gateway-and-cut-over-dns)
- [Commit and Push the Cloned Stack Files](#commit-and-push-the-cloned-stack-files)
- [Verification](#verification)
  - [just gcp-cluster verify-clone](#just-gcp-cluster-verify-clone)
- [Troubleshooting](#troubleshooting)

---

## Architecture Overview

A `dcr-kube` cluster clone rebuilds the cluster deterministically across four layers instead of restoring raw GCE disk snapshots with Velero:

| Layer | Mechanism | Typical Duration | Why Declarative Clone Beats Disk Snapshots |
|-------|-----------|------------------|--------------------------------------------|
| 1. Compute and network | `kops` (`bootstrap-bundle` + `create-cluster`) | 25 to 30 minutes | Builds fresh GCE VMs, VPCs, and PD CSI drivers from Git manifests |
| 2. Cluster foundation | `clone-datastore-stacks` + `deploy-foundation` | 5 minutes | Initializes every stack with the new cluster KMS key so Pulumi state matches live Kubernetes objects |
| 3. Stateful data | GCS archive imports (`arangodb`, `postgres`, `minio`, `redis`) | 15 to 20 minutes | Uses application-consistent tools (`arangodump`, Barman Cloud / `pg_dump`, `mc mirror` + `restic`, and `redis-cli --rdb` + `restic`) that never corrupt ArangoDB Raft consensus |
| 4. Services and Gateway | `scaffold-*-stack`, `deploy-backend-services`, `gateway deploy` | 5 to 10 minutes | Renders per-cluster endpoints from `config/clusters/<cluster>.yaml` |

---

## Prerequisites

| Requirement | Check | Guide |
|-------------|-------|-------|
| Target GCP project with billing enabled | `gcloud projects describe "$PROJECT_ID"` | [`kops-setup.md`](../../kops-setup.md) |
| Read access to the source cluster (`dcr-kube1`) | `just cluster-env --env prod --cluster dcr-kube1` | [`arangodb-deploy.md`](../../arangodb-deploy.md) |
| Pinned local toolchain (`kops`, `pulumi`, `kubectl`, `yq`, `jq`, `mc`) | `just check-tools` | [Tool versions](../kops/tool-versions.md) |

---

## Bootstrap kOps Compute and Pulumi Backend

Create the target cluster environment, provision the kOps cluster, initialize the KMS-backed Pulumi state bucket, and register the cluster in `config/clusters/<target-cluster>.yaml`.

Full details: [`kops-setup.md`](../../kops-setup.md), [`pulumi-setup.md`](../../pulumi-setup.md), and [cluster registry](../backend/cluster-registry.md).

---

## Clone Datastore Stack Configurations

Application services (`modware-*`, `graphql_server`, `frontend_application`, `dcr-mcp`, `event-messenger`, and `gateway`) generate their stack files from `config/templates/*.tmpl` via `scaffold-*-stack`.

Infrastructure and datastore projects do not use `config/templates/*.tmpl`. Instead, `clone-datastore-stacks` copies their `Pulumi.<from-stack>.yaml` files cleanly for the target cluster.

### `just gcp-pulumi clone-datastore-stacks`

```bash
just gcp-pulumi clone-datastore-stacks --from-stack dcr-kube1
```

#### Behavior

1. Resolves `--to-stack` from `$PULUMI_STACK` and `--project` from `$PROJECT_ID`. Fails if either value is empty.
2. Iterates over the 22 infrastructure and datastore folders:
   - `storage_class`, `namespace-bootstrap`
   - `arangodb-operator`, `arangodb-cluster`, `arangodb-backup`, `arangodb-restore`, `backup_secrets`, `source_backup_secrets`, `create-arangodb-databases`, `reset-root-password`
   - `cloudnative-pg-operator`, `cnpg-backup-plugin`, `cloudnative-pg-cluster`
   - `minio`, `minio-backup-secrets`, `minio-backup`, `minio-restore`
   - `nats`
   - `redis-standalone`, `redis-backup-secrets`, `redis-backup`, `redis-restore`
3. Skips any folder where `Pulumi.<to-stack>.yaml` already exists on disk.
4. Strips `.secretsprovider`, `.encryptedkey`, and all `{secure: ...}` ciphertext blocks from `Pulumi.<from-stack>.yaml` with `yq`. Those ciphertexts belong to the source project's KMS key and cannot be decrypted by the target cluster.
5. Replaces the source stack/project identifier (`dcr-kube1`) with the target `$PROJECT_ID` in `gcp:project` and project-suffixed bucket paths.

When each datastore deploy recipe runs later, `ensure-stack` initializes the stack with the target cluster's `$PULUMI_SECRET_PROVIDER` and writes the new `secretsprovider` and `encryptedkey` headers.

#### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--to-stack` / `-t` | No | `$PULUMI_STACK` | Target cluster stack name |
| `--from-stack` / `-F` | No | `dcr-kube1` | Source production stack to copy from |
| `--project` / `-p` | No | `$PROJECT_ID` | Target GCP project ID |
| `--folder` / `-f` | No | all 22 datastore folders | Restricts the clone to one project folder |

---

## Deploy the Cluster Foundation

### `just gcp-cluster deploy-foundation`

```bash
just gcp-cluster deploy-foundation
```

#### Behavior

1. Validates the kOps HA control plane (`validate-kops-ha`) and the 3-node `stateful-db` pool (`arangodb check-pool`).
2. Applies `storage_class` (`dictycr-balanced` and `dictycr-ssd`) and `namespace-bootstrap` (`operators` and `prod`).
3. Installs the Gateway API CRDs (`gateway install-crds`) and restarts Cilium (`gateway restart-cilium`) so `GatewayClass` `cilium` is accepted.
4. Deploys stateless `nats` and standalone `redis` (`redis-standalone`).
5. Deploys the `cloudnative-pg-operator` and `arangodb-operator` Helm releases into `operators`.

#### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Passed to all Pulumi stack recipes |
| `--namespace` / `-n` | No | `prod` | Application namespace for `nats` and `redis` |

---

## Migrate Stateful Datastores

Every stateful datastore in the cluster backs up to Google Cloud Storage and restores into a clone cluster from its source GCS bucket:

| Datastore | Source GCS Backup Bucket | Daily CronJob Schedule | Clone Migration Recipe |
|-----------|--------------------------|------------------------|------------------------|
| Redis | `gs://restic-redis-backup-<source-project>` | `0 1 * * *` (01:00) | `just redis bootstrap-from-snapshot` |
| MinIO | `gs://restic-minio-backup-<source-project>` | `30 1 * * *` (01:30) | `just minio bootstrap-from-snapshot` |
| ArangoDB | `gs://restic-arangodb-backup-prod` | `0 2 * * *` (02:00) | `just arangodb bootstrap-from-snapshot` + `finalize-bootstrap` + `rewrite-content-urls` |
| PostgreSQL (`CloudNativePG`) | `gs://cloudnative-pg-backup-<source-project>` | `0 0 0 * * *` (00:00) | `just postgres configure-source` + `deploy-cluster` (or `dump-logical` + `restore-logical`) |

### ArangoDB Snapshot Import and URL Rewrite

1. In the **source** cluster shell (`dcr-kube1`), run `just arangodb grant-source-bucket-reader`, `just arangodb source-secret-value --namespace prod`, and `just arangodb source-app-user --namespace prod`.
2. In the **target** cluster shell, deploy the ArangoDB cluster (`configure-backup-secrets` + `deploy-cluster`), wire the source reader Secret (`configure-source-secrets`), import the snapshot (`bootstrap-from-snapshot`), and rotate passwords (`finalize-bootstrap`).
3. If the public MinIO storage hostname changes on the new cluster, rewrite the embedded S3 URLs in the `content` database with `just arangodb rewrite-content-urls`.
4. Enable scheduled backups on the new cluster with `just arangodb deploy-backup`.

Full details: [ArangoDB import details](../arangodb/import.md) and [content URL rewrite](../arangodb/content-urls.md).

### PostgreSQL Recovery or Logical Import

In the **target** cluster shell, wire the target backup identity (`just postgres configure-backup`), point recovery at the source cluster (`just postgres configure-source --source-cluster <source-cluster>`), and create the cluster (`just postgres deploy-cluster --app-password '<app-password>'`).

If the clone upgrades across PostgreSQL major versions, use `just postgres dump-logical --source-cluster <source-cluster>` and `just postgres restore-logical` instead.

Full details: [PostgreSQL import details](../postgres/import.md) and [logical import](../postgres/logical-import.md).

### MinIO Snapshot Bootstrap and Backup

1. In the **source** cluster shell, run `just minio grant-source-bucket-reader` and `just minio source-secret-value --namespace prod`.
2. In the **target** cluster shell, deploy standalone MinIO (`just minio deploy --root-user '<user>' --root-password '<password>'`), wire the read-only source Secret (`just minio configure-source-secrets`), and run `just minio bootstrap-from-snapshot --namespace prod --bucket restic-minio-backup-<source-project>`.
3. Enable daily GCS backups on the target cluster with `just minio configure-backup-secrets --restic-password '<target-restic-pass>'` and `just minio deploy-backup`.

Full details: [MinIO backup details](../minio/backup.md) and [MinIO restore and bootstrap details](../minio/restore.md).

### Redis Snapshot Bootstrap and Backup

1. In the **source** cluster shell, run `just redis grant-source-bucket-reader` and `just redis source-secret-value --namespace prod`.
2. In the **target** cluster shell (`redis-standalone` is already running from `deploy-foundation`), wire the read-only source Secret (`just redis configure-source-secrets`) and run `just redis bootstrap-from-snapshot --namespace prod --bucket restic-redis-backup-<source-project>`.
3. Enable daily GCS backups on the target cluster with `just redis configure-backup-secrets --restic-password '<target-restic-pass>'` and `just redis deploy-backup`.

Full details: [Redis backup details](../redis/backup.md) and [Redis restore and bootstrap details](../redis/restore.md).

---

## Scaffold and Deploy Application Services

After the datastores are running and populated, deploy the application services in dependency order:

1. **Modware gRPC backend services** (`order`, `annotation`, `anno-feat`, `stock`, `content`):
   - `just ci create-deploy-key`
   - `just ci render-composite-deploy`
   - `just ci deploy-backend-services`
   - Refer to [`backend-service-deploy.md`](../../backend-service-deploy.md).
2. **GraphQL server** (`graphql_server`):
   - `just gcp-pulumi scaffold-graphql-stack --store-endpoint https://<target-minio-host>`
   - Pin `properties.image.tag` and run `just gcp-pulumi create-resource --folder graphql_server`.
   - Refer to [`graphql-deploy.md`](../../graphql-deploy.md).
3. **MCP server** (`dcr-mcp`):
   - `just gcp-pulumi scaffold-dcr-mcp-stack`
   - Pin `properties.image.tag` and run `just gcp-pulumi create-resource --folder dcr-mcp`.
   - Refer to [`dcr-mcp-deploy.md`](../../dcr-mcp-deploy.md).
4. **Frontend applications** (`frontend_application`):
   - `just gcp-pulumi scaffold-frontend-stack`
   - Pin the four app tags (`frontpage`, `publication`, `stockcenter`, `genomepage`) and run `just gcp-pulumi create-resource --folder frontend_application`.
   - Refer to [`frontend-deploy.md`](../../frontend-deploy.md).
5. **Event Messenger** (`event-messenger`):
   - `just gcp-pulumi scaffold-event-messenger-stack`
   - Set the Mailgun, NCBI, and GitHub secrets, pin `properties.image.tag`, and run `just gcp-pulumi create-resource --folder event-messenger`.
   - Refer to [`event-messenger-deploy.md`](../../event-messenger-deploy.md).

---

## Deploy the Gateway and Cut Over DNS

Scaffold and configure the shared Cilium Gateway (`dictycr-gateway`), deploy it, and point your DNS `A` records at the external LoadBalancer IP address that `just gateway show-ip` prints.

Full details: [`gateway-deploy.md`](../../gateway-deploy.md).

---

## Commit and Push the Cloned Stack Files

**CAUTION: Commit and push `config/kops/<target-cluster>/`, `config/clusters/<target-cluster>.yaml`, and all `*/Pulumi.<target-cluster>.yaml` files to `develop` before any CI tag deploy runs.** GitHub Actions tag workflows clone `cluster-ops` from `develop`.

---

## Verification

### `just gcp-cluster verify-clone`

Read-only verification recipe that checks every layer of a cloned cluster in one command and exits non-zero on any failure.

```bash
just gcp-cluster verify-clone
```

#### Behavior

1. Runs `just gcp-cluster validate-hardening` (cluster-autoscaler, node-problem-detector, metrics-server, cert-manager, node-local-dns).
2. Runs `just gcp-pulumi check-storageclass --classes "dictycr-balanced,dictycr-ssd"`.
3. Runs `just arangodb verify --namespace <ns>`.
4. Runs `just postgres verify --namespace <ns>`.
5. Runs `just minio verify --namespace <ns>`.
6. Runs `just redis verify --namespace <ns>`.
7. Runs `just nats verify --namespace <ns>`.
8. Runs `just dcr-mcp verify`, `just frontend verify`, and `just gateway verify` when their `Pulumi.<stack>.yaml` files exist.

#### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Cluster stack name |
| `--namespace` / `-n` | No | `prod` | Application namespace |

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `ERROR: no target stack` or `ERROR: no target project` | Recipe run outside the target `cluster-env` sub-shell | Enter `just cluster-env --env prod --cluster <target-cluster>` |
| `decrypting secret value: ... PermissionDenied` during `pulumi preview` | `Pulumi.<target-cluster>.yaml` was copied with `cp` or `fork-stack` and still holds source KMS `secure:` blocks | Delete the copied file and run `just gcp-pulumi clone-datastore-stacks --from-stack dcr-kube1` |
| `bootstrap-from-snapshot` fails with HTTP 403 on GCS | Source reader service account missing `roles/storage.objectViewer` on the source bucket, or wrong `restic` password | Re-run `grant-source-bucket-reader` in the source cluster shell and `configure-source-secrets` in the target cluster shell |
| `graphql-api-server` or `event-messenger` pods exit at startup with missing `*_SERVICE_HOST` | Application pods started before `redis`, `minio`, `nats`, or `modware-*` Services existed | Follow the guide order: `deploy-foundation` → datastores → `deploy-backend-services` → `graphql` / `event-messenger` |
| Gateway TLS Secrets stay unissued after `just gateway deploy` | DNS `A` records do not resolve to `just gateway show-ip` yet | Update the DNS `A` records, wait for propagation, and re-run `just gateway verify` |
