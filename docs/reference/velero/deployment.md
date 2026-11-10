# Velero Cluster Backup Details

Back to: [Cluster Backup with Velero](../../velero-deploy.md)

Details for the `install-velero/` Pulumi project: the GCP and Kubernetes resources, the comparison between modern `dcr-kube` clusters and the legacy `dcr-experiments` cluster, the service account roles, the stack configuration, the `custodian` datastore and volume exclusions, the deployment and verification recipes, day-2 operations, and the troubleshooting table.

## Table of Contents

- [What the Project Creates](#what-the-project-creates)
- [Modern dcr-kube vs Legacy dcr-experiments](#modern-dcr-kube-vs-legacy-dcr-experiments)
- [Prerequisites](#prerequisites)
- [Service Account Setup](#service-account-setup)
- [Stack Configuration](#stack-configuration)
  - [Production Stack File (dcr-kube1)](#production-stack-file-dcr-kube1)
  - [Service Account Key Path](#service-account-key-path)
  - [Configuration Keys](#configuration-keys)
- [Datastore and Volume Exclusions](#datastore-and-volume-exclusions)
  - [just gcp-cluster exclude-from-backup](#just-gcp-cluster-exclude-from-backup)
- [Deploy Behavior](#deploy-behavior)
  - [just gcp-cluster setup-cluster-backup](#just-gcp-cluster-setup-cluster-backup)
- [Legacy dcr-experiments Setup](#legacy-dcr-experiments-setup)
  - [Legacy Service Accounts](#legacy-service-accounts)
  - [Legacy Stack File (Pulumi.experiments.yaml)](#legacy-stack-file-pulumiexperimentsyaml)
  - [Legacy Deployment Commands](#legacy-deployment-commands)
- [Day-2 Operations and Restore](#day-2-operations-and-restore)
- [Verification](#verification)
  - [just gcp-cluster verify-cluster-backup](#just-gcp-cluster-verify-cluster-backup)
- [Teardown](#teardown)
- [Troubleshooting](#troubleshooting)

---

## What the Project Creates

The `install-velero/` project creates one GCS bucket and three local command resources in strict dependency order:

| Order | Resource | Name | Details |
|-------|----------|------|---------|
| 1 | GCS Bucket | `<properties.bucket>` (for example, `velero-k8s-backup-dcr-kube1`) | Location `US`. Object versions are enabled. Lifecycle rule deletes `ARCHIVED` objects when 2 newer versions exist. 40-day soft-delete retention (`3456000` seconds). **`ForceDestroy: true`**. |
| 2 | Velero Install | `velero-install` | Runs `velero install --provider <provider> --plugins <plugins> --bucket <bucket> --secret-file <serviceAccountJson> --namespace <namespace> --wait`. Depends on the GCS bucket. |
| 3 | Velero Schedule | `velero-schedule` | Runs `velero schedule create <schedule.name> --schedule="<schedule.run>" --ttl <schedule.ttl> --exclude-resources cronjobs.batch,jobs.batch`. Depends on `velero-install`. |
| 4 | Immediate Backup | `velero-immediate-backup` | Runs `velero backup create <schedule.name>-initial --from-schedule=<schedule.name>`. Depends on `velero-schedule`. |

The project creates **no Namespace**. The `namespace-bootstrap` stack owns the `operators` namespace and the application namespace (`prod` on `dcr-kube` clusters, `dev` on `dcr-experiments`). Refer to [shared namespaces](../pulumi/namespaces.md).

---

## Modern dcr-kube vs Legacy dcr-experiments

The table below compares the modern `dcr-kube` (`dcr-kube1`) cluster backup setup with the legacy `dcr-experiments` setup:

| Property | `dcr-kube1` (Modern Production) | `dcr-experiments` (Legacy Lab) |
|----------|---------------------------------|--------------------------------|
| Environment shell | `just cluster-env --env prod --cluster dcr-kube1` | `just cluster-env --env dev --cluster dcr-experiments` |
| Pulumi stack (`$PULUMI_STACK`) | `dcr-kube1` | `experiments` |
| Velero namespace | `operators` | `operators` |
| Application namespace | `prod` | `dev` |
| Velero CLI version (`.tool-versions`) | `1.18.2` | `1.14.0` |
| GCP plugin image | `velero/velero-plugin-for-gcp:v1.14.4` | `velero/velero-plugin-for-gcp:v1.7.0` |
| GCS backup bucket | `velero-k8s-backup-dcr-kube1` | `velero-k8s-backup-dcr-experiments` |
| Schedule name | `k8s-dcr-kube1` | `k8s-dcr-experiments` |
| Service account recipe | `just gcp-sa create-sa --sa-name cluster-backup` | `just gcp-cluster sa-accounts-setup` |
| Service account key path | `credentials/dcr-kube1/cluster-backup.json` | `credentials/dcr-experiments-cluster-backup.json` (or `credentials/dcr-experiments-database-backup.json`) |
| ArangoDB resource labels | Declarative `velero.io/exclude-from-backup: "true"` in `arangodb-cluster` plus `custodian` | `arangodb-single` plus `custodian` on `dev` |

---

## Prerequisites

| Requirement | Check | Guide |
|-------------|-------|-------|
| Active `cluster-env` sub-shell | `echo "$PULUMI_STACK"` | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Velero CLI and `yq` installed | `just check-tools` | [Tool versions](../kops/tool-versions.md) |
| Shared namespaces (`operators` and `prod`) | `just gcp-pulumi apply-namespaces` | [Shared namespaces](../pulumi/namespaces.md) |
| Exported cluster `KUBECONFIG` | `kubectl get nodes` | [Cluster access](../kops/cluster-access.md) |

---

## Service Account Setup

Velero authenticates to GCP with a dedicated `cluster-backup` service account key mounted into Deployment `velero` from Secret `cloud-credentials`.

```bash
just gcp-sa create-sa --sa-name cluster-backup
```

### Behavior

1. Resolves `--project` from `$PROJECT_ID`.
2. Reads the IAM role list from `gcs-files/roles-permissions/cluster-backup-roles.txt`:
   - `roles/compute.admin` (creates and manages persistent disk snapshots)
   - `roles/storage.admin` (reads and writes backup archives in the GCS bucket)
   - `roles/iam.serviceAccountTokenCreator` (signs storage URLs for backup downloads and logs)
3. Creates or reuses `cluster-backup@${PROJECT_ID}.iam.gserviceaccount.com` with a describe-then-create probe.
4. Binds the three roles on the project and retries on transient IAM propagation delays.
5. Writes the JSON key to `credentials/${PROJECT_ID}/cluster-backup.json` when the file does not exist on disk.

### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--sa-name` / `-s` | Yes | `pulumi-manager` | Pass `cluster-backup` for Velero |
| `--project` / `-p` | No | `$PROJECT_ID` | From the `cluster-env` shell |
| `--roles-file` / `-r` | No | `gcs-files/roles-permissions/cluster-backup-roles.txt` | Resolved automatically from `--sa-name` |
| `--output-file` / `-o` | No | `credentials/${PROJECT_ID}/cluster-backup.json` | Project-scoped directory |

**Key creation is not idempotent in GCP.** If you delete the local file and re-run `create-sa` (or run `just gcp-sa create-sa-key`), GCP mints an additional key on the service account. List active keys with:

```bash
gcloud iam service-accounts keys list \
  --iam-account="cluster-backup@${PROJECT_ID}.iam.gserviceaccount.com" \
  --project="${PROJECT_ID}"
```

---

## Stack Configuration

The repository ships `install-velero/Pulumi.experiments.yaml` for the legacy cluster. Before you deploy Velero on a `dcr-kube` cluster, create `install-velero/Pulumi.${PULUMI_STACK}.yaml`.

### Production Stack File (`dcr-kube1`)

Create `install-velero/Pulumi.dcr-kube1.yaml` with these contents:

```yaml
config:
  gcp:project: dcr-kube1
  install-velero:properties:
    bucket: velero-k8s-backup-dcr-kube1
    namespace: operators
    plugins:
      - velero/velero-plugin-for-gcp:v1.14.4
    provider: gcp
    schedule:
      name: k8s-dcr-kube1
      run: '@every 8h'
      ttl: 720h
```

Then initialize the stack so Pulumi records the cluster KMS `secretsprovider` and `encryptedkey` in the file:

```bash
just gcp-pulumi ensure-stack --folder install-velero
```

### Service Account Key Path

Store `properties.serviceAccountJson` as a KMS-encrypted value:

```bash
just gcp-pulumi set-secret \
  --folder install-velero \
  --key properties.serviceAccountJson \
  --value "../credentials/${PROJECT_ID}/cluster-backup.json"
```

Pulumi runs inside the `install-velero/` directory during `preview` and `create-resource`. The relative path `../credentials/${PROJECT_ID}/cluster-backup.json` resolves to `<repo-root>/credentials/${PROJECT_ID}/cluster-backup.json`. Pulumi passes this path to `velero install --secret-file`. The key file must exist on the operator workstation whenever Pulumi creates or replaces `velero-install`.

### Configuration Keys

| Key | Type | Example (`dcr-kube1`) | Notes |
|-----|------|-----------------------|-------|
| `gcp:project` | string (plain text) | `dcr-kube1` | Pins the GCP project for bucket creation |
| `properties.bucket` | string (plain text) | `velero-k8s-backup-dcr-kube1` | Globally unique GCS bucket name |
| `properties.namespace` | string (plain text) | `operators` | Must remain plain text (`setup-cluster-backup` and `verify-cluster-backup` read it with `yq`) |
| `properties.plugins.0` | string (plain text) | `velero/velero-plugin-for-gcp:v1.14.4` | Use `v1.14.x` with Velero `1.18.2` (`v1.7.0` on legacy Velero `1.14.0`) |
| `properties.provider` | string (plain text) | `gcp` | Passed to `velero install --provider` |
| `properties.schedule.name` | string (plain text) | `k8s-dcr-kube1` | Must remain plain text (`verify-cluster-backup` reads it with `yq`). The initial backup is named `<schedule.name>-initial`. |
| `properties.schedule.run` | string (plain text) | `@every 8h` | Cron expression or `@every <duration>` |
| `properties.schedule.ttl` | string (plain text) | `720h` | Backup retention window (30 days) |
| `properties.serviceAccountJson` | string (KMS secret) | `../credentials/dcr-kube1/cluster-backup.json` | Path to the JSON key file relative to `install-velero/` |

---

## Datastore and Volume Exclusions

Stateful datastores in the cluster use dedicated backup or live-mirror paths. Velero volume snapshots must not back up these datastores a second time:

| Datastore | Dedicated Backup or Migration Mechanism | Velero Exclusion Mechanism |
|-----------|-----------------------------------------|----------------------------|
| ArangoDB (`arangodb-cluster`, `arangodb-single`) | `arangodb-backup` (`arangodump` + `restic` to GCS) | Declarative `velero.io/exclude-from-backup: "true"` label on `Secret` and `ArangoDeployment`, plus `custodian` labels on `app.kubernetes.io/name=kube-arangodb` resources and pod volume annotations |
| PostgreSQL (`CloudNativePG`) | Barman Cloud plugin (`ObjectStore` to GCS) and `dump-logical` / `restore-logical` | Pod volume exclusion annotations applied by `custodian` |
| Redis (`redis-standalone`) | `redis-backup` (`redis-cli --rdb` + `restic` to GCS) | Pod volume exclusion annotations applied by `custodian` |
| MinIO (`minio`) | `minio-backup` (`mc mirror` + `restic` to GCS) | Pod volume exclusion annotations applied by `custodian` |
| `jobs.batch`, `cronjobs.batch` | Transient batch workloads | `--exclude-resources cronjobs.batch,jobs.batch` on the Velero `Schedule` |

### `just gcp-cluster exclude-from-backup`

```bash
just gcp-cluster exclude-from-backup
```

#### Behavior

1. Verifies that `KUBECONFIG` is set in the environment.
2. Compiles `cmd/custodian/main.go` to `bin/custodian` and registers an `EXIT` trap to remove the binary when the command finishes.
3. Runs `./bin/custodian exclude-from-backup --namespace <namespace>`:
   - Queries the Kubernetes discovery API for all namespaced resource types that support `list` and `update`.
   - Lists objects in `<namespace>` that match `app.kubernetes.io/name=kube-arangodb`.
   - Sets label `velero.io/exclude-from-backup=true` on each matched resource.
4. Runs `./bin/custodian exclude-volumes-from-backup --namespace <namespace>`:
   - Lists all pods in `<namespace>`.
   - Collects every volume name in `spec.volumes[].name` on each pod.
   - Applies strategic merge patch `backup.velero.io/backup-volumes-excludes: "<vol1>,<vol2>,..."` to the pod annotations.

**Pod annotations do not survive pod recreation.** When a Deployment, StatefulSet, or operator recreates a pod in `prod`, run `just gcp-cluster exclude-from-backup` again to annotate the replacement pod.

#### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` / `-n` | No | `prod` | Pass `--namespace dev` on `dcr-experiments` |

---

## Deploy Behavior

### `just gcp-cluster setup-cluster-backup`

```bash
just gcp-cluster setup-cluster-backup
```

#### Behavior

1. Resolves the stack name from `--stack` or `$PULUMI_STACK`, and fails if neither is set.
2. Verifies that `KUBECONFIG` is set, `velero` is on `PATH`, and `install-velero/Pulumi.<stack>.yaml` exists on disk.
3. Reads `properties.namespace` from `install-velero/Pulumi.<stack>.yaml` (default `operators`) and runs `velero client config set namespace=<velero_ns>`. This client setting is required because `velero schedule create` and `velero backup create` read the namespace from the local Velero client configuration.
4. Runs `just gcp-cluster exclude-from-backup --namespace <namespace>`.
5. Runs `just gcp-pulumi ensure-stack --folder install-velero --stack <stack>`.
6. Runs `just gcp-pulumi preview --folder install-velero --stack <stack>`.
7. Runs `just gcp-pulumi create-resource --folder install-velero --stack <stack>`, which creates the GCS bucket, installs Velero into `operators`, creates Schedule `<schedule.name>`, and triggers Backup `<schedule.name>-initial`.

#### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Selects `install-velero/Pulumi.<stack>.yaml` |
| `--namespace` / `-n` | No | `prod` | Application namespace passed to `exclude-from-backup` (`dev` on `dcr-experiments`) |

---

## Legacy `dcr-experiments` Setup

The `dcr-experiments` lab cluster (`dictycr-dev-staging-dcr-experiments-k8s.local`, GCP project `dcr-experiments`, Pulumi stack `experiments`, application namespace `dev`) uses the initial Velero layout committed in this repository.

### Legacy Service Accounts

On `dcr-experiments`, operators provisioned service accounts in bulk with `just gcp-cluster sa-accounts-setup`:

```bash
just gcp-cluster sa-accounts-setup --project dcr-experiments
```

That recipe creates five service accounts (`cloud-manager`, `cluster-backup`, `database-backup`, `deploy-manager`, and `kops-cluster-creator`) and writes flat key files directly under `credentials/`:

- `credentials/dcr-experiments-cluster-backup.json` (roles from `gcs-files/roles-permissions/cluster-backup-roles.txt`)
- `credentials/dcr-experiments-database-backup.json` (roles from `gcs-files/roles-permissions/database-backup-roles.txt`)

The initial `install-velero/Pulumi.experiments.yaml` configuration set `serviceAccountJson` to `../credentials/dcr-experiments-database-backup.json` and encrypted that path with the `dcr-experiments` KMS key. On modern `dcr-kube` clusters, do not run `sa-accounts-setup`. Instead, create only `cluster-backup` with `just gcp-sa create-sa --sa-name cluster-backup`, which writes to the project-scoped path `credentials/${PROJECT_ID}/cluster-backup.json`.

### Legacy Stack File (`Pulumi.experiments.yaml`)

`install-velero/Pulumi.experiments.yaml` contains:

```yaml
secretsprovider: gcpkms://projects/dcr-experiments/locations/us-central1/keyRings/dictycr/cryptoKeys/pulumi
encryptedkey: CiQALAIOnAsCqCZmpBuEhun0Jw4of0FVuWzcRHo21tm1s5lsQNUSSQB2302FUAbqLWSgxFRR3hB2/TgKxsw3cYfG0VWXElh07GB3bQF5ASERja53eeizDQ9roB+kI8ZIEpfgzTlXVn7IKYLh+Vau2GI=
config:
  gcp:project: dcr-experiments
  install-velero:properties:
    bucket: velero-k8s-backup-dcr-experiments
    exclude:
      - jobs
      - cronjobs
    namespace: operators
    plugins:
      - velero/velero-plugin-for-gcp:v1.7.0
    provider: gcp
    schedule:
      name: k8s-dcr-experiments
      run: '@every 8h'
      ttl: 720h
    serviceAccountJson:
      secure: v1:LIFbbuogSVg5kNYT:6v/RcOjVGMqVZDsN+HbGCoAfmPFR1RZUxYZq9K4ofvVcT4tgiUiXJfIHa68tzO+5rQKmtLZmd2PdC35JWv5t817X
```

Note that `exclude: [jobs, cronjobs]` in `Pulumi.experiments.yaml` is informational only. The Pulumi program hardcodes `--exclude-resources cronjobs.batch,jobs.batch` when it creates the schedule.

### Legacy Deployment Commands

To run or reconcile the Velero setup on `dcr-experiments`, enter the `dcr-experiments` environment and pass `--namespace dev`:

```bash
just cluster-env --env dev --cluster dcr-experiments
just gcp-cluster setup-cluster-backup --stack experiments --namespace dev
just gcp-cluster verify-cluster-backup --stack experiments
```

---

## Day-2 Operations and Restore

Because `setup-cluster-backup` sets `velero client config set namespace=operators`, you can run `velero` CLI commands directly inside the `cluster-env` shell:

```bash
# List schedules and backups
velero schedule get
velero backup get

# Inspect a backup and view its logs
velero backup describe <backup-name> --details
velero backup logs <backup-name>

# Trigger an on-demand backup from the cluster schedule
velero backup create "<custom-backup-name>" --from-schedule="k8s-${CLUSTER_NAME}"

# Restore a backup (or a single namespace from a backup)
velero restore create --from-backup "<backup-name>"
velero restore get
velero restore describe "<restore-name>"
```

If you open a new machine without the local Velero client configuration, point the client at `operators` first:

```bash
velero client config set namespace=operators
```

---

## Verification

### `just gcp-cluster verify-cluster-backup`

Read-only verification recipe. Exits non-zero if any check fails.

```bash
just gcp-cluster verify-cluster-backup
```

#### Behavior

1. Resolves `--stack` from `$PULUMI_STACK` and reads `namespace` and `schedule.name` from `install-velero/Pulumi.<stack>.yaml`.
2. Waits up to `120s` for `deployment/velero` in `operators` to complete its rollout.
3. Reads `BackupStorageLocation` `default` in `operators` and requires `status.phase` to equal `Available`.
4. Verifies that `schedule.velero.io/<schedule.name>` and `backup.velero.io/<schedule.name>-initial` exist in `operators`.

#### Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Selects `install-velero/Pulumi.<stack>.yaml` |

---

## Teardown

**CAUTION: The GCS backup bucket sets `ForceDestroy: true`.** When you run `remove-resource`, Pulumi runs `velero schedule delete <schedule.name> --confirm`, runs `velero uninstall --namespace <namespace> --force`, and deletes the GCS bucket along with all stored backups. Only the 40-day GCS soft-delete policy can recover deleted bucket objects.

```bash
just gcp-pulumi remove-resource --folder install-velero
```

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `Error: no stack name` | Recipe run outside the `cluster-env` sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` or pass `--stack <name>` |
| `Error: Velero is not installed or not in the PATH` | Per-cluster tool manifest not installed | Run `just prepare-tools` inside `just cluster-env` |
| `Error: stack config missing: install-velero/Pulumi.<stack>.yaml` | Stack file not written yet | Write `install-velero/Pulumi.${PULUMI_STACK}.yaml` from [Production Stack File](#production-stack-file-dcr-kube1) |
| `open ../credentials/.../cluster-backup.json: no such file or directory` during `velero install` | Service account key file missing on disk, or `serviceAccountJson` path is wrong | Run `just gcp-sa create-sa --sa-name cluster-backup` and verify `properties.serviceAccountJson` is `../credentials/${PROJECT_ID}/cluster-backup.json` |
| `namespaces "velero" not found` when you run the `velero` CLI manually | Local Velero client namespace not set to `operators` | Run `velero client config set namespace=operators` |
| `BackupStorageLocation` `default` phase is `Unavailable` | Missing GCS permissions on `cluster-backup`, or plugin mismatch with Velero version | Check `kubectl -n operators logs deployment/velero`, verify `cluster-backup` has `roles/storage.admin`, and match `velero-plugin-for-gcp:v1.14.4` to Velero `1.18.2` |
| `velero-schedule` or `velero-immediate-backup` fails on re-create with `already exists` | CRDs remained in `operators` after a partial apply | Delete the stale `schedule.velero.io` or `backup.velero.io` resource in `operators`, or refresh the stack state before the next run |
