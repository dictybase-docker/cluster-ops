# Filestash Install Details

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Resources](#resources)
- [Stack Keys](#stack-keys)
- [Stack Values to Set](#stack-values-to-set)
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
| PersistentVolumeClaim | `filestash-state` | `/app/data/state`: `config.json`, SQLite files, plugins, logs. Label `velero.io/exclude-from-backup=true`. |
| ConfigMap | `filestash-seed` | First-start `config.json`. |
| Secret | `filestash` | `ADMIN_PASSWORD`, `CONFIG_SECRET`, from the encrypted stack values. |
| Secret | `filestash-users` | `<role>AccessKey`, `<role>SecretKey` for the four roles, from the encrypted stack values. |
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
| `ingress.enabled` | No | `false` | Set `true` at the route step, only on a cluster with no Gateway stack. |
| `ingress.hosts` / `ingress.tlsSecret` / `ingress.label` | When enabled | class `nginx` | The kcert label `kcert.dev/ingress: managed` issues the certificate. |
| `plugins.image.name` / `.tag` / `.digest` | Yes | none | From `just filestash build-plugins`. |
| `backup.maxAgeHours` | Yes | none | Recovery point target in hours. `verify` fails when the newest successful snapshot is older. |
| `secrets.adminPasswordHash` / `secrets.configSecret` | Yes | none | Encrypted. Set by `create-secret`. |
| `users.<role>.accessKey` / `users.<role>.secretKey` | Yes | none | For `intake`, `public`, `operator`, `publisher`. The secret key is encrypted. Set by `create-users`. |
| `previousImage.*` | No | none | Written by `upgrade`, read by `rollback`. |

Validation reports `<...>` placeholders first, then invalid values. The error names each field.

## Stack Values to Set

These values have no default. Set them with
`just gcp-pulumi set-config --folder filestash --key properties.<key> --value <value>`.

| Key | Value | Notes |
|---|---|---|
| `applicationURL` | `https://<filestash-host>` | Filestash builds share URLs from it. |
| `ingress.hosts.0` | `<filestash-host>` | `experiments` only, at the route step, with `ingress.enabled=true`. |
| `backup.maxAgeHours` | Hours, for example `6` | Recovery point target. Larger than the 4-hour schedule. |
| `plugins.image.tag` / `plugins.image.digest` | From `build-plugins` | |

`create-secret` and `create-users` set the encrypted values. Never edit them by hand.

## State Disk Size

The stack file sets `storage.size`. The code has no default. The values are `5Gi` on `dcr-kube1` and `2Gi` on `experiments`. MinIO holds the uploaded and published files, not this PVC. The PVC holds the plugin ZIP files (about 100 MB without `office`), the SQLite files, and the logs. `verify` fails above 80% use. Grow the PVC with `just filestash resize-state --size <quantity>`. A PVC cannot shrink.

## just gcp-pulumi scaffold-filestash-stack

### What It Does

Writes `filestash/Pulumi.<stack>.yaml` and `filestash-backup/Pulumi.<stack>.yaml` from `config/templates/filestash-stack.yaml.tmpl`, `config/templates/filestash-backup-stack.yaml.tmpl`, and the cluster registry, and initializes both stacks with the registry KMS secrets provider.

### Behavior

1. Resolves the stack, and fails without `--state-size`.
2. Reads `namespace`, `kms_secrets_provider`, and `gcp_project` from the registry entry with the same `stack`.
3. Keeps each existing file that has a `config:` block.
4. Writes `ingress.enabled: false` on every cluster. It prints the public route mode: a Gateway route when `gateway/Pulumi.<stack>.yaml` exists, else the nginx Ingress, which the route step turns on.
5. Writes the backup bucket `restic-filestash-backup-<gcp_project>`.
6. Runs `pulumi stack init` with the KMS provider for each new file.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--state-size` | Yes | none | `Mi`, `Gi`, or `Ti`. |
| `--host` | No | `<filestash-host>` | Sets `applicationURL` and `ingress.hosts`. |
| `--stack` | No | `$PULUMI_STACK` | |

## just filestash create-secret

### What It Does

Stores `secrets.configSecret` and `secrets.adminPasswordHash` as KMS-encrypted values in `filestash/Pulumi.<stack>.yaml`. `deploy` makes Secret `filestash` from them.

### Behavior

1. Keeps an existing `secrets.configSecret`. When it is missing but `pvc/filestash-state` exists, the recipe refuses: restore the stack file from Git. Otherwise it stores a random 32-character value.
2. Reads the admin password from a hidden prompt. It needs at least 12 characters.
3. Makes a bcrypt hash with `htpasswd -niBC 10`. Filestash rejects a plaintext `ADMIN_PASSWORD`.
4. Stores the hash. Each value goes to Pulumi on standard input, never in the process arguments.

**Do not change `CONFIG_SECRET`.** It encrypts the secret fields in `config.json`. A new value breaks them. Commit the stack file after the run.

### Flags

| Flag | Required | Default |
|---|---|---|
| `--stack` | No | `$PULUMI_STACK` |
| `--namespace` | No | `properties.namespace` |

## just filestash check

Read-only preflight: the namespace, the encrypted stack values for both Secrets, Secret `minio-root`, and Service `minio`. `deploy` runs it first.

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
