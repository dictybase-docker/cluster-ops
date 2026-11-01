#!/usr/bin/env bash
# Contract tests for `just frontend verify`.
#
# kubectl is mocked on PATH: it records every call and answers from the STUB_*
# variables. yq runs for real against a throwaway stack file.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

just_bin="$(command -v just)"
tmp_dir="$(mktemp -d)"
stack="frontend-contract-$$"
cfg_file="frontend_application/Pulumi.${stack}.yaml"
log_file="${tmp_dir}/calls.log"
trap 'rm -f "$cfg_file"; rm -rf "$tmp_dir"' EXIT
mkdir -p "${tmp_dir}/bin"

cat > "${tmp_dir}/bin/kubectl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"rollout status deployment/${STUB_ROLLOUT_FAIL:-none}"*) exit 1 ;;
    *"rollout status"*) exit 0 ;;
    *"get deployment"*"jsonpath"*)
        all="$*"; rest="${all#*get deployment }"; app="${rest%% *}"
        if [[ "${app}" == "${STUB_WRONG_IMAGE:-none}" ]]; then
            printf 'dictybase/old:old-tag'
        else
            printf '%s' "${STUB_IMAGE_PREFIX}/${app%-api-server}:develop-abc1234"
        fi
        ;;
    *"get service ${STUB_SERVICE_MISSING:-none}"*) exit 1 ;;
    *"get service"*) exit 0 ;;
    *) echo "unexpected kubectl call: $*" >&2; exit 1 ;;
esac
MOCK
chmod +x "${tmp_dir}/bin/kubectl"

write_config() {
    local tag="${1:-develop-abc1234}" ns="${2:-prod}"
    cat > "$cfg_file" <<YAML
config:
  frontend_application:properties:
    apps:
      - image: img/frontpage
        name: frontpage
        tag: ${tag}
      - image: img/stockcenter
        name: stockcenter
        tag: develop-abc1234
    namespace: ${ns}
    port: 3000
YAML
}

run_recipe() {
    env "PATH=${tmp_dir}/bin:${PATH}" "PULUMI_STACK=${stack}" \
        "STUB_LOG=${log_file}" "STUB_IMAGE_PREFIX=img" "$@"
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

write_config
: > "$log_file"
out=$(run_recipe "$just_bin" frontend verify)
printf '%s' "$out" | rg -q 'PASS frontpage: img/frontpage:develop-abc1234'
printf '%s' "$out" | rg -q 'PASS stockcenter'
printf '%s' "$out" | rg -q 'Next: just gateway verify'
rg -q 'kubectl -n prod rollout status deployment/frontpage-api-server' "$log_file"
rg -q 'kubectl -n prod get service stockcenter-api' "$log_file"
echo 'verify success: PASS'

: > "$log_file"
run_recipe "$just_bin" frontend verify --namespace dev >/dev/null
rg -q 'kubectl -n dev rollout status' "$log_file"
echo 'namespace override: PASS'

expect_failure 'no stack name' env -u PULUMI_STACK "$just_bin" frontend verify
echo 'no stack guard: PASS'

rm -f "$cfg_file"
expect_failure 'stack config missing' "$just_bin" frontend verify
echo 'missing-config guard: PASS'

write_config bootstrap
expect_failure "placeholder 'bootstrap'" "$just_bin" frontend verify
echo 'placeholder tag guard: PASS'

write_config develop-abc1234 '<ns>'
expect_failure 'empty or placeholder namespace' "$just_bin" frontend verify
echo 'namespace placeholder guard: PASS'

write_config
expect_failure 'did not roll out' env STUB_ROLLOUT_FAIL=stockcenter-api-server "$just_bin" frontend verify
echo 'rollout failure: PASS'

expect_failure 'running image dictybase/old:old-tag' env STUB_WRONG_IMAGE=frontpage-api-server "$just_bin" frontend verify
echo 'image mismatch: PASS'

expect_failure 'service/stockcenter-api not found' env STUB_SERVICE_MISSING=stockcenter-api "$just_bin" frontend verify
echo 'missing service: PASS'

expect_failure '1 of 2 app(s) failed' env STUB_SERVICE_MISSING=stockcenter-api "$just_bin" frontend verify
echo 'failure count: PASS'

expect_failure 'timeout must look like' "$just_bin" frontend verify --timeout abc
echo 'timeout guard: PASS'

echo 'frontend recipe contract tests PASSED'
