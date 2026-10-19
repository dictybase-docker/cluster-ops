#!/usr/bin/env bash
# Contract tests for the `just ci` recipes: check-deploy-credentials,
# set-deploy-secret, sync-deploy-vars.
#
# check-deploy-credentials is a thin wrapper over
# `go run ./cmd/cluster-ops preflight deploy-credentials`; go is mocked on
# PATH (MOCK_GO_EXIT selects the gate result) and the probe behaviour —
# wrong-project key, IAM permissions batched via testIamPermissions with
# every missing permission listed — is covered by the Go unit tests in
# internal/preflight/backend. gh is mocked on PATH for the other recipes.
# Asserts: flag pass-through and exit propagation for the gate; var names
# derive from the registry ci_env (never hardcoded), a missing registry entry
# fails, repo names validate, the secret upsert carries --visibility
# selected, and a gh failure propagates with the repo named.
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

cat > "${mock_bin}/gh" <<'EOF'
#!/usr/bin/env bash
echo "gh $*" >> "${MOCK_CALLS_LOG}"
case "${MOCK_GH_MODE:-ok}:$1 $2" in
    "noauth:org list") exit 1 ;;
esac
case "$1 $2" in
    "org list") echo "dictyBase" ;;
    "repo view") echo '{"id": "R_1"}' ;;
    "variable list") echo "PULUMI_STATE_STORAGE	gs://org-bucket" ;;
    "secret set") exit 0 ;;
    "variable set") exit 0 ;;
    "api") echo '{"repositories": [{"name": "modware-order"}]}' ;;
esac
exit 0
EOF
cat > "${mock_bin}/go" <<'EOF'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
exit "${MOCK_GO_EXIT:-0}"
EOF
# Nested just calls: record and succeed.
cat > "${mock_bin}/just" <<'EOF'
#!/usr/bin/env bash
echo "just $*" >> "${MOCK_CALLS_LOG}"
exit 0
EOF
chmod +x "${mock_bin}/gh" "${mock_bin}/go" "${mock_bin}/just"
export MOCK_CALLS_LOG="${calls}"
REAL_JUST="$(command -v just)"
export PATH="${mock_bin}:${PATH}"

sa_key="${tmp}/deployer.json"
cat > "${sa_key}" <<'JSON'
{"project_id": "dcr-kube1", "client_email": "deployer@dcr-kube1.iam.gserviceaccount.com", "type": "service_account"}
JSON

line_of() { grep -n -e "$1" "${calls}" | head -n1 | cut -d: -f1; }
last_of() { grep -n -e "$1" "${calls}" | tail -n1 | cut -d: -f1; }

echo "=== 1. check-deploy-credentials: failing gate propagates ==="
: > "${calls}"
if out=$(MOCK_GO_EXIT=1 "${REAL_JUST}" ci check-deploy-credentials --cluster dcr-kube1 --sa-key "${sa_key}" 2>&1); then
    fail "failing preflight exited 0: ${out}"
fi
echo "  exit propagated: PASS"

echo "=== 2. check-deploy-credentials: flags reach the Go preflight ==="
: > "${calls}"
out=$("${REAL_JUST}" ci check-deploy-credentials --cluster dcr-kube1 --sa-key "${sa_key}") \
    || fail "happy path failed: ${out}"
grep -q -- "go run ./cmd/cluster-ops preflight deploy-credentials --cluster dcr-kube1 --sa-key ${sa_key}" "${calls}" \
    || fail "deploy-credentials not invoked with the flags: $(cat "${calls}")"
: > "${calls}"
CLUSTER_NAME=dcr-kube1 "${REAL_JUST}" ci check-deploy-credentials --sa-key "${sa_key}" >/dev/null \
    || fail "CLUSTER_NAME default failed"
grep -q -- "--cluster dcr-kube1" "${calls}" || fail "CLUSTER_NAME not used as the --cluster default: $(cat "${calls}")"
: > "${calls}"
CLUSTER_NAME=dcr-kube1 "${REAL_JUST}" ci check-deploy-credentials >/dev/null \
    || fail "default --sa-key failed"
grep -q -- "go run ./cmd/cluster-ops preflight deploy-credentials --cluster dcr-kube1 --sa-key credentials/dcr-kube1/deployer.json" "${calls}" \
    || fail "default --sa-key not resolved from cluster: $(cat "${calls}")"
echo "  happy path: PASS (flags + CLUSTER_NAME + default --sa-key)"

echo "=== 3. set-deploy-secret: upsert carries selected visibility ==="
: > "${calls}"
out=$("${REAL_JUST}" ci set-deploy-secret --cluster dcr-kube1 --sa-key "${sa_key}" --repos dictyBase/modware-order) \
    || fail "set-deploy-secret failed: ${out}"
grep -q -- "gh secret set PROD_DEPLOY_SA_KEY --org dictyBase --visibility selected" "${calls}" \
    || fail "upsert call wrong: $(grep 'secret set' "${calls}")"
printf '%s' "${out}" | grep -q "rm ${sa_key}" || fail "rm reminder missing"
echo "  upsert: PASS (org + selected visibility + rm reminder)"

echo "=== 4. set-deploy-secret: bad repo name and no-auth gh both fail ==="
if out=$("${REAL_JUST}" ci set-deploy-secret --cluster dcr-kube1 --sa-key "${sa_key}" --repos "bad name" 2>&1); then
    fail "bad repo name accepted"
fi
printf '%s' "${out}" | grep -q "not in owner/name form" || fail "bad-repo failure unnamed:\n${out}"
export MOCK_GH_MODE=noauth
: > "${calls}"
if out=$("${REAL_JUST}" ci set-deploy-secret --cluster dcr-kube1 --sa-key "${sa_key}" --repos dictyBase/modware-order 2>&1); then
    fail "no-auth gh accepted"
fi
printf '%s' "${out}" | grep -q "cannot access org dictyBase" || fail "no-auth failure unnamed:\n${out}"
unset MOCK_GH_MODE
echo "  guards: PASS (repo-name + gh auth)"

echo "=== 5. sync-deploy-vars: names derive from ci_env, values from registry ==="
: > "${calls}"
out=$("${REAL_JUST}" ci sync-deploy-vars --cluster dcr-kube1 --repos dictyBase/modware-order) \
    || fail "sync-deploy-vars failed: ${out}"
for var_val in \
    "PROD_CLUSTER=dcr-kube1" \
    "PROD_KOPS_STATE_STORAGE=gs://kops-state-dcr-kube1" \
    "PROD_KOPS_VERSION=1.36.1" \
    "PROD_KUBECTL_VERSION=1.35.8" \
    "PROD_PULUMI_VERSION=3.255.0"; do
    var="${var_val%%=*}"
    val="${var_val#*=}"
    grep -q -- "gh variable set ${var} --body ${val} --repo dictyBase/modware-order" "${calls}" \
        || fail "missing or wrong ${var}:\n$(grep 'variable set' "${calls}")"
done
printf '%s' "${out}" | grep -q "was:" || fail "before/after print missing"
echo "  variable derivation: PASS (5 PROD_* vars from registry)"

echo "=== 6. sync-deploy-vars: missing registry entry fails ==="
if out=$("${REAL_JUST}" ci sync-deploy-vars --cluster nope --repos dictyBase/modware-order 2>&1); then
    fail "missing registry accepted"
fi
printf '%s' "${out}" | grep -q "no registry entry for cluster 'nope'" || fail "registry failure unnamed:\n${out}"
echo "  missing registry: PASS"

echo "ci recipe contract tests PASSED"
