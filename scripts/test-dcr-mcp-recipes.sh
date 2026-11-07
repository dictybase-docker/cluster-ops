#!/usr/bin/env bash
# Contract tests for `just dcr-mcp check` and `just dcr-mcp verify`.
#
# kubectl is mocked on PATH: it records every call and answers from the STUB_*
# variables. yq runs for real against a throwaway stack file and registry entry.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

just_bin="$(command -v just)"
tmp_dir="$(mktemp -d)"
stack="dcr-mcp-contract-$$"
cfg_file="dcr-mcp/Pulumi.${stack}.yaml"
log_file="${tmp_dir}/calls.log"
trap 'rm -f "$cfg_file"; rm -rf "$tmp_dir"' EXIT
mkdir -p "${tmp_dir}/bin"

cat > "${tmp_dir}/bin/kubectl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"get namespace ${STUB_NS_MISSING:-none}"*) exit 1 ;;
    *"get namespace"*) exit 0 ;;
    *"rollout status deployment/${STUB_ROLLOUT_FAIL:-none}"*) exit 1 ;;
    *"rollout status"*) exit 0 ;;
    *"get deployment"*"jsonpath"*)
        if [[ "${STUB_WRONG_IMAGE:-0}" == "1" ]]; then
            printf 'dictybase/dcr-mcp:old-tag'
        else
            printf 'dictybase/dcr-mcp:v1.1.0'
        fi
        ;;
    *"get service ${STUB_SERVICE_MISSING:-none}"*) exit 1 ;;
    *"get service"*) exit 0 ;;
    *) echo "unexpected kubectl call: $*" >&2; exit 1 ;;
esac
MOCK
chmod +x "${tmp_dir}/bin/kubectl"

write_config() {
    local tag="${1:-v1.1.0}" ns="${2:-prod}"
    cat > "$cfg_file" <<YAML
config:
  dcr-mcp:properties:
    allowedOrigins:
      - "*"
    command: start-server
    host: 0.0.0.0
    image:
      name: dictybase/dcr-mcp
      tag: ${tag}
    ingress:
      enabled: false
    name: dcr-mcp
    namespace: ${ns}
    port: 8080
    rateLimit: 120
    stateless: true
YAML
}

run_recipe() {
    env "PATH=${tmp_dir}/bin:${PATH}" "PULUMI_STACK=${stack}" \
        "STUB_LOG=${log_file}" "$@"
}

expect_failure() {
    local expected="$1"
    shift
    local output status
    set +e
    output=$(run_recipe "$@" 2>&1)
    status=$?
    set -e
    if [[ "$status" -eq 0 || "$output" != *"$expected"* ]]; then
        printf 'expected failure containing %s, got status=%s output=%s\n' "$expected" "$status" "$output" >&2
        exit 1
    fi
}

echo "=== 1. just dcr-mcp check guards and happy path ==="
expect_failure 'no stack name' env -u PULUMI_STACK "$just_bin" dcr-mcp check
echo '  check no stack guard: PASS'

expect_failure "no registry entry with stack '${stack}'" "$just_bin" dcr-mcp check
echo '  check unregistered stack guard: PASS'

: > "$log_file"
out=$(env "PATH=${tmp_dir}/bin:${PATH}" "PULUMI_STACK=dcr-kube1" "STUB_LOG=${log_file}" "$just_bin" dcr-mcp check)
printf '%s' "$out" | rg -q "dcr-mcp prerequisites ready for stack 'dcr-kube1' in namespace 'prod'."
printf '%s' "$out" | rg -q 'Next: just gcp-pulumi scaffold-dcr-mcp-stack'
rg -q 'kubectl get namespace prod' "$log_file"
rg -q 'kubectl -n prod get service graphql-api' "$log_file"
rg -q 'kubectl -n prod rollout status deployment/graphql-api-server' "$log_file"
echo '  check happy path: PASS'

expect_failure "service/graphql-api not found" env "PULUMI_STACK=dcr-kube1" "STUB_SERVICE_MISSING=graphql-api" "$just_bin" dcr-mcp check
echo '  check missing graphql-api service guard: PASS'

echo "=== 2. just dcr-mcp verify guards and happy path ==="
expect_failure 'no stack name' env -u PULUMI_STACK "$just_bin" dcr-mcp verify
echo '  verify no stack guard: PASS'

rm -f "$cfg_file"
expect_failure 'stack config missing' "$just_bin" dcr-mcp verify
echo '  verify missing-config guard: PASS'

write_config bootstrap
expect_failure "tag is 'bootstrap'" "$just_bin" dcr-mcp verify
echo '  verify placeholder tag guard: PASS'

write_config v1.1.0 '<ns>'
expect_failure 'empty or placeholder namespace' "$just_bin" dcr-mcp verify
echo '  verify namespace placeholder guard: PASS'

write_config v1.1.0 prod
: > "$log_file"
out=$(run_recipe "$just_bin" dcr-mcp verify)
printf '%s' "$out" | rg -q 'PASS dcr-mcp: dictybase/dcr-mcp:v1.1.0'
printf '%s' "$out" | rg -q 'Next: just gateway verify'
rg -q 'kubectl -n prod rollout status deployment/dcr-mcp-api-server' "$log_file"
rg -q 'kubectl -n prod get service dcr-mcp-api' "$log_file"
echo '  verify success: PASS'

expect_failure 'did not roll out' env STUB_ROLLOUT_FAIL=dcr-mcp-api-server "$just_bin" dcr-mcp verify
echo '  verify rollout failure: PASS'

expect_failure 'running image dictybase/dcr-mcp:old-tag' env STUB_WRONG_IMAGE=1 "$just_bin" dcr-mcp verify
echo '  verify image mismatch: PASS'

expect_failure 'service/dcr-mcp-api not found' env STUB_SERVICE_MISSING=dcr-mcp-api "$just_bin" dcr-mcp verify
echo '  verify missing service: PASS'

echo "dcr-mcp recipe contract tests PASSED"
