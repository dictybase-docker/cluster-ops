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

echo "=== 5. check-cves passes when manifest and disks match latest upstream and invokes trivy ==="
cat > "${mock_bin}/gcloud" <<'MOCK'
#!/usr/bin/env bash
echo "gcloud $*" >> "${MOCK_CALLS_LOG}"
if [[ "$*" == *"describe-from-family"* ]]; then
    echo "${MOCK_LATEST_IMG:-ubuntu-2404-noble-amd64-v20261024}"
    exit 0
fi
if [[ "$*" == *"disks list"* ]]; then
    printf 'nodes-8h3b\tus-central1-b\t%s\n' "${MOCK_DISK_IMG:-ubuntu-2404-noble-amd64-v20261024}"
    exit 0
fi
exit 0
MOCK
cat > "${mock_bin}/trivy" <<'MOCK'
#!/usr/bin/env bash
echo "trivy $*" >> "${MOCK_CALLS_LOG}"
exit 0
MOCK
chmod +x "${mock_bin}/gcloud" "${mock_bin}/trivy"

: > "${calls}"
CLUSTER_NAME=dcr-kube1 PROJECT_ID=dcr-kube1 just gcp-cluster check-cves >/dev/null 2>&1 \
    || fail "no-arg check-cves failed in cluster-env shell"
grep -q "trivy k8s" "${calls}" \
    || fail "check-cves must invoke trivy k8s when trivy is on PATH: $(cat "${calls}")"
echo "  check-cves pass (cluster-env defaults) + trivy invocation: PASS"

echo "=== 6. check-cves resolves legacy bundle dir from PROJECT_ID/KUBECONFIG when CLUSTER_NAME is short ==="
: > "${calls}"
MOCK_LATEST_IMG="ubuntu-2204-jammy-v20261021" MOCK_DISK_IMG="ubuntu-2204-jammy-v20261021" \
    CLUSTER_NAME=dcr-experiments PROJECT_ID=dcr-experiments \
    just gcp-cluster check-cves >/dev/null 2>&1 \
    || fail "no-arg check-cves failed for dcr-experiments cluster-env"
echo "  check-cves dcr-experiments cluster-env resolution: PASS"

echo "=== 7. check-cves exits non-zero when a node boot disk lags behind latest upstream ==="
if MOCK_DISK_IMG="ubuntu-2404-noble-amd64-v20260906" CLUSTER_NAME=dcr-kube1 PROJECT_ID=dcr-kube1 just gcp-cluster check-cves --skip-trivy yes >/dev/null 2>&1; then
    fail "check-cves exited 0 although node disk lagged behind latest upstream image"
fi
echo "  check-cves lag detection: PASS"

echo "cluster validation contract tests PASSED"
