# Filestash Users and Policies

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Buckets](#buckets)
- [MinIO Users](#minio-users)
- [Where Each Key Is](#where-each-key-is)
- [Bucket Policy](#bucket-policy)
- [just filestash create-buckets](#just-filestash-create-buckets)
- [just filestash create-users](#just-filestash-create-users)
- [just filestash login-keys](#just-filestash-login-keys)
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
| Operator password store | The intake, public, and operator keys for staff login. Print them with [`just filestash login-keys`](#just-filestash-login-keys). |
| `share.sql` on the state PVC | An encrypted copy of the session key in each link. The restic snapshots hold it too. |

## Bucket Policy

`just filestash publish-bucket` sets `filestash/policies/downloads-anonymous.json` on `downloads`: `s3:GetObject` on `arn:aws:s3:::downloads/*` for every principal. `just filestash unpublish-bucket` sets the bucket to private again. `verify` compares the live policy with the file.

## just filestash create-buckets

### What It Does

Creates the buckets `uploads` and `downloads` and makes `uploads` private. A second run changes nothing.

### Behavior

1. Reads the root key from Secret `minio-root` and opens a port-forward to Service `minio`.
2. Runs `mc mb --ignore-existing` for `uploads` and `downloads`.
3. Runs `mc anonymous set none` on `uploads`.

The public read policy on `downloads` is a separate step: `just filestash publish-bucket`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack` | No | `$PULUMI_STACK` | |
| `--namespace` | No | `properties.namespace` | A value that differs from the stack file stops the recipe. |

## just filestash create-users

### What It Does

Stores a key pair for each of the four roles as encrypted stack values, then creates the MinIO users and their policies.

### Behavior

1. Lists the MinIO users with the root key from Secret `minio-root`.
2. For each role, keeps the stored keys when the stack has them.
3. When the stack has no keys for a role but MinIO user `filestash-<role>` exists, the recipe stops. New keys stop every link. Restore the stack file from Git in that case.
4. Otherwise it stores the access key `filestash-<role>` and a random 40-character secret key, encrypted.
5. Reads the keys back from the stack and opens a port-forward to Service `minio`. The keys go to the child process in the environment only.
6. For each role: `mc admin policy create`, `mc admin user add` with the keys on standard input, and `mc admin policy attach` when the policy is not attached.

`deploy` then makes Secret `filestash-users`. Commit the stack file after the run.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--stack` | No | `$PULUMI_STACK` | |
| `--namespace` | No | `properties.namespace` | A value that differs from the stack file stops the recipe. |

## just filestash login-keys

### What It Does

Prints the access key and the secret key of one staff login role. Staff paste them into the Filestash login form.

### Behavior

1. Accepts only the roles `intake`, `public`, and `operator`. The `publisher` keys are for the `publish` and `unpublish-file` recipes, and this recipe refuses them.
2. Reads the access key from the stack file and decrypts the secret key with the KMS provider.
3. Prints both keys to the terminal. The recipe changes nothing.

When the stack has no keys for the role, the recipe prints `run: just filestash create-users`.

**The output is a live credential.** Copy it into the operator password store, then clear the terminal. Do not paste it into a ticket or a chat.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--role` | Yes | none | `intake`, `public`, or `operator`. |
| `--stack` | No | `$PULUMI_STACK` | |
| `--namespace` | No | `properties.namespace` | A value that differs from the stack file stops the recipe. |

## Key Rotation

A new secret key for a user stops every link that staff made with the old key. Rotate one user at a time:

1. Record the links that the old key made (`just filestash audit-shares`).
2. Set a new value with `just gcp-pulumi set-secret --folder filestash --key properties.users.<role>.secretKey`.
3. Run `just filestash create-users`, then `just filestash deploy`.
4. Make the valid links again and send the new URLs.
