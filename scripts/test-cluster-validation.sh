#!/usr/bin/env bash
# Contract tests for the cluster-validation recipes that wrap cluster-ops:
#   - `just gcp-cluster verify-cluster` → `cluster-ops preflight verify-cluster`
#   - exit code propagates.
#
# go is mocked on PATH (argv recorder). Go-side behavior (kops classification,
# HA and hardening groups) is covered by Go unit tests.
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

cat > "${mock_bin}/go" <<'MOCK'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
exit "${MOCK_GO_EXIT:-0}"
MOCK
cat > "${mock_bin}/kops" <<'MOCK'
#!/usr/bin/env bash
echo "kops $*" >> "${MOCK_CALLS_LOG}"
exit 1
MOCK
chmod +x "${mock_bin}"/*
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

# A non-empty kubeconfig keeps the wrapper out of its export-kubeconfig fix-up.
export KUBECONFIG="${tmp}/kubeconfig.yaml"
printf 'apiVersion: v1\nkind: Config\n' > "${KUBECONFIG}"

echo "=== 1. verify-cluster runs the composite via cluster-ops ==="
: > "${calls}"
just gcp-cluster verify-cluster --cluster dcr-test-1 >/dev/null 2>&1 \
    || fail "verify-cluster failed"
grep -q "preflight verify-cluster" "${calls}" \
    || fail "verify-cluster must call cluster-ops: $(cat "${calls}")"
echo "  composite invocation: PASS"

echo "=== 2. verify-cluster propagates a failing check ==="
if MOCK_GO_EXIT=1 just gcp-cluster verify-cluster --cluster dcr-test-1 >/dev/null 2>&1; then
    fail "verify-cluster exited 0 although the check failed"
fi
echo "  exit propagated: PASS"

echo "=== 3. no-arg invocation resolves the cluster-shell environment ==="
: > "${calls}"
CLUSTER_NAME=env-cluster just gcp-cluster verify-cluster >/dev/null 2>&1 \
    || fail "verify-cluster with env only failed"
grep -q -- "--cluster env-cluster" "${calls}" \
    || fail "empty --cluster must resolve from CLUSTER_NAME: $(cat "${calls}")"
echo "  env fallback: PASS"

echo "=== 4. a failed kubeconfig export does not swallow the composite ==="
: > "${calls}"
if KUBECONFIG="" MOCK_GO_EXIT=1 just gcp-cluster verify-cluster --cluster dcr-test-1 >/dev/null 2>&1; then
    fail "verify-cluster passed although kops is broken"
fi
grep -q "kops export kubeconfig" "${calls}" \
    || fail "export-kubeconfig must be attempted: $(cat "${calls}")"
grep -q "preflight verify-cluster" "${calls}" \
    || fail "the composite must still run after a failed export: $(cat "${calls}")"
echo "  export is best-effort: PASS"

echo "cluster validation contract tests PASSED"
