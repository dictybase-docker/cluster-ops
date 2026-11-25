#!/usr/bin/env bash
# Print "Bearer <JWT>" for the mcp-reader user. Pi runs this as the
# Authorization header command of the `arangodb` MCP server.
# `just arangodb mcp-configure-pi` installs it as ~/bin/arangodb-mcp-token.
# Needs a running `just arangodb mcp-up`. Reads only the state directory
# below, so Pi needs no access to the cluster-ops repository.
set -euo pipefail
STATE="$HOME/.local/state/arangodb-mcp"
if [[ ! -r "$STATE/reader.env" || ! -r "$STATE/forward.port" ]]; then
    echo "Error: MCP server state is missing in $STATE — run 'just arangodb mcp-up' first." >&2
    exit 1
fi
set -a
# shellcheck source=/dev/null
. "$STATE/reader.env"
set +a
PORT=$(<"$STATE/forward.port")
jq -nc '{username: env.ARANGO_USERNAME, password: env.ARANGO_PASSWORD}' |
    curl -fsS -X POST "http://127.0.0.1:${PORT}/_open/auth" --data-binary @- |
    jq -er '"Bearer " + .jwt'
