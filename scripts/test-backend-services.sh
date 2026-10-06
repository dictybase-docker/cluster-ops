#!/usr/bin/env bash
# Contract tests for the backend services orchestration surface:
#   - `just ci deploy-backend-services` (aggregate over config/services.yaml)
#   - `just gcp-pulumi bootstrap-service` idempotent skip + auto tag
#   - `just ci latest-tag` (mixed v-prefix/bare semver)
#   - `just ci create-deploy-key` (standard credentials/<cluster>/ path, reuse)
#
# Nested `just` calls are intercepted (recorded); gh/gcloud/kubectl mocks
# carry simple state via env vars. Asserts: per-service call order, skip
# paths on re-run, services filter, repos list for set-deploy-secret,
# tag resolution (1.2.1 beats v0.1.0), key reuse, and the standard folder.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

tmp="$(mktemp -d)"
mock_bin="${tmp}/bin"
mkdir -p "${mock_bin}"
calls="${tmp}/calls.log"
: > "${calls}"
cleanup() { rm -rf "${tmp}"; rm -rf "${FAKE_SERVICES_DIR}"; }
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

# --- mocks ------------------------------------------------------------------
# Nested just: record; simulate side effects so re-run skip logic is real.
cat > "${mock_bin}/just" <<'EOF'
#!/usr/bin/env bash
echo "just $*" >> "${MOCK_CALLS_LOG}"
case "$1 $2" in
    "gcp-pulumi scaffold-backend-stack")
        folder=""; stack=""
        while [ $# -gt 0 ]; do case "$1" in
            --folder) folder="$2"; shift;;
            --stack) stack="$2"; shift;;
        esac; shift; done
        printf 'secretsprovider: mock\nencryptedkey: mock\nconfig:\n  %s:properties:\n    appName: x\n    namespace: prod\n    port: 1\n' "$(basename "${folder}")" > "${folder}/Pulumi.${stack}.yaml"
        ;;
    "gcp-pulumi bootstrap-service") : ;;
    "ci render-tag-deploy")
        out=""; while [ $# -gt 0 ]; do case "$1" in --out) out="$2"; shift;; esac; shift; done
        mkdir -p "$(dirname "${out}")"; echo "rendered" > "${out}"
        ;;
esac
exit 0
EOF
# gh: tags list + variables + secret visibility
cat > "${mock_bin}/gh" <<'EOF'
#!/usr/bin/env bash
echo "gh $*" >> "${MOCK_CALLS_LOG}"
case "$1 $2" in
    "api repos/dictyBase/modware-annotation"*) printf 'v0.1.0\n0.9.0\n' ;;
    "api repos/dictyBase/modware-order"*) printf '1.2.1\nv0.1.0\n1.1.0\n' ;;
    "api repos/dictyBase/modware-stock"*) printf '1.1.0\nv1.1.0\n' ;;
    "org list") echo "dictyBase" ;;
    "repo view") echo '{"id": "R_1"}' ;;
    "variable list") echo "PULUMI_STATE_STORAGE	gs://b" ;;
    "secret set") exit 0 ;;
    "variable set") exit 0 ;;
    "api orgs") echo '{"repositories": [{"name": "modware-order"}, {"name": "modware-stock"}, {"name": "modware-annotation"}]}' ;;
esac
exit 0
EOF
# kubectl: deployment state via flag file
cat > "${mock_bin}/kubectl" <<'EOF'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "$*" in
    *"get deploy/order-api-server"*)
        if [ "${MOCK_DEPLOY_PRESENT:-0}" = "1" ]; then
            [ "$*" = "get -n prod deploy/order-api-server" ] || true
            echo -n "dictybase/modware-order:v1.2.1"
        else
            exit 1
        fi ;;
    "rollout status deploy/order-api-server") echo "ok" ;;
esac
exit 0
EOF
chmod +x "${mock_bin}/just" "${mock_bin}/gh" "${mock_bin}/kubectl"
export MOCK_CALLS_LOG="${calls}"
REAL_JUST="$(command -v just)"
export PATH="${mock_bin}:${PATH}"

