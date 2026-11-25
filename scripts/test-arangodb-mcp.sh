#!/usr/bin/env bash
# Contract tests for the local ArangoDB MCP wiring: the state directory lives
# outside the repository, `mcp-configure-pi` installs the token script into
# ~/bin, and the token script works from any folder.
#
# HOME points at a throwaway directory. curl and docker are mocked on PATH.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

just_bin="$(command -v just)"
tmp_dir="$(mktemp -d)"
sleeper_pid=""
cleanup() {
    [[ -n "$sleeper_pid" ]] && kill "$sleeper_pid" 2>/dev/null || true
    rm -rf "$tmp_dir"
}
trap cleanup EXIT

fake_home="${tmp_dir}/home"
state="${fake_home}/.local/state/arangodb-mcp"
other_cwd="${tmp_dir}/elsewhere"
mkdir -p "${tmp_dir}/bin" "$fake_home/.pi/agent" "$other_cwd"

cat > "${tmp_dir}/bin/curl" <<'MOCK'
#!/usr/bin/env bash
cat >/dev/null
printf '{"jwt":"test-jwt"}'
MOCK
cat > "${tmp_dir}/bin/docker" <<'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
chmod +x "${tmp_dir}/bin/curl" "${tmp_dir}/bin/docker"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "  $1: PASS"; }
run_env() { env HOME="$fake_home" PATH="${tmp_dir}/bin:${PATH}" "$@"; }

# 1. No recipe keeps MCP state inside the repository.
if grep -nE '^\s*STATE="\$PWD/\.state/arangodb-mcp"' just_modules/arangodb.justfile; then
    fail "an MCP recipe still keeps state in the repository"
fi
grep -q 'STATE="$HOME/.local/state/arangodb-mcp"' scripts/arangodb-mcp-token.sh ||
    fail "token script does not read ~/.local/state/arangodb-mcp"
pass test_state_outside_repo

# 2. mcp-configure-pi installs the script, creates the state dir, keeps other servers.
echo '{"mcpServers":{"other":{"url":"http://x/"}}}' >"$fake_home/.pi/agent/mcp.json"
run_env "$just_bin" arangodb mcp-configure-pi >/dev/null
cmp -s scripts/arangodb-mcp-token.sh "$fake_home/bin/arangodb-mcp-token" ||
    fail "installed token script differs from the source"
[[ -x "$fake_home/bin/arangodb-mcp-token" ]] || fail "installed token script is not executable"
[[ -d "$state" ]] || fail "state directory was not created"
[[ "$(stat -f '%Lp' "$state" 2>/dev/null || stat -c '%a' "$state")" == 700 ]] ||
    fail "state directory mode is not 700"
header=$(jq -r '.mcpServers.arangodb.headers.Authorization' "$fake_home/.pi/agent/mcp.json")
[[ "$header" == "!${fake_home}/bin/arangodb-mcp-token" ]] || fail "unexpected Authorization header: $header"
[[ "$(jq -r '.mcpServers.other.url' "$fake_home/.pi/agent/mcp.json")" == "http://x/" ]] ||
    fail "other MCP servers were not kept"
pass test_configure_pi

# 3. The token script fails clearly when state is missing.
if out=$(cd "$other_cwd" && run_env "$fake_home/bin/arangodb-mcp-token" 2>&1); then
    fail "token script succeeded without state"
fi
[[ "$out" == *"MCP server state is missing"* ]] || fail "unexpected missing-state error: $out"
pass test_token_missing_state

# 4. The token script works from another folder with state only in ~/.local/state.
printf 'ARANGO_USERNAME=mcp-reader\nARANGO_PASSWORD=x\n' >"$state/reader.env"
echo 18529 >"$state/forward.port"
out=$(cd "$other_cwd" && run_env "$fake_home/bin/arangodb-mcp-token")
[[ "$out" == "Bearer test-jwt" ]] || fail "token script printed an unexpected value"
pass test_token_from_other_folder

# 5. mcp-down stops forwards, empties the state dir, keeps it, removes legacy state.
sleep 300 &
sleeper_pid=$!
disown "$sleeper_pid"
echo "$sleeper_pid" >"$state/forward.pid"
legacy="${other_cwd}/.state/arangodb-mcp"
mkdir -p "$legacy"
echo "stale" >"$legacy/reader.env"
(cd "$other_cwd" && run_env "$just_bin" --justfile "${repo_root}/justfile" arangodb mcp-down >/dev/null)
sleep 0.2
if kill -0 "$sleeper_pid" 2>/dev/null; then fail "forward process is still running"; fi
sleeper_pid=""
[[ -d "$state" ]] || fail "state directory was removed"
[[ -z "$(ls -A "$state")" ]] || fail "state directory is not empty"
[[ ! -e "$legacy" ]] || fail "legacy repository state was not removed"
pass test_mcp_down
