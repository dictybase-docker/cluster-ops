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
4. Checks every destination key with `mc stat`. If one exists, the recipe prints all existing keys and stops before the first copy.
5. Copies each object with a `Content-Type` from the file extension: `csv`, `tsv`, `txt`, `fa`, `fasta`, `gff`, `gff3`, `json`, `gz`, `zip`, `pdf`, `png`, `jpg`, `jpeg`, `mp4`. Other extensions get `application/octet-stream`.
6. Prints `https://<minio-host>/downloads/<dataset>/<key>` with each path segment percent-encoded.
7. Deletes the Lease only if this run holds it.

The URL host is a `storage.*` host of the Gateway `minio` route, else its first host, else the MinIO `apiIngress` hostname.

**The no-replace rule applies to this recipe only.** A person with the MinIO root key can still replace an object. MinIO conditional writes are not used, because `mc cp` cannot send `If-None-Match`.

### Flags

| Flag | Required | Default |
|---|---|---|
| `--source` | Yes | none |
| `--dataset` | Yes | none |
| `--url-host` | No | from the gateway or minio stack |
| `--stack`, `--namespace` | No | `$PULUMI_STACK`, `properties.namespace` |

## just filestash unpublish-file

Deletes one object from `downloads` as `filestash-publisher`, under the same Lease. Requires `--key <dataset>/<path>` and `--confirm yes`. The direct URL then returns 404.

## just filestash release-lock

After a crashed run, the Lease stays. `release-lock --holder <id>` deletes it only if its holder equals `<id>`. The failed run prints the holder. `--lease` selects `filestash-publish` (default) or `filestash-maintenance` (`backup-now` and the in-place restore). A Lease error other than `AlreadyExists` (for example missing RBAC) is printed as it is.
