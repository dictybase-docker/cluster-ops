#!/usr/bin/env bash
# Contract tests for the backend prerequisite probes:
#   - `just gcp-pulumi check-backend-prereqs` (composite gate)
#   - `just gcp-cluster verify-deployer-access` (deployer identity probe)
#
# Cloud CLIs are mocked on PATH (kubectl, kops, gcloud, pulumi never runs).
# The mocks are stateless except for a mode env var, so each phase asserts
# one fail-closed cause. Both recipes must make zero mutations: every
# recorded mock call is checked against an allow-list.
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

# --- mocks -----------------------------------------------------------------
cat > "${mock_bin}/kubectl" <<'EOF'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "${MOCK_KUBECTL_MODE:-ok}:$1" in
    "no-ns:get") [[ "$*" == "get ns prod" ]] && exit 1 ;;
    "no-secret:get") [[ "$*" == "get secret order -n prod" ]] && exit 1 ;;
    "no-secret-key:get") [[ "$*" == *"jsonpath={.data.user}"* ]] && { echo -n ""; exit 0; } ;;
esac
case "$1 $2" in
    "port-forward") echo "Forwarding from 127.0.0.1:18529 -> 8529" >&2; sleep 30 ;;
esac
case "$*" in
    "get ns prod") echo "prod Active" ;;
    "get secret order -n prod") echo "order" ;;
    "get secret order -n prod -o"*) echo -n "dXNlcg==" ;;
    "auth can-i update deployments -n prod") echo "yes" ;;
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
cat > "${mock_bin}/gcloud" <<'EOF'
#!/usr/bin/env bash
echo "gcloud $*" >> "${MOCK_CALLS_LOG}"
case "$1" in
    kms) cat ;;
    *) exit 0 ;;
esac
EOF
cat > "${mock_bin}/nc" <<'EOF'
#!/usr/bin/env bash
echo "nc $*" >> "${MOCK_CALLS_LOG}"
exit 0
EOF
cat > "${mock_bin}/curl" <<'EOF'
#!/usr/bin/env bash
echo "curl $*" >> "${MOCK_CALLS_LOG}"
echo "${MOCK_CURL_CODE:-200}"
EOF
chmod +x "${mock_bin}/kubectl" "${mock_bin}/kops" "${mock_bin}/gcloud" "${mock_bin}/nc" "${mock_bin}/curl"
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

# --- fixtures --------------------------------------------------------------
folder="${tmp}/modware-order"
mkdir -p "${folder}"
cfg="${folder}/Pulumi.dcr-kube1.yaml"
cat > "${cfg}" <<'YAML'
secretsprovider: gcpkms://projects/dcr-kube1/locations/us-central1/keyRings/dcr-kube1/cryptoKeys/dcr-kube1
encryptedkey: mock
config:
  modware-order:properties:
    appName: order
    arangodbSecret:
      name: order
      passkey: password
      userkey: user
    command: start-server
    image:
      name: dictybase/modware-order
      tag: bootstrap
    namespace: prod
    port: 9250
YAML

sa_key="${tmp}/deployer.json"
cat > "${sa_key}" <<'JSON'
{"project_id": "dcr-kube1", "client_email": "deployer@dcr-kube1.iam.gserviceaccount.com", "type": "service_account"}
JSON

MUTATION_RE='^(create|delete|apply|patch|edit|run|scale|set env|rolling)'
assert_no_mutations() {
    if grep -Eq "${MUTATION_RE}" "${calls}"; then
        fail "mutation recorded: $(grep -E "${MUTATION_RE}" "${calls}")"
    fi
}

run_gate() { # run_gate [mode]
    export MOCK_KUBECTL_MODE="${1:-ok}"
    : > "${calls}"
    just gcp-pulumi check-backend-prereqs --stack dcr-kube1 --folder "${folder}"
}

echo "=== 1. gate green names every layer, no mutations ==="
out=$(run_gate ok) || fail "green gate failed: ${out}"
printf '%s' "${out}" | grep -q "All prerequisites green" || fail "missing green line"
grep -q "port-forward -n prod svc/arangodb" "${calls}" || fail "green run did not probe svc/arangodb"
grep -q -- "-u user:user" "${calls}" || fail "probe did not authenticate with the Secret credentials"
assert_no_mutations
echo "  green: PASS"

echo "=== 2. missing namespace fails naming it ==="
if out=$(run_gate no-ns 2>&1); then fail "no-ns mode accepted: ${out}"; fi
printf '%s' "${out}" | grep -q "MISSING: namespace prod" || fail "no-ns failure unnamed:\n${out}"
assert_no_mutations
echo "  missing namespace: PASS"

echo "=== 3. missing Secret fails naming it ==="
if out=$(run_gate no-secret 2>&1); then fail "no-secret mode accepted: ${out}"; fi
printf '%s' "${out}" | grep -q "MISSING: secret order in namespace prod" || fail "no-secret failure unnamed:\n${out}"
assert_no_mutations
echo "  missing secret: PASS"

echo "=== 4. absent database fails with 404 pointing at create-arangodb-databases ==="
export MOCK_CURL_CODE=404
if out=$(run_gate no-db 2>&1); then fail "no-db mode accepted: ${out}"; fi
printf '%s' "${out}" | grep -q "database order not found (404)" || fail "no-db failure unnamed:\n${out}"
assert_no_mutations
echo "  missing database: PASS (404 named)"

echo "=== 4b. rejected credentials fail with 401 ==="
export MOCK_CURL_CODE=401
if out=$(run_gate ok 2>&1); then fail "bad-creds mode accepted: ${out}"; fi
printf '%s' "${out}" | grep -q "credentials rejected for database order (401)" || fail "401 failure unnamed:\n${out}"
assert_no_mutations
echo "  rejected credentials: PASS (401 named)"
unset MOCK_CURL_CODE

echo "=== 5. verify-deployer-access happy path + wrong-project refusal ==="
: > "${calls}"
export MOCK_KUBECTL_MODE=ok
out=$(just gcp-cluster verify-deployer-access --cluster dcr-kube1 --sa-key "${sa_key}") \
    || fail "verify happy path failed:\n${out}"
printf '%s' "${out}" | grep -q "Deployer access verified" || fail "missing verified line"
assert_no_mutations
grep -q "kops export kubeconfig" "${calls}" || fail "kubeconfig export not exercised"

bad_key="${tmp}/bad.json"
printf '{"project_id": "dcr-experiments", "type": "service_account"}' > "${bad_key}"
if out=$(just gcp-cluster verify-deployer-access --cluster dcr-kube1 --sa-key "${bad_key}" 2>&1); then
    fail "wrong-project key accepted"
fi
printf '%s' "${out}" | grep -q "does not match registry gcp_project" || fail "wrong-project failure unnamed:\n${out}"
echo "  verify-deployer-access: PASS (happy + wrong project)"

echo "backend prerequisite contract tests PASSED"
