#!/usr/bin/env bash
# Contract tests for the datastore recipes that wrap cluster-ops:
#   - `just <arangodb|minio|postgres|redis> check-pool` → `cluster-ops preflight pool`
#   - `just minio import-bucket` → `cluster-ops forward run -- bash -c '<mc …>'`
#
# go is mocked on PATH. For `forward run` the mock behaves like the real
# command: it exports FORWARD_PORT/FORWARD_ADDR and RUNS the child after `--`,
# so variables the child needs must really reach it. kubectl, mc and sleep are
# mocked too. Go-side behaviour (pool checks, reconnects) is covered by Go tests.
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
if [[ "$*" == *" forward run "* ]]; then
    while [[ $# -gt 0 && "$1" != "--" ]]; do shift; done
    shift
    export FORWARD_PORT=45678 FORWARD_ADDR=127.0.0.1:45678
    exec "$@"
fi
exit "${MOCK_GO_EXIT:-0}"
MOCK
cat > "${mock_bin}/kubectl" <<'MOCK'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "$*" in
    *"{.data.rootUser}"*) printf '%s' "${MOCK_ROOT_USER:-rootuser}" | base64 ;;
    *"{.data.rootPassword}"*) printf '%s' "${MOCK_ROOT_PASS:-rootpass}" | base64 ;;
