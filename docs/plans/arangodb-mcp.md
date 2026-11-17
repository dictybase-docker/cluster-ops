# ArangoDB MCP server for Pi — design and implementation plan

## Goal

Pi (inside nono) queries the cluster ArangoDB through the
`arangodb/mcp-arangodb` image as a read-only user. Docker and port
listeners stay outside nono. Pi connects only to `localhost:18529` (JWT
login) and `localhost:8530` (MCP endpoint).

## Verified image facts (Docker Hub README, 2026-11-16)

| Variable | Value used |
|---|---|
| `ARANGO_HOSTS` | `http://host.docker.internal:8529` (the `forward hold` port on the host) |
| `ARANGO_USERNAME` / `ARANGO_PASSWORD` | `mcp-reader` credentials (server connection and `/health` only) |
| `ARANGO_DEFAULT_DB_NAME` | Required. Default `annotation` |
| `ARANGO_VERIFY_SSL` | `false` (plain `http://`) |
| `MCP_TRANSPORT` | `http` |
| `MCP_REQUIRE_AUTH` | `true` (HTTP mode does not start in Docker without it) |
| `FASTMCP_PORT` | `8080` in the container, published as `127.0.0.1:8530` |
| `MCP_ALLOWED_HOSTS` | Default `localhost:*,127.0.0.1:*` |

- MCP endpoint: `/`. Health: `/health` (200 when ArangoDB is reachable).
- Each request needs `Authorization: Bearer <JWT>` from `POST /_open/auth`.
  Queries run as the token user. Tokens expire after about one hour.

## Recipes (`just_modules/arangodb.justfile`)

### Task 1 — `create-mcp-reader --reader-password <pw> [--namespace prod] [--stack <name>]`

Option B: Pulumi owns the password and the Secret, like `backend`.

1. DONE. No new Go code. The `create-arangodb-databases` program already
   reads `grant` and `createDatabases` from config, so two new stacks of
   that project hold the reader: `experiments-mcp-reader` (`dev`) and
   `dcr-kube1-mcp-reader` (`prod`). Config:
   `arangodbSecret.name=arangodb-mcp`, `grant=ro`,
   `createDatabases=false`. The recipe sets user and password as secrets.
   Known side effect: both stacks label Jobs `app=arangodb-create-databases`,
   so `_refresh-before-job` also deletes the old completed `backend` Job.
   The `backend` recipes refresh first, so their state stays correct.
2. Preflight (no mutation): `KUBECONFIG` readable, namespace exists,
   Secret `arangodb-pass` has `password`, database list not empty,
   reader password differs from root and app passwords.
3. Mutation: `ensure-stack` → `config set-all --secret` (password, user)
   → `preview` → `create-resource` → `_wait-job`.
4. Verify: log in as `mcp-reader` through `/_open/auth` in a
   `cluster-ops forward run` child. Prove a write fails with 403.
5. Tests: `main_test.go` asserts `ro` grant and the Secret name.

### Task 2 — `mcp-up [--namespace prod] [--database annotation]`

1. Preflight: Docker reachable, ports 8529 and 8530 free, Secret
   `arangodb-mcp` present.
2. Start `cluster-ops forward hold --local-port 18529` in the background.
   Write the PID to `${XDG_STATE_HOME:-$HOME/.local/state}/cluster-ops/arangodb-mcp/forward.pid`.
3. Write reader credentials to `.../arangodb-mcp/reader.env` (mode 0600).
4. `docker run -d --name arangodb-mcp -p 127.0.0.1:8530:8080` with the
   variables above. Pass the password through `--env-file`, not argv.
5. Poll `/health` with backoff. On failure, run the `mcp-down` body and exit 1.

### Task 3 — `mcp-down` and `mcp-status`

- `mcp-down`: `docker rm -f arangodb-mcp`, kill the forward PID, delete
  the state directory. Idempotent.
- `mcp-status`: forward PID alive, container state, `/health` code.

### Task 4 — `mcp-configure-pi`

Add the `arangodb` server to `~/.pi/agent/mcp.json` with `jq` (merge,
keep other servers):

```json
{
  "url": "http://localhost:8530/",
  "headers": {
    "Authorization": "!sh -c '. \"${XDG_STATE_HOME:-$HOME/.local/state}/cluster-ops/arangodb-mcp/reader.env\" && curl -fsS -X POST http://localhost:8529/_open/auth -d \"{\\\"username\\\":\\\"$ARANGO_USERNAME\\\",\\\"password\\\":\\\"$ARANGO_PASSWORD\\\"}\" | jq -r \"\\\"Bearer \\\" + .jwt\"'"
  },
  "description": "Read-only AQL queries against the cluster ArangoDB"
}
```

Open item: nono must allow Pi to read `reader.env`. Check with
`nono why --self --path <file> --op read`. If it is denied, add the
path to a profile draft.

### Task 5 — Docs and checks

- `docs/reference/arangodb/mcp.md` (STE, per `docs/STYLE.md`).
- One Quick Reference row in `docs/arangodb-deploy.md` with a link.
- `just check` passes. `just --fmt --check` passes.

## Manual test (outside nono)

1. `just arangodb create-mcp-reader --namespace dev` on the lab cluster.
2. `just arangodb mcp-up --namespace dev`, then `mcp-status`.
3. In Pi: `pi mcp list` shows `arangodb` with four tools. Run
   `fetch-schemas`. After about one hour, reconnect with `/mcp`.
4. `just arangodb mcp-down`.