# Test-scoped cluster: unique stack name so the aggregate scaffolds fresh
# stack files (the real repo already carries dcr-kube1 stack files for
# order/annotation — those must never be touched). Everything the run
# creates is removed on exit.
cluster="svc-agg-$$"
stack="${cluster}"
entry="config/clusters/${cluster}.yaml"
created_files=""
cleanup() {
    rm -rf "${tmp}"
    rm -f "${entry}" credentials/${cluster}/deployer.json
    rmdir credentials/${cluster} 2>/dev/null || true
    for f in modware-order/Pulumi.${stack}.yaml modware-annotation/Pulumi.${stack}.yaml; do
        [ -f "${f}" ] && [ "${f}" != *dcr-kube1* ] && rm -f "${f}"
    done
    mv "${tmp}/services.yaml.bak" config/services.yaml 2>/dev/null || true
}
trap cleanup EXIT

cp config/services.yaml "${tmp}/services.yaml.bak"
cat > config/services.yaml <<'YAML'
services:
  - app: order
    folder: modware-order
    port: 9250
  - app: annotation
    folder: modware-annotation
    port: 9250
YAML

cat > "${entry}" <<YAML
cluster: ${cluster}
stack: ${stack}
kops_state: gs://kops-state-${cluster}
gcp_project: proj-${cluster}
kms_secrets_provider: gcpkms://projects/proj-${cluster}/locations/us-central1/keyRings/test/cryptoKeys/pulumi
pulumi_state: gs://pulumi-state-${cluster}
namespace: prod
ci_env: TEST
kops_version: 1.36.1
kubectl_version: 1.35.8
pulumi_version: 3.255.0
YAML

# Deployer key fixture at the standard path for the test cluster.
mkdir -p "credentials/${cluster}"
printf '{"project_id":"proj-%s","type":"service_account"}' "${cluster}" > "credentials/${cluster}/deployer.json"

line_of() { grep -n -e "$1" "${calls}" | head -n1 | cut -d: -f1; }
count_of() { grep -c -e "$1" "${calls}" || true; }

echo "=== 1. aggregate: per-service order, all services, secret once ==="
: > "${calls}"
out=$("${REAL_JUST}" ci deploy-backend-services --stack "${stack}") \
    || fail "aggregate failed:\n${out}"
for svc in modware-order modware-annotation; do
    grep -q "scaffold-backend-stack --stack "${stack}" --folder ${svc} --port" "${calls}" \
        || fail "no scaffold for ${svc}"
    grep -q "check-backend-prereqs --stack "${stack}" --folder ${svc}" "${calls}" \
        || fail "no gate for ${svc}"
    grep -q "bootstrap-service --stack "${stack}" --folder ${svc}" "${calls}" \
        || fail "no bootstrap for ${svc}"
    grep -q "sync-deploy-vars --cluster "${cluster}" --repos dictyBase/${svc}" "${calls}" \
        || fail "no vars for ${svc}"
    grep -q "render-tag-deploy --stack "${stack}" --app .* --project ${svc}" "${calls}" \
        || fail "no render for ${svc}"
done
# ports from services.yaml (order 9250, annotation 9250) in scaffold calls
grep -q "scaffold-backend-stack --stack "${stack}" --folder modware-order --port 9250" "${calls}" \
    || fail "order port wrong"
# per-service order: scaffold before gate before bootstrap for order
o_s=$(line_of "scaffold-backend-stack --stack "${stack}" --folder modware-order")
o_g=$(line_of "check-backend-prereqs --stack "${stack}" --folder modware-order")
o_b=$(line_of "bootstrap-service --stack "${stack}" --folder modware-order")
[ "${o_s}" -lt "${o_g}" ] && [ "${o_g}" -lt "${o_b}" ] || fail "per-service order broken"
# secret visibility once, with both repos
[ "$(count_of 'set-deploy-secret')" = "1" ] || fail "set-deploy-secret not called exactly once"
grep -q -- "--repos dictyBase/modware-order,dictyBase/modware-annotation" "${calls}" \
    || fail "repos list wrong"
# gate called with service flag
grep -q -- "--arango-service arangodb" "${calls}" || fail "gate missing arango service"
# bootstrap without tag → nested latest-tag resolution happens inside the real
# recipe; with mocked just the nested latest-tag is recorded instead
echo "  aggregate loop: PASS (2 services × 5 steps, secret once, ports from manifest)"

