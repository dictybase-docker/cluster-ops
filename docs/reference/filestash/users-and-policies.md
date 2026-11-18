# Filestash Users and Policies

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Buckets](#buckets)
- [MinIO Users](#minio-users)
- [Where Each Key Is](#where-each-key-is)
- [Bucket Policy](#bucket-policy)
- [just filestash create-buckets](#just-filestash-create-buckets)
- [just filestash create-users](#just-filestash-create-users)
- [Key Rotation](#key-rotation)

## Buckets

| Bucket | Access | Content |
|---|---|---|
| `uploads` | Private | One prefix for each collaborator, for example `alice/`. Files before review. |
| `downloads` | Anonymous `s3:GetObject` only | Published files under `<dataset>/`. |

The bucket names are part of each published URL. Do not change them.

## MinIO Users

The policy files are in `filestash/policies/`. No Filestash path uses the MinIO root key.

| User | Policy | Used for |
|---|---|---|
| `filestash-intake` | `PutObject` on `uploads/*`, `ListBucket` on `uploads` | Staff log in with it to make upload links. |
| `filestash-public` | `GetObject` on `downloads/*`, `ListBucket` on `downloads` | Staff log in with it to make the public link. |
| `filestash-operator` | `GetObject`, `DeleteObject` on `uploads/*`, `GetObject` on `downloads/*`, `ListBucket` on both | Review and delete uploads. No write to `downloads`. |
| `filestash-publisher` | `GetObject` on `uploads/*`, `GetObject`, `PutObject`, `DeleteObject` on `downloads/*`, `ListBucket` on both | The `publish` and `unpublish-file` recipes only. Never a Filestash login. |

A share link stores the session of the staff member who made it, with that MinIO key. The policy of that key is the upper limit of the link. Never make a link in a `filestash-operator` session.

**Do not add `s3:GetObject` to `filestash-intake`** without a security review and project approval. With that permission, the link flags become the only read control on `uploads`.

## Where Each Key Is

| Location | Content |
|---|---|
| `filestash/Pulumi.<stack>.yaml` | `users.<role>.accessKey` (plain) and `users.<role>.secretKey` (KMS-encrypted). The only copy: commit the file. |
| Secret `filestash-users` | Made by `deploy` from the stack values. |
| Operator password store | The intake, public, and operator keys for staff login. Read them with `pulumi -C filestash config get --path filestash:properties.users.<role>.secretKey`. |
| `share.sql` on the state PVC | An encrypted copy of the session key in each link. The restic snapshots hold it too. |

## Bucket Policy

`just filestash publish-bucket` sets `filestash/policies/downloads-anonymous.json` on `downloads`: `s3:GetObject` on `arn:aws:s3:::downloads/*` for every principal. `just filestash unpublish-bucket` sets the bucket to private again. `verify` compares the live policy with the file.

## just filestash create-buckets

Runs `mc mb --ignore-existing` for both buckets and `mc anonymous set none` on `uploads`, with the root key from Secret `minio-root`. A second run changes nothing.

## just filestash create-users

### Behavior

1. Lists the MinIO users with the root key from Secret `minio-root`.
2. For each role: keeps stored keys. When the stack has no keys for a role but MinIO user `filestash-<role>` exists, the recipe refuses: new keys stop every link, so restore the stack file from Git. Otherwise it stores `filestash-<role>` and a random 40-character secret key, encrypted.
3. Reads the keys back from the stack and opens a port-forward to Service `minio`. The keys go to the child process in the environment only.
4. For each role: `mc admin policy create`, `mc admin user add` with the keys on standard input, and `mc admin policy attach` when the policy is not attached.

`deploy` then makes Secret `filestash-users`. Commit the stack file after the run.

### Flags

| Flag | Required | Default |
|---|---|---|
| `--stack` | No | `$PULUMI_STACK` |
| `--namespace` | No | `properties.namespace` |

## Key Rotation

A new secret key for a user stops every link that staff made with the old key. Rotate one user at a time:

1. Record the links that the old key made (`just filestash audit-shares`).
2. Set a new value with `just gcp-pulumi set-secret --folder filestash --key properties.users.<role>.secretKey`.
3. Run `just filestash create-users`, then `just filestash deploy`.
4. Make the valid links again and send the new URLs.