esac
MOCK
cat > "${mock_bin}/mc" <<'MOCK'
#!/usr/bin/env bash
echo "mc $* MC_HOST_minio_dst=${MC_HOST_minio_dst:-unset} MC_CONFIG_DIR=${MC_CONFIG_DIR:-unset}" >> "${MOCK_CALLS_LOG}"
[[ "$1" == "mirror" && "${MOCK_MC_MIRROR:-ok}" == "fail" ]] && exit 1
exit 0
MOCK
printf '#!/usr/bin/env bash\nexit 0\n' > "${mock_bin}/sleep"
chmod +x "${mock_bin}"/*
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

echo "=== 1. check-pool passes its flags to the Go pool check ==="
for module in arangodb minio postgres redis; do
    : > "${calls}"
    out=$(just "${module}" check-pool 2>&1) || fail "${module} check-pool failed: ${out}"
    grep -q -- "preflight pool --pool database --count 3" "${calls}" \
        || fail "${module}: defaults not passed: $(cat "${calls}")"
    printf '%s' "${out}" | grep -q "All pool checks passed" || fail "${module}: no success line: ${out}"
done
grep -q -- "--require-amd64" <(: > "${calls}"; just arangodb check-pool >/dev/null 2>&1; cat "${calls}") \
    || fail "arangodb check-pool must require amd64"
: > "${calls}"
just redis check-pool --pool cache --node-count 2 >/dev/null 2>&1 || fail "redis check-pool with flags failed"
grep -q -- "preflight pool --pool cache --count 2" "${calls}" || fail "flags not passed: $(cat "${calls}")"
echo "  flag pass-through: PASS"

echo "=== 2. check-pool propagates a failing pool check ==="
for module in arangodb minio postgres redis; do
    if MOCK_GO_EXIT=1 just "${module}" check-pool >/dev/null 2>&1; then
        fail "${module} check-pool exited 0 although the pool check failed"
    fi
done
echo "  exit propagated: PASS"

import_bucket() {
    just minio import-bucket --bucket media --source-url https://src.example \
        --source-user srcuser --source-password srcpass "$@"
}

echo "=== 3. import-bucket mirrors through the forward port ==="
: > "${calls}"
out=$(import_bucket 2>&1) || fail "import-bucket failed: ${out}"
[[ "$(grep -c '^mc mirror' "${calls}")" == "1" ]] || fail "expected one mirror: $(cat "${calls}")"
grep '^mc mirror' "${calls}" | grep -q 'MC_HOST_minio_dst=http://rootuser:rootpass@localhost:45678' \
    || fail "target alias must use the forward port: $(grep '^mc mirror' "${calls}")"
echo "  target alias + mirror: PASS"

echo "=== 4. import-bucket retries the mirror --retries times, then fails ==="
: > "${calls}"
if out=$(MOCK_MC_MIRROR=fail import_bucket --retries 3 2>&1); then
    fail "import-bucket exited 0 although every mirror failed"
fi
[[ "$(grep -c '^mc mirror' "${calls}")" == "3" ]] || fail "expected 3 mirror attempts: $(grep -c '^mc mirror' "${calls}")"
printf '%s' "${out}" | grep -q "after 3 attempts" || fail "failure must name the attempts: ${out}"
echo "  retries: PASS"

echo "=== 5. user values reach the shell quoted ({{ quote() }} rendering) ==="
: > "${calls}"
out=$(just minio import-bucket --bucket 'we"ird$(echo INJ)' --secret 'sec"ret' \
    --source-url https://src.example --source-user srcuser --source-password srcpass 2>&1) \
    || fail "import-bucket broke on shell metacharacters in flags: ${out}"
grep -qF 'minio_dst/we"ird$(echo INJ)' "${calls}" \
    || fail "bucket must reach mc literally: $(cat "${calls}")"
grep -qF 'sec"ret' "${calls}" \
    || fail "secret must reach kubectl literally: $(cat "${calls}")"
echo "  quote() rendering: PASS"

echo "=== 6. source alias lives in a throwaway MC_CONFIG_DIR, removed after ==="
: > "${calls}"
out=$(import_bucket 2>&1) || fail "import-bucket failed: ${out}"
cfg_dir=$(grep -o 'MC_CONFIG_DIR=[^ ]*' "${calls}" | head -1 | cut -d= -f2)
[[ -n "${cfg_dir}" && "${cfg_dir}" != "unset" && "${cfg_dir}" != "${HOME}/.mc" ]] \
    || fail "source alias must not touch the default mc config: $(cat "${calls}")"
[[ ! -d "${cfg_dir}" ]] || fail "throwaway MC_CONFIG_DIR left behind: ${cfg_dir}"
echo "  throwaway config: PASS"

echo "=== 7. target URL percent-encodes the credentials ==="
: > "${calls}"
out=$(MOCK_ROOT_USER='ro@ot' MOCK_ROOT_PASS='p@ss/wo:rd#1%2' import_bucket 2>&1) \
    || fail "import-bucket failed: ${out}"
grep '^mc mirror' "${calls}" | grep -q 'MC_HOST_minio_dst=http://ro%40ot:p%40ss%2Fwo%3Ard%231%252@localhost:45678' \
    || fail "credentials must be URL-encoded: $(grep '^mc mirror' "${calls}")"
echo "  percent-encoding: PASS"

echo "=== 8. verify recipes delegate to Go preflight and exit cleanly ==="
for module in minio redis nats postgres arangodb; do
    : > "${calls}"
    out=$(just "${module}" verify 2>&1) || fail "${module} verify failed: ${out}"
    grep -q -- "preflight ${module}" "${calls}" \
        || fail "${module} verify did not call preflight ${module}: $(cat "${calls}")"
    if MOCK_GO_EXIT=1 just "${module}" verify >/dev/null 2>&1; then
        fail "${module} verify exited 0 although preflight failed"
    fi
done
echo "  verify recipes: PASS"

echo "=== 9. arangodb update-cluster reconciles without --root-password and waits on CR + pods + verify ==="
: > "${calls}"
PULUMI_STACK=dcr-kube1 just arangodb update-cluster --namespace prod >/dev/null 2>&1 \
    || fail "arangodb update-cluster failed"
grep -q -- "set-secret" "${calls}" && fail "update-cluster must not touch secret.password"
grep -q -- "stack ensure --folder arangodb-cluster --stack dcr-kube1" "${calls}" \
    || fail "update-cluster did not run ensure-stack: $(cat "${calls}")"
grep -q -- "stack preview --folder arangodb-cluster --stack dcr-kube1" "${calls}" \
    || fail "update-cluster did not run preview: $(cat "${calls}")"
grep -q -- "stack up --folder arangodb-cluster --stack dcr-kube1" "${calls}" \
    || fail "update-cluster did not run create-resource: $(cat "${calls}")"
grep -q -- "wait custom-resource --namespace prod --name arangodb" "${calls}" \
    || fail "update-cluster did not wait for ArangoDeployment Running: $(cat "${calls}")"
grep -q -- "wait pods-ready --namespace prod --selector arango_deployment=arangodb --count 9" "${calls}" \
    || fail "update-cluster did not wait for 9 ready pods: $(cat "${calls}")"
grep -q -- "preflight arangodb" "${calls}" \
    || fail "update-cluster did not run verify at the end: $(cat "${calls}")"
echo "  arangodb update-cluster: PASS"

echo "=== 10. arangodb collection-counts uses forward run and defaults to all 7 databases ==="
: > "${calls}"
curl_log="${tmp}/curl.log"
: > "${curl_log}"
cat > "${mock_bin}/curl" <<'MOCK'
#!/usr/bin/env bash
echo "curl $*" >> "${MOCK_CURL_LOG}"
case "$*" in
    *"/_api/collection?excludeSystem=true"*)
        printf '{"error":false,"code":200,"result":[{"name":"stock"},{"name":"cv"}]}'
        ;;
    *"/_api/collection/cv/count"*)
        printf '{"error":false,"code":200,"count":15}'
        ;;
    *"/_api/collection/stock/count"*)
        printf '{"error":false,"code":200,"count":29841}'
        ;;
esac
MOCK
chmod +x "${mock_bin}/curl"
export MOCK_CURL_LOG="${curl_log}"
# Also support .data.user / .data.password in mock kubectl
cat > "${mock_bin}/kubectl" <<'MOCK'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "$*" in
    *"{.data.rootUser}"*) printf '%s' "${MOCK_ROOT_USER:-rootuser}" | base64 ;;
    *"{.data.rootPassword}"*) printf '%s' "${MOCK_ROOT_PASS:-rootpass}" | base64 ;;
    *"{.data.user}"*) printf '%s' "appuser" | base64 ;;
    *"{.data.password}"*) printf '%s' "apppass" | base64 ;;
esac
MOCK
chmod +x "${mock_bin}/kubectl"

kubeconfig_tmp="${tmp}/kubeconfig.yaml"
printf 'apiVersion: v1\nkind: Config\n' > "${kubeconfig_tmp}"

out=$(KUBECONFIG="${kubeconfig_tmp}" just arangodb collection-counts 2>&1) \
    || fail "collection-counts default run failed: ${out}"
grep -q -- "forward run --kubeconfig ${kubeconfig_tmp} --ns prod --svc arangodb --remote-port 8529" "${calls}" \
    || fail "collection-counts did not invoke forward run: $(cat "${calls}")"
[ "$(grep -c '^=== Database:' <<< "${out}")" = "7" ] \
    || fail "expected 7 database sections, got:\n${out}"

: > "${calls}"
: > "${curl_log}"
out2=$(KUBECONFIG="${kubeconfig_tmp}" just arangodb collection-counts --databases stock --databases order 2>&1) \
    || fail "collection-counts filtered run failed: ${out2}"
[ "$(grep -c '^=== Database:' <<< "${out2}")" = "2" ] \
    || fail "expected 2 database sections, got:\n${out2}"
grep -q '^=== Database: stock ===' <<< "${out2}" || fail "stock missing"
grep -q '^=== Database: order ===' <<< "${out2}" || fail "order missing"
rm -f "${mock_bin}/curl"
echo "  arangodb collection-counts: PASS"

echo "datastore recipe contract tests PASSED"