echo "=== 2. aggregate re-run: scaffold skipped, render re-run, no new failure ==="
: > "${calls}"
out=$("${REAL_JUST}" ci deploy-backend-services --stack "${stack}") \
    || fail "second run failed (not idempotent):\n${out}"
[ "$(count_of 'scaffold-backend-stack')" = "0" ] \
    || fail "scaffold ran again on existing stack files"
printf '%s' "${out}" | grep -q "stack config present, skipping scaffold" \
    || fail "skip message missing"
echo "  re-run: PASS (scaffold skipped, everything else converged)"

echo "=== 3. services filter ==="
: > "${calls}"
out=$("${REAL_JUST}" ci deploy-backend-services --stack "${stack}" --services order) \
    || fail "filtered run failed:\n${out}"
grep -q "folder modware-order" "${calls}" || fail "filtered run missed order"
if grep -q "folder modware-annotation" "${calls}"; then fail "filter leaked annotation"; fi
[ "$(count_of 'set-deploy-secret')" = "1" ] || fail "filtered secret call wrong"
grep -q -- "--repos dictyBase/modware-order" "${calls}" || fail "filtered repos wrong"
echo "  filter: PASS (order only, single repo secret)"

echo "=== 4. missing deployer key fails with the create hint ==="
mv credentials/${cluster}/deployer.json "${tmp}/key.bak"
if out=$("${REAL_JUST}" ci deploy-backend-services --stack "${stack}" 2>&1); then
    fail "missing key accepted"
fi
printf '%s' "${out}" | grep -q "just ci create-deploy-key --cluster "${cluster}"" \
    || fail "missing-key hint wrong:\n${out}"
mv "${tmp}/key.bak" credentials/${cluster}/deployer.json
echo "  missing key: PASS"

echo "=== 5. bootstrap-service idempotent skip (real recipe, mocked kubectl) ==="
: > "${calls}"
export MOCK_DEPLOY_PRESENT=1
out=$("${REAL_JUST}" gcp-pulumi bootstrap-service --stack dcr-kube1 \
    --folder modware-order --image-tag v1.2.1) || fail "skip path failed:\n${out}"
unset MOCK_DEPLOY_PRESENT
printf '%s' "${out}" | grep -q "verifying rollout only" || fail "skip message missing:\n${out}"
if grep -q "create-resource\|set-config\|preview" "${calls}"; then
    fail "skip path mutated: $(cat "${calls}")"
fi
grep -q "rollout status deploy/order-api-server" "${calls}" || fail "skip path did not verify rollout"
echo "  skip path: PASS (verify only, zero deploy calls)"

echo "=== 6. latest-tag: highest semver, mixed prefixes ==="
t=$("${REAL_JUST}" ci latest-tag --repo dictyBase/modware-order)
[ "${t}" = "1.2.1" ] || fail "order highest tag = ${t}, want 1.2.1"
t=$("${REAL_JUST}" ci latest-tag --repo dictyBase/modware-annotation)
[ "${t}" = "0.9.0" ] || fail "annotation highest tag = ${t}, want 0.9.0 (highest semver wins)"
t=$("${REAL_JUST}" ci latest-tag --repo dictyBase/modware-stock)
[ "${t}" = "v1.1.0" ] || fail "stock highest tie = ${t}, want v1.1.0 (verbatim on tie)"
echo "  tag resolution: PASS (1.2.1 > v0.1.0 ordering, verbatim output)"

echo "=== 7. create-deploy-key: reuse + standard folder ==="
mkdir -p credentials/dcr-kube1
printf '{"project_id":"dcr-kube1","type":"service_account"}' > credentials/dcr-kube1/deployer.json
out=$(CLUSTER_NAME=dcr-kube1 "${REAL_JUST}" ci create-deploy-key) \
    || fail "create-deploy-key via CLUSTER_NAME failed:\n${out}"
printf '%s' "${out}" | grep -q "Reusing existing key: credentials/dcr-kube1/deployer.json" \
    || fail "reuse path wrong:\n${out}"
rm -f credentials/dcr-kube1/deployer.json
echo "  key reuse: PASS (standard credentials/<cluster>/ path)"

echo "backend services orchestration contract tests PASSED"