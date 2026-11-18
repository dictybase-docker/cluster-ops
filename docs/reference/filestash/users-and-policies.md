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
| Secret `filestash-users` | `<role>AccessKey` and `<role>SecretKey` for the four roles. |
| Operator password store | The intake, public, and operator keys for staff login. |
| `share.sql` on the state PVC | An encrypted copy of the session key in each link. Protect the PVC and its backups as secret data. |
| Git | Only Secret names and key names. |

## Bucket Policy

`just filestash publish-bucket` sets `filestash/policies/downloads-anonymous.json` on `downloads`: `s3:GetObject` on `arn:aws:s3:::downloads/*` for every principal. `just filestash unpublish-bucket` sets the bucket to private again. `verify` compares the live policy with the file.

## just filestash create-buckets

Runs `mc mb --ignore-existing` for both buckets and `mc anonymous set none` on `uploads`, with the root key from Secret `minio-root`. A second run changes nothing.

## just filestash create-users

### Behavior

1. Reuses the keys in Secret `filestash-users` when it exists. Otherwise it makes four random 40-character secret keys and creates the Secret from a mode-600 file.
2. Opens a port-forward to Service `minio`. The keys go to the child process in the environment only.
3. For each role: `mc admin policy create`, `mc admin user add` with the keys on standard input, and `mc admin policy attach` when the policy is not attached.

A second run makes no new keys and attaches nothing again.

### Flags

| Flag | Required | Default |
|---|---|---|
| `--stack` | No | `$PULUMI_STACK` |
| `--namespace` | No | `properties.namespace` |

## Key Rotation

A new secret key for a user stops every link that staff made with the old key. Rotate one user at a time. Then make the valid links again and send the new URLs. To rotate, delete Secret `filestash-users` only after you record the old keys, then run `create-users`.
