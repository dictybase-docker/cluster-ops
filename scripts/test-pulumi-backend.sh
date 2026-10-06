#!/usr/bin/env bash
# Contract test for check-backend: invokes Go CLI, reads PULUMI_* env vars.
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

cat > "${mock_bin}/go" <<'EOF'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
exit "${MOCK_GO_EXIT:-0}"
EOF
chmod +x "${mock_bin}/go"
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

# Set minimal Pulumi env
export PULUMI_GCP_CREDENTIALS=/tmp/fake.json
export PULUMI_SECRET_PROVIDER=gcpkms://fake
export PULUMI_BACKEND_URL=gs://fake-bucket
export PULUMI_STACK=prod

echo "=== 1. check-backend invokes Go CLI ==="
: > "${calls}"
just gcp-pulumi check-backend >/dev/null || fail "check-backend failed"
grep -q "go run ./cmd/cluster-ops preflight pulumi-backend" "${calls}" \
    || fail "pulumi-backend not invoked: $(cat "${calls}")"
echo "  Go CLI invoked: PASS"

echo "=== 2. check-backend propagates a failing gate ==="
: > "${calls}"
if MOCK_GO_EXIT=1 just gcp-pulumi check-backend >/dev/null 2>&1; then
    fail "check-backend exited 0 although the preflight failed"
fi
echo "  exit propagated: PASS"

echo "=== 3. old script is gone ==="
[ ! -e scripts/pulumi/check-backend.sh ] || fail "scripts/pulumi/check-backend.sh still exists"
echo "  script removed: PASS"

echo "check-backend contract test PASSED"