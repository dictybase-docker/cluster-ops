# Filestash Install Details

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Resources](#resources)
- [Stack Keys](#stack-keys)
- [State Disk Size](#state-disk-size)
- [just gcp-pulumi scaffold-filestash-stack](#just-gcp-pulumi-scaffold-filestash-stack)
- [just filestash create-secret](#just-filestash-create-secret)
- [just filestash check](#just-filestash-check)
- [just filestash deploy](#just-filestash-deploy)

## Resources

The stack creates these resources in `properties.namespace`. Each has the label `app.kubernetes.io/name=filestash`.

| Kind | Name | Notes |
|---|---|---|
| Deployment | `filestash` | One replica, strategy `Recreate`, user 1000. Init containers `plugins`, then `seed`. |
| Service | `filestash` | ClusterIP, port `8334`. |
| PersistentVolumeClaim | `filestash-state` | `/app/data/state`: `config.json`, SQLite files, plugins, logs, state copies. |
| ConfigMap | `filestash-seed` | First-start `config.json`. |
| CronJob | `filestash-state-backup` | Refer to [Backup and restore](backup-restore.md). |
| ServiceAccount, Role, RoleBinding | `filestash-state-backup` | The CronJob can scale `deployment/filestash`. |
| Ingress | `filestash`, `filestash-admin-block` | Only when `ingress.enabled` is `true`. |

The `seed` init container copies `filestash-seed` to `config/config.json` only when that file does not exist. After the first start, the admin console owns `config.json`. The seed has one S3 connection with pinned `type`, `label`, `endpoint`, and `region`, and no keys. A login form with a changed endpoint fails. The seed also sets `features.server.console_enable=false`, `features.search.explore_timeout=5`, and `log.telemetry=false`.

`/app/data/cache` is an `emptyDir` with the limit `cacheSizeLimit`.

## Stack Keys

Key prefix: `filestash:properties`.

| Key | Required | Default | Notes |
|---|---|---|---|
| `namespace` | Yes | none | From `config/clusters/<cluster>.yaml`. |
| `applicationURL` | Yes | none | `https://<filestash-host>`. Filestash builds share URLs from it. |
| `image.name` / `image.tag` / `image.digest` | tag, digest | `machines/filestash` | The vendor publishes only `latest`. The digest is the pin. |
| `name` | No | `filestash` | Validation rejects any other value: the recipes and manifests use the fixed names. |
| `secretName` | No | `filestash` | Keys `ADMIN_PASSWORD`, `CONFIG_SECRET`. Validation rejects any other value. |
| `storage.size` | Yes | none | At least `1Gi`. Refer to [State Disk Size](#state-disk-size). |
| `storage.storageClass` | No | `dictycr-balanced` | |
| `cacheSizeLimit` | No | `2Gi` | |
| `s3.endpoint` / `s3.region` / `s3.label` | endpoint | `us-east-1`, `dictycr-minio` | `http://minio.<namespace>.svc.cluster.local:9000`. |
| `ingress.enabled` | No | `false` | `true` only on a cluster with no Gateway stack. |
| `ingress.hosts` / `ingress.tlsSecret` / `ingress.label` | When enabled | class `nginx` | The kcert label `kcert.dev/ingress: managed` issues the certificate. |
| `plugins.image.name` / `.tag` / `.digest` | Yes | none | From `just filestash build-plugins`. |
| `backup.maxAgeHours` | Yes | none | Recovery point target in hours. |
| `backup.schedule` / `backup.keep` | No | `30 */4 * * *`, `7` | |
| `previousImage.*` | No | none | Written by `upgrade`, read by `rollback`. |

Validation reports `<...>` placeholders first, then invalid values. The error names each field.

## State Disk Size

The stack file sets `storage.size`. The code has no default. The values are `5Gi` on `dcr-kube1` and `2Gi` on `experiments`. MinIO holds the uploaded and published files, not this PVC. The PVC holds the plugin ZIP files (about 100 MB without `office`), the SQLite files, the logs, and 7 state copies. `verify` fails above 80% use. Grow the PVC with `just filestash resize-state --size <quantity>`. A PVC cannot shrink.

## just gcp-pulumi scaffold-filestash-stack

### What It Does

Writes `filestash/Pulumi.<stack>.yaml` from `config/templates/filestash-stack.yaml.tmpl` and the cluster registry, and initializes the stack with the registry KMS secrets provider.

### Behavior

1. Resolves the stack, and fails without `--state-size`.
2. Reads `namespace` and `kms_secrets_provider` from the registry entry with the same `stack`.
3. Keeps an existing file that has a `config:` block, and exits.
4. Sets `ingress.enabled: true` only when `gateway/Pulumi.<stack>.yaml` does not exist.
5. Runs `pulumi stack init` with the KMS provider.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--state-size` | Yes | none | `Mi`, `Gi`, or `Ti`. |
| `--host` | No | `<filestash-host>` | Sets `applicationURL` and `ingress.hosts`. |
| `--stack` | No | `$PULUMI_STACK` | |

## just filestash create-secret

### What It Does

Creates or updates Secret `filestash` with the label `app.kubernetes.io/name=filestash`.

### Behavior

1. Reads the admin password from a hidden prompt. It needs at least 12 characters.
2. Makes a bcrypt hash with `htpasswd -niBC 10`. Filestash rejects a plaintext `ADMIN_PASSWORD`.
3. Keeps an existing `CONFIG_SECRET`. Refuses when the Secret is missing but `pvc/filestash-state` exists: restore the Secret from Velero first. Otherwise it makes a random 32-character value.
4. Applies the Secret from a mode-600 temporary file. No value is in the process arguments.

**Do not change `CONFIG_SECRET`.** It encrypts the secret fields in `config.json`. A new value breaks them.

### Flags

| Flag | Required | Default |
|---|---|---|
| `--stack` | No | `$PULUMI_STACK` |
| `--namespace` | No | `properties.namespace` |

## just filestash check

Read-only preflight: the namespace, Secret `filestash` with both keys, Secret `minio-root`, and Service `minio`. `deploy` runs it first.

## just filestash deploy

### Behavior

1. Runs `check`.
2. Selects the stack, or initializes it from the committed file.
3. Runs `pulumi up` on `filestash/`. Validation stops a stack file with placeholders.
4. Waits for `deployment/filestash`.

### Flags

| Flag | Required | Default |
|---|---|---|
| `--timeout` | No | `300s` |
| `--stack` | No | `$PULUMI_STACK` |
| `--namespace` | No | `properties.namespace` |
