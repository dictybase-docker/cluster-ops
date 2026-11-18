# Filestash Publish Details

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [just filestash publish](#just-filestash-publish)
- [just filestash unpublish-file](#just-filestash-unpublish-file)
- [just filestash release-lock](#just-filestash-release-lock)

## just filestash publish

### What It Does

Copies every object under `uploads/<source>/` to `downloads/<dataset>/` as `filestash-publisher`, and prints the direct URL of each file.

### Behavior

1. Validates `--dataset` (`a-z 0-9 . _ -`) and `--source` (no `.`, `..`, or empty segment). The source is always a prefix.
2. Takes the Lease `filestash-publish` with `kubectl create`. If another run holds it, the recipe stops and prints the holder.
3. Lists the source objects. An empty source stops the run.
4. Checks every destination key with `mc stat`. If a key is in `downloads` already, the recipe prints each such key and stops before the first copy.
5. Copies each object with a `Content-Type` from the file extension: `csv`, `tsv`, `txt`, `fa`, `fasta`, `gff`, `gff3`, `json`, `gz`, `zip`, `pdf`, `png`, `jpg`, `jpeg`, `mp4`. Other extensions get `application/octet-stream`.
6. Prints `https://<minio-host>/downloads/<dataset>/<key>` with each path segment percent-encoded.
7. Deletes the Lease only if this run holds it.

The URL host is a `storage.*` host of the Gateway `minio` route, else its first host, else the MinIO `apiIngress` hostname.

**The no-replace rule applies to this recipe only.** A person with the MinIO root key can still replace an object. MinIO conditional writes are not used, because `mc cp` cannot send `If-None-Match`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--source` | Yes | none | A prefix in `uploads`, for example `alice/2026-batch`. |
| `--dataset` | Yes | none | A folder in `downloads`. `a-z 0-9 . _ -`, first a letter or digit. |
| `--url-host` | No | from the gateway or minio stack | The host in the printed URLs. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash unpublish-file

### What It Does

Deletes one object from `downloads` as `filestash-publisher`. The direct URL then returns 404.

### Behavior

1. Checks `--key`. It must have the form `<dataset>/<path>`, with no `.`, `..`, or empty segment.
2. Stops unless `--confirm yes` is given.
3. Takes Lease `filestash-publish`, the same lock as `publish`.
4. Deletes the object, then deletes the Lease.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--key` | Yes | none | `<dataset>/<path>` in `downloads`. |
| `--confirm` | Yes | none | Must be `yes`. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |

## just filestash release-lock

### What It Does

Deletes a Lease that a crashed run left behind. The failed run prints the holder.

### Behavior

1. Reads the holder of the Lease.
2. Deletes the Lease only if its holder equals `--holder`. Otherwise it stops and changes nothing.

`publish` and `unpublish-file` take `filestash-publish`. The in-place restore
takes `filestash-maintenance`. `backup-now` takes no Lease. When a run cannot
create its Lease for a reason other than `AlreadyExists`, it prints the
Kubernetes error unchanged. Missing RBAC is an example.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--holder` | Yes | none | The holder that the failed run printed. |
| `--lease` | No | `filestash-publish` | `filestash-publish` or `filestash-maintenance`. |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` | |
