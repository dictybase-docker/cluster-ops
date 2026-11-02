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

expect_failure 'no stack name' env -u PULUMI_STACK "$just_bin" frontend pr-deploy-workflow
echo 'pr-deploy-workflow no stack guard: PASS'

expect_failure "no registry entry with stack '${stack}'" "$just_bin" frontend pr-deploy-workflow
echo 'pr-deploy-workflow registry guard: PASS'

# Mock git + gh for pr-deploy-workflow happy path against stack dcr-kube1
render_dir="${tmp_dir}/render"
mkdir -p "$render_dir"
printf 'name: Tag Build\n      runner: dcr-frontendx-prod\n' > "${render_dir}/frontendx-tag-build.yaml"
remote_bare="${tmp_dir}/frontendx-remote.git"
seed_repo="${tmp_dir}/frontendx-seed"
command git init --bare -b develop "$remote_bare" >/dev/null 2>&1
command git clone "$remote_bare" "$seed_repo" >/dev/null 2>&1
mkdir -p "${seed_repo}/.github/workflows" \
    "${seed_repo}/apps/dicty-frontpage" \
    "${seed_repo}/apps/stock-center" \
    "${seed_repo}/apps/publication" \
    "${seed_repo}/apps/genome-page"
printf 'name: staging\n' > "${seed_repo}/.github/workflows/staging.yml"
printf 'name: ci\n       runner: dcr-frontendx\n' > "${seed_repo}/.github/workflows/ci.yml"
printf '# frontendx\n\n## Table of Contents\n\n- [Installation](#installation)\n' > "${seed_repo}/README.md"
cat > "${seed_repo}/apps/dicty-frontpage/.env.production" <<'ENV'
VITE_APP_STOCKCENTER_URL="https://dictybase.dev/stockcenter"
VITE_APP_FRONTPAGE_URL="https://dictybase.dev"
VITE_APP_PUBLICATION_URL="https://dictybase.dev/publication"
VITE_APP_GRAPHQL_SERVER="https://graphql.dictybase.dev"
VITE_AUTH_ENABLED=true
ENV
cp "${seed_repo}/apps/dicty-frontpage/.env.production" "${seed_repo}/apps/stock-center/.env.production"
cat > "${seed_repo}/apps/publication/.env.production" <<'ENV'
VITE_FRONTPAGE_URL="https://dictybase.dev"
VITE_STOCKCENTER_URL="https://dictybase.dev/stockcenter"
VITE_PUBLICATION_URL="https://dictybase.dev/publication"
VITE_GRAPHQL_SERVER='https://graphql.dictybase.dev'
VITE_DEPLOY_ENV='development'
VITE_AUTH_ENABLED=true
ENV
cat > "${seed_repo}/apps/genome-page/.env.development" <<'ENV'
VITE_BASENAME='/gene'
VITE_GRAPHQL_SERVER='https://graphql.dictybase.dev'
VITE_FRONTPAGE_URL="https://dictybase.dev"
VITE_STOCKCENTER_URL="https://dictybase.dev/stockcenter"
VITE_PUBLICATION_URL="https://dictybase.dev/publication"
VITE_DEPLOY_ENV='development'
VITE_AUTH_ENABLED=true
ENV
cat > "${seed_repo}/apps/genome-page/.env.production" <<'ENV'
NEXT_PUBLIC_BASENAME='gene'
NEXT_PUBLIC_GRAPHQL_SERVER="https://staginggraphql.dictycr.org"
ENV
command git -C "$seed_repo" config user.email "test@example.org"
command git -C "$seed_repo" config user.name "Test"
command git -C "$seed_repo" add -A
command git -C "$seed_repo" commit -m "seed" >/dev/null
command git -C "$seed_repo" push origin develop >/dev/null 2>&1

real_git="$(command -v git)"
cat > "${tmp_dir}/bin/git" << MOCK_GIT
#!/usr/bin/env bash
set -euo pipefail
args=()
for a in "\$@"; do
    if [[ "\$a" == "https://github.com/dictyBase/frontendx.git" ]]; then
        args+=("${remote_bare}")
    else
        args+=("\$a")
    fi
done
if [[ "\${args[*]}" == *"commit -m"* ]]; then
    exec "$real_git" -c user.email=test@example.org -c user.name=Test "\${args[@]}"
fi
exec "$real_git" "\${args[@]}"
MOCK_GIT
chmod +x "${tmp_dir}/bin/git"

cat > "${tmp_dir}/bin/gh" <<'MOCK_GH'
#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >> "${STUB_LOG}"
case "$1 $2" in
    "pr list") printf '' ;;
    "pr create") printf 'https://github.com/dictyBase/frontendx/pull/999\n' ;;
    *) exit 0 ;;
esac
MOCK_GH
chmod +x "${tmp_dir}/bin/gh"

: > "$log_file"
pr_out=$(run_recipe "$just_bin" frontend pr-deploy-workflow --stack dcr-kube1 --render-dir "$render_dir")
printf '%s' "$pr_out" | rg -q 'https://github.com/dictyBase/frontendx/pull/999'
rg -q 'gh pr create --repo dictyBase/frontendx' "$log_file"
verify_clone="${tmp_dir}/verify-clone"
"$real_git" clone --branch ci/tag-deploy-dcr-kube1 "$remote_bare" "$verify_clone" >/dev/null 2>&1
[[ -f "${verify_clone}/.github/workflows/tag-build.yaml" ]]
[[ ! -f "${verify_clone}/.github/workflows/staging.yml" ]]
rg -q 'runner: dcr-frontendx-prod' "${verify_clone}/.github/workflows/tag-build.yaml"
rg -q 'runner: dcr-frontendx-beefy' "${verify_clone}/.github/workflows/ci.yml"
rg -q '# Production Deployment' "${verify_clone}/README.md"
rg -q 'https://github.com/purpleclay/nsv' "${verify_clone}/README.md"
rg -q 'tag=\$\(nsv next\)' "${verify_clone}/README.md"
rg -q 'VITE_APP_GRAPHQL_SERVER="https://graphql.dictycr.org"' "${verify_clone}/apps/dicty-frontpage/.env.production"
rg -q 'VITE_APP_FRONTPAGE_URL="https://dictycr.org"' "${verify_clone}/apps/stock-center/.env.production"
rg -q "VITE_GRAPHQL_SERVER='https://graphql.dictycr.org'" "${verify_clone}/apps/publication/.env.production"
rg -q "VITE_GRAPHQL_SERVER='https://graphql.dictycr.org'" "${verify_clone}/apps/genome-page/.env.production"
rg -q 'VITE_PUBLICATION_URL="https://dictycr.org/publication"' "${verify_clone}/apps/genome-page/.env.production"
echo 'pr-deploy-workflow happy path: PASS'

echo 'frontend recipe contract tests PASSED'
