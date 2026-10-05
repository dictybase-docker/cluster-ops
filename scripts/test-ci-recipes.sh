#!/usr/bin/env bash
# Contract tests for the `just ci` recipes: check-deploy-credentials,
# set-deploy-secret, sync-deploy-vars.
#
# gh and gcloud are mocked on PATH; nested `just` calls are intercepted the
# same way. Asserts: var names derive from the registry ci_env (never
# hardcoded), a missing registry entry fails, repo names validate, the secret
# upsert carries --visibility selected, and a gh failure propagates with the
# repo named.
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
cat > "${mock_bin}/gcloud" <<'EOF'
#!/usr/bin/env bash
echo "gcloud $*" >> "${MOCK_CALLS_LOG}"
case "$1" in
    projects) printf 'roles/storage.objectViewer\nroles/cloudkms.cryptoKeyEncrypterDecrypter\nroles/iam.serviceAccountUser\n' ;;
    kms) cat ;;
    *) exit 0 ;;
esac
EOF
cat > "${mock_bin}/kubectl" <<'EOF'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "$*" in
    "auth can-i update deployments -n prod") echo "yes" ;;
    "get pods -n prod -l app=arangodb"*) echo -n "arangodb-0" ;;
esac
exit 0
EOF
cat > "${mock_bin}/kops" <<'EOF'
#!/usr/bin/env bash
echo "kops $*" >> "${MOCK_CALLS_LOG}"
case "$1" in
    export) : > "$(echo "$*" | grep -o '\-\-kubeconfig [^ ]*' | cut -d' ' -f2)" ;;
esac
exit 0
EOF
# Nested just calls from check-deploy-credentials (verify-deployer-access):
# record and succeed.
cat > "${mock_bin}/just" <<'EOF'
#!/usr/bin/env bash
echo "just $*" >> "${MOCK_CALLS_LOG}"
exit 0
EOF
chmod +x "${mock_bin}/gh" "${mock_bin}/gcloud" "${mock_bin}/kubectl" "${mock_bin}/kops" "${mock_bin}/just"
export MOCK_CALLS_LOG="${calls}"
REAL_JUST="$(command -v just)"
export PATH="${mock_bin}:${PATH}"

sa_key="${tmp}/deployer.json"
cat > "${sa_key}" <<'JSON'
{"project_id": "dcr-kube1", "client_email": "deployer@dcr-kube1.iam.gserviceaccount.com", "type": "service_account"}
JSON

# deployer-roles.txt: one role the gcloud mock always returns.
mkdir -p gcs-files/roles-permissions
roles_file="gcs-files/roles-permissions/deployer-roles.txt"
had_roles=false
if [ -f "${roles_file}" ]; then
    had_roles=true
else
    printf 'roles/storage.objectViewer\n' > "${roles_file}"
fi
restore_roles() {
    if [ "${had_roles}" = "false" ] && [ -f "${roles_file}" ]; then
        rm -f "${roles_file}"
    fi
}
trap 'cleanup; restore_roles' EXIT

line_of() { grep -n -e "$1" "${calls}" | head -n1 | cut -d: -f1; }
last_of() { grep -n -e "$1" "${calls}" | tail -n1 | cut -d: -f1; }

echo "=== 1. check-deploy-credentials: wrong-project key refused ==="
bad_key="${tmp}/bad.json"
printf '{"project_id": "dcr-experiments", "type": "service_account"}' > "${bad_key}"
if out=$("${REAL_JUST}" ci check-deploy-credentials --cluster dcr-kube1 --sa-key "${bad_key}" 2>&1); then
    fail "wrong-project key accepted: ${out}"
fi
printf '%s' "${out}" | grep -q "does not match registry gcp_project" || fail "wrong-project failure unnamed:\n${out}"
echo "  wrong-project: PASS"

echo "=== 2. check-deploy-credentials: happy path probes roles ==="
: > "${calls}"
out=$("${REAL_JUST}" ci check-deploy-credentials --cluster dcr-kube1 --sa-key "${sa_key}") \
    || fail "happy path failed: ${out}"
printf '%s' "${out}" | grep -q "All deployer roles present" || fail "roles line missing:\n${out}"
grep -q "just gcp-cluster verify-deployer-access" "${calls}" || fail "deployer probe not delegated"
grep -q "gcloud projects get-iam-policy" "${calls}" || fail "iam policy not probed"
echo "  happy path: PASS (delegates verify-deployer-access + role probe)"

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
