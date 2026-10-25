# ArangoDB Content Storage URLs

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

This document explains why you must let `graphql-server` rewrite S3 storage origins on the fly instead of mutating the `content` database in ArangoDB, and documents the `inspect-content-urls` and `rewrite-content-urls` recipes.

## Table of Contents

- [Why You Must Not Rewrite the Database After Restore](#why-you-must-not-rewrite-the-database-after-restore)
- [Inspect Content URLs (`inspect-content-urls`)](#inspect-content-urls-inspect-content-urls)
- [Manual Database Rewrite (`rewrite-content-urls`)](#manual-database-rewrite-rewrite-content-urls)

---

## Why You Must Not Rewrite the Database After Restore

Lexical JSON documents in the `content` database (`serialized_json` collection) contain full S3 asset URLs (`https://<storage-host>/editor/assets/<date>/<uuid>`). When you restore a backup from `dcr-experiments` (`https://storage.dictybase.dev`) into `dcr-kube1` (`https://slimestorage.dictycr.org`), or restore a `dcr-kube1` backup back into `dcr-experiments`, those stored strings still carry the source cluster's hostname.

**Do not run `rewrite-content-urls` as part of a normal restore or bootstrap.** Let `graphql-server` rewrite the storage origin on the fly when it serves `Content.content` ([GraphQL storage endpoint](../graphql/deployment.md#storage-endpoint)).

| Reason | On-the-Fly Rewrite in `graphql-server` (Recommended) | In-Place Database Rewrite (`rewrite-content-urls`) |
|--------|------------------------------------------------------|----------------------------------------------------|
| **Post-restore manual work** | Zero manual steps. Backups work immediately in either direction (`dcr-experiments` to `dcr-kube1` or `dcr-kube1` to `dcr-experiments`). | Requires an operator to remember and run a mutation recipe after every restore or single-database import. |
| **Data safety** | Read-only. ArangoDB keeps the original `serialized_json` document untouched, so a bug cannot corrupt stored JSON. | Mutates production JSON strings in place across `serialized_json` with an AQL `SUBSTITUTE` query. |
| **Natural self-healing** | When an editor opens a page, `graphql-server` serves the current cluster's `--s3-storage-api` URL. When the editor saves the page, the updated URL is stored automatically. | Rewrites every document at once, even pages nobody edited. |
| **Single source of truth** | Uses `--s3-storage-api` (`properties.endpoints.store` in `graphql_server/Pulumi.<stack>.yaml`), which is already configured per cluster. | Duplicates the storage hostname as a manual CLI flag on a database script. |

Use `inspect-content-urls` whenever you want a read-only audit of the URLs stored in `serialized_json`. Keep `rewrite-content-urls` only as an emergency or offline migration utility when a consumer reads `serialized_json` directly from ArangoDB without going through `graphql-server`.

---

## Inspect Content URLs (`inspect-content-urls`)

Read-only scan of all user collections in `--database` (or one `--collection`). The recipe opens a supervised local port-forward (`cluster-ops forward run`) to the coordinator Service using the credentials from Secret `backend`, extracts every `http://` or `https://` URL matching `--pattern`, splits each URL into its `origin` (`https://<host>`) and `segment` (`/editor/assets/...`), and prints the summary and per-document details. It creates zero pods in the cluster.

```bash
# Scan the content database on prod for dev storage URLs
just arangodb inspect-content-urls --pattern storage.dictybase.dev

# Scan on dcr-experiments (--namespace dev) for prod storage URLs
just arangodb inspect-content-urls --namespace dev --pattern slimestorage.dictycr.org
```

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--pattern` / `-m` | No | `storage.dictybase.dev` | Substring to match inside extracted URLs |
| `--database` / `-d` | No | `content` | ArangoDB database name |
| `--collection` / `-l` | No | empty (all user collections) | Collection name (for example, `serialized_json`) |
| `--namespace` / `-n` | No | `prod` | Kubernetes namespace holding the Service and Secret |
| `--service` / `-s` | No | `arangodb` | Coordinator Service name |
| `--port` / `-p` | No | `8529` | Coordinator Service port |
| `--secret` / `-c` | No | `backend` | Application credentials Secret name |
| `--user-key` / `-u` | No | `user` | Secret data key for the username |
| `--pass-key` / `-k` | No | `password` | Secret data key for the password |

---

## Manual Database Rewrite (`rewrite-content-urls`)

**Optional. Not recommended when `graphql-server` normalizes storage URLs on the fly ([see above](#why-you-must-not-rewrite-the-database-after-restore)).**

Rewrites `--from-origin` to `--to-origin` inside `doc.content` across `--collection` (`serialized_json` by default) using AQL `SUBSTITUTE`. Defaults to `--dry-run yes`, which reports the number of matching documents without modifying ArangoDB. Pass `--dry-run no` to apply the update.

```bash
# 1. Dry-run preview on dcr-kube1 (prod): dev origin -> prod origin
just arangodb rewrite-content-urls \
  --from-origin https://storage.dictybase.dev \
  --to-origin https://slimestorage.dictycr.org

# 2. Apply on dcr-kube1 (prod)
just arangodb rewrite-content-urls \
  --from-origin https://storage.dictybase.dev \
  --to-origin https://slimestorage.dictycr.org \
  --dry-run no

# 3. Reverse direction when restoring a prod snapshot back to dcr-experiments
just arangodb rewrite-content-urls \
  --namespace dev \
  --from-origin https://slimestorage.dictycr.org \
  --to-origin https://storage.dictybase.dev \
  --dry-run no
```

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--from-origin` / `-f` | Yes | — | Source scheme and host (`https://<old-host>`). Trailing slash is stripped. Path segments are rejected. |
| `--to-origin` / `-t` | Yes | — | Target scheme and host (`https://<new-host>`). Must differ from `--from-origin`. |
| `--dry-run` | No | `yes` | `yes` previews matching document count only. `no` executes the AQL `UPDATE`. |
| `--database` / `-d` | No | `content` | ArangoDB database name |
| `--collection` / `-l` | No | `serialized_json` | Collection holding the `content` field |
| `--namespace` / `-n` | No | `prod` | Kubernetes namespace holding the Service and Secret |
| `--service` / `-s` | No | `arangodb` | Coordinator Service name |
| `--port` / `-p` | No | `8529` | Coordinator Service port |
| `--secret` / `-c` | No | `backend` | Application credentials Secret name |
| `--user-key` / `-u` | No | `user` | Secret data key for the username |
| `--pass-key` / `-k` | No | `password` | Secret data key for the password |
