#!/usr/bin/env bash
# Contract tests for backend prerequisite checks:
#   - `just gcp-pulumi check-backend-prereqs` (composite gate)
#   - `just gcp-cluster verify-deployer-access` (deployer identity probe)
#
# Mocks `go run` on PATH (kubectl/kops/gcloud run from Go code, not shell).
# Both recipes must make zero mutations: every recorded mock call is checked.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

tmp="$(mktemp -d)"
mock_bin="${tmp}/bin"
mkdir -p "${mock_bin}"
calls="${tmp}/calls.log"
: > "${calls}"
cleanup() { rm -rf "${tmp}"; }
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

# --- mock go run ---
cat > "${mock_bin}/go" <<'EOF'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
case "${MOCK_GO_MODE:-ok}" in
    "fail-prereqs") exit 1 ;;
    "fail-deployer") [[ "$*" == *"deployer-access"* ]] && exit 1 ;;
esac
exit 0
EOF
chmod +x "${mock_bin}/go"
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

# --- fixtures ---
folder="${tmp}/modware-order"
mkdir -p "${folder}"
cfg="${folder}/Pulumi.dcr-kube1.yaml"
cat > "${cfg}" <<'YAML'
config:
  modware-order:properties:
    appName: order
    arangodbSecret:
      name: order
      passkey: password
      userkey: user
    port: 9250
YAML

sa_key="${tmp}/deployer.json"
cat > "${sa_key}" <<'JSON'
{"project_id": "dcr-kube1", "type": "service_account"}
JSON

echo "=== 1. gate green: go run invoked with correct flags ==="
export MOCK_GO_MODE=ok
export PULUMI_STACK=dcr-kube1
: > "${calls}"
out=$(just gcp-pulumi check-backend-prereqs --folder "${folder}") || fail "green gate failed: ${out}"
grep -q "go run ./cmd/cluster-ops preflight backend-prereqs --folder ${folder} --stack dcr-kube1 --arango-service arangodb" "${calls}" \
    || fail "go run not called with expected flags:\n$(cat "${calls}")"
echo "  green: PASS"

echo "=== 2. gate failure propagates exit code ==="
export MOCK_GO_MODE=fail-prereqs
if out=$(just gcp-pulumi check-backend-prereqs --folder "${folder}" 2>&1); then
    fail "fail-prereqs mode did not propagate exit 1"
fi
echo "  exit propagation: PASS"

echo "=== 3. verify-deployer-access calls deployer-access subcommand ==="
export MOCK_GO_MODE=ok
: > "${calls}"
out=$(just gcp-cluster verify-deployer-access --cluster dcr-kube1 --sa-key "${sa_key}") || fail "deployer happy path failed"
grep -q "go run ./cmd/cluster-ops preflight deployer-access --cluster dcr-kube1 --sa-key ${sa_key}" "${calls}" \
    || fail "deployer-access not invoked:\n$(cat "${calls}")"
echo "  deployer-access: PASS"

echo "backend prerequisite contract tests PASSED"