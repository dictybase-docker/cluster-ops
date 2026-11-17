#!/usr/bin/env bash
# Contract tests for the `just filestash` recipes.
#
# curl, docker, kubectl, mc, velero, and `go run ./cmd/cluster-ops forward run`
# are mocked on PATH: each mock records its call in $STUB_LOG and answers from
# STUB_* variables. yq and jq run for real against throwaway fixture files.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

just_bin="$(command -v just)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/filestash-test.XXXXXX")"
log_file="${tmp_dir}/calls.log"
stack="fs-contract-$$"
cfg_file="filestash/Pulumi.${stack}.yaml"
reg_ingress="config/clusters/fs-scaffold-ing-$$.yaml"
reg_gateway="config/clusters/fs-scaffold-gw-$$.yaml"
gw_file="gateway/Pulumi.fs-scaffold-gw-$$.yaml"
cleanup() {
    rm -rf "$tmp_dir"
    rm -f "$cfg_file" "$reg_ingress" "$reg_gateway" "$gw_file" \
        "filestash/Pulumi.fs-scaffold-ing-$$.yaml" "filestash/Pulumi.fs-scaffold-gw-$$.yaml"
}
trap cleanup EXIT
mkdir -p "${tmp_dir}/bin" "${tmp_dir}/fixtures"

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s' "$1" | sha256sum | awk '{print $1}'
    else
        printf '%s' "$1" | shasum -a 256 | awk '{print $1}'
    fi
}

# curl writes "content-of-<last url segment>" to the -o path, unless the URL
# is listed in STUB_CURL_FAIL.
cat > "${tmp_dir}/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'curl %s\n' "$*" >> "${STUB_LOG}"
out="" url=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -o) out="$2"; shift 2 ;;
        --retry|-m|--max-time) shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
[[ "${STUB_CURL_FAIL:-}" == *"$url"* ]] && exit 22
case "$url" in
    http://localhost:19000/healthz) exit 0 ;;
    http://localhost:19000/about) printf '%s' "${STUB_ABOUT:-}"; exit 0 ;;
    http://localhost:19000/admin/tty/) printf '%s' "${STUB_TTY_CODE:-404}"; exit 0 ;;
esac
printf 'content-of-%s' "${url##*/}" > "$out"
MOCK

cat > "${tmp_dir}/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    "buildx imagetools inspect"*) printf 'raw-manifest' ;;
esac
MOCK
stub_dir="${tmp_dir}/state"
mkdir -p "$stub_dir"

# kubectl keeps Secret state in $STUB_DIR so a recipe can create a Secret and
# read it back in the same run.
cat > "${tmp_dir}/bin/kubectl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "${STUB_LOG}"
b64() { printf '%s' "$1" | base64; }
env_value() { [[ -f "$1" ]] || exit 1; awk -F= -v k="$2" '$1 == k {sub(/^[^=]*=/, ""); print; f=1} END {exit !f}' "$1" | tr -d '\n' | base64; }
args="$*"
case "$args" in
    *"get namespace ${STUB_NS_MISSING:-none}"*) exit 1 ;;
    *"get namespace"*) exit 0 ;;
    *"get secret minio-root -o jsonpath={.data.rootUser}"*) b64 rootuser ;;
    *"get secret minio-root -o jsonpath={.data.rootPassword}"*) b64 "root-pass-SECRET" ;;
    *"get secret minio-root"*) exit 0 ;;
    *"get secret filestash-users -o jsonpath={.data."*)
        key="${args##*{.data.}"; key="${key%%\}*}"
        env_value "${STUB_DIR}/filestash-users.env" "$key" ;;
    *"get secret filestash-users"*) [[ -f "${STUB_DIR}/filestash-users.env" ]] ;;
    *"get secret filestash -o jsonpath={.data."*)
        key="${args##*{.data.}"; key="${key%%\}*}"
        env_value "${STUB_DIR}/filestash.env" "$key" ;;
    *"create secret generic filestash-users --from-env-file="*)
        f="${args##*--from-env-file=}"; cp "${f%% *}" "${STUB_DIR}/filestash-users.env" ;;
    *"create secret generic filestash --from-env-file="*"--dry-run=client"*)
        f="${args##*--from-env-file=}"; cat "${f%% *}" ;;
    *"label --local -f -"*) cat ;;
    *"apply -f -"*) cat > "${STUB_DIR}/filestash.env" ;;
    *"label secret"*) exit 0 ;;
    *"get service ${STUB_SERVICE_MISSING:-none}"*) exit 1 ;;
    *"get service"*) exit 0 ;;
    *"rollout status"*) exit 0 ;;
    *"get pvc filestash-state -o jsonpath={.status.phase}"*) printf 'Bound' ;;
    *"get pvc filestash-state -o jsonpath={.spec.resources.requests.storage}"*) printf '%s' "${STUB_PVC_SIZE:-5Gi}" ;;
    *"get pvc filestash-state -o jsonpath={.spec.storageClassName}"*) printf 'dictycr-balanced' ;;
    *"get storageclass dictycr-balanced"*) printf 'true' ;;
    *"exec deployment/filestash -c filestash -- df -P"*)
        printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/sdb 5000 500 4500 %s%% /app/data/state\n' "${STUB_DF_USE:-10}" ;;
    *"exec deployment/filestash -c filestash -- ls"*) echo "share.sql" ;;
    *) echo "unexpected kubectl call: $*" >&2; exit 1 ;;
esac
MOCK

# go: `run ./cmd/cluster-ops forward run ... -- <cmd>` runs <cmd> with
# FORWARD_PORT=19000; every other cluster-ops call only records itself.
cat > "${tmp_dir}/bin/go" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >> "${STUB_LOG}"
if [[ "$*" == "run ./cmd/cluster-ops forward run"* ]]; then
    while [[ $# -gt 0 && "$1" != "--" ]]; do shift; done
    shift
    FORWARD_PORT=19000 exec "$@"
fi
MOCK

# mc records argv only. `admin user add` reads keys from stdin; the mock
# records that it got two lines, never the values.
cat > "${tmp_dir}/bin/mc" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'mc %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    "admin user add"*) n=$(wc -l | tr -d ' '); printf 'mc-stdin-lines %s\n' "$n" >> "${STUB_LOG}" ;;
    "admin user info"*) printf '{"policyName":"%s"}\n' "${STUB_ATTACHED:-}" ;;
    "anonymous get-json"*) cat "${STUB_ANON_FILE:-filestash/policies/downloads-anonymous.json}" ;;
    "anonymous get minio/uploads") echo 'Access permission for `minio/uploads` is `private`' ;;
esac
MOCK

cat > "${tmp_dir}/bin/htpasswd" <<'MOCK'
#!/usr/bin/env bash
cat >/dev/null
printf ':$2y$10$fakehashfakehashfakehashfakehashfakehashfakehashfake\n'
MOCK

cat > "${tmp_dir}/bin/pulumi" <<'MOCK'
#!/usr/bin/env bash
printf 'pulumi %s\n' "$*" >> "${STUB_LOG}"
MOCK
chmod +x "${tmp_dir}/bin/"*

run_recipe() {
    env "PATH=${tmp_dir}/bin:${PATH}" "STUB_LOG=${log_file}" "STUB_DIR=${stub_dir}" "$@"
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

assert_no_call() {
    local pattern="$1"
    if rg -q -- "$pattern" "$log_file"; then
        printf 'unexpected call matching %s:\n' "$pattern" >&2
        cat "$log_file" >&2
        exit 1
    fi
}

# Fixture lock files that pass the gate. `bad` makes one checksum wrong.
write_locks() {
    local mode="${1:-good}" theme_sum
    theme_sum=$(sha256_of 'content-of-theme_apple.zip')
    [[ "$mode" == "bad" ]] && theme_sum=$(sha256_of 'something else')
    cat > "${tmp_dir}/fixtures/plugins.lock.yaml" <<YAML
plugins:
  - name: theme_apple
    url: https://example.test/theme_apple.zip
    sha256: ${theme_sum}
    license: test-license
    reviewer: tester
office:
  name: application_office
  ref: 7509e60ed29d8f19add5220e589fc6f1a388a3c0
  reviewer: tester
YAML
    cat > "${tmp_dir}/fixtures/office.lock.yaml" <<YAML
files:
  - file: soffice.js
    url: https://example.test/soffice.js
    sha256: $(sha256_of 'content-of-soffice.js')
YAML
}

lock_flags=(--lock "${tmp_dir}/fixtures/plugins.lock.yaml" --office-lock "${tmp_dir}/fixtures/office.lock.yaml")

echo "=== 1. plugin lock files ==="
lock=filestash/plugins/plugins.lock.yaml
bad=$(yq -r '.plugins[] | select((.sha256 | test("^[0-9a-f]{64}$") | not) or (.url | test("^https://") | not) or (.size | type) != "number") | .name' "$lock")
[[ -z "$bad" ]] || { echo "lock entries without sha256/url/size: $bad" >&2; exit 1; }
for want in theme_apple application_adobe application_biomed application_photography; do
    yq -e ".plugins[] | select(.name == \"$want\")" "$lock" >/dev/null
done
[[ "$(yq -r '.office.ref' "$lock")" =~ ^[0-9a-f]{40}$ ]]
echo '  test_plugins_lock_complete: PASS'

echo "=== 2. just filestash build-plugins ==="
expect_failure 'no bundle tag' "$just_bin" filestash build-plugins
expect_failure "invalid --tag 'latest'" "$just_bin" filestash build-plugins --tag latest
echo '  test_build_plugins_requires_tag: PASS'

: > "$log_file"
expect_failure 'reviewer' "$just_bin" filestash build-plugins --tag t1
assert_no_call '^curl '
assert_no_call '^docker '
echo '  test_build_plugins_refuses_unreviewed (real lock): PASS'

write_locks bad
: > "$log_file"
expect_failure 'checksum mismatch' "$just_bin" filestash build-plugins --tag t1 "${lock_flags[@]}"
assert_no_call '^docker '
echo '  test_build_plugins_fails_on_checksum: PASS'

write_locks good
: > "$log_file"
out=$(run_recipe "$just_bin" filestash build-plugins --tag t1 "${lock_flags[@]}")
rg -q 'docker buildx build --platform linux/amd64 --build-arg FILESTASH_REF=7509e60ed29d8f19add5220e589fc6f1a388a3c0 -t dictybase/filestash-plugins:t1 --push' "$log_file"
printf '%s' "$out" | rg -qF "Built and pushed dictybase/filestash-plugins:t1@sha256:$(sha256_of raw-manifest)"
printf '%s' "$out" | rg -q 'properties.plugins.image.digest'
echo '  test_build_plugins_happy_path: PASS'

echo "=== 3. just filestash lock-office-deps ==="
cat > "${tmp_dir}/fixtures/office-fill.lock.yaml" <<'YAML'
# header comment stays
files:
  - file: soffice.js
    url: https://example.test/soffice.js
    sha256: ""
  - file: zeta.js
    url: https://example.test/zeta.js
    sha256: ""
YAML
run_recipe "$just_bin" filestash lock-office-deps --office-lock "${tmp_dir}/fixtures/office-fill.lock.yaml" >/dev/null
rg -q '^# header comment stays' "${tmp_dir}/fixtures/office-fill.lock.yaml"
[[ "$(yq -r '.files[1].sha256' "${tmp_dir}/fixtures/office-fill.lock.yaml")" == "$(sha256_of 'content-of-zeta.js')" ]]
[[ "$(yq -r '.files[0].sha256' "${tmp_dir}/fixtures/office-fill.lock.yaml")" == "$(sha256_of 'content-of-soffice.js')" ]]
echo '  test_lock_office_deps_writes_sums: PASS'

echo "=== 4. MinIO policy files ==="
pol=filestash/policies
actions() { jq -c '[.Statement[] | {a: (.Action | sort), r: (.Resource | sort)}] | sort' "$pol/$1.json"; }
[[ "$(actions intake)" == '[{"a":["s3:ListBucket"],"r":["arn:aws:s3:::uploads"]},{"a":["s3:PutObject"],"r":["arn:aws:s3:::uploads/*"]}]' ]]
[[ "$(actions public)" == '[{"a":["s3:GetObject"],"r":["arn:aws:s3:::downloads/*"]},{"a":["s3:ListBucket"],"r":["arn:aws:s3:::downloads"]}]' ]]
[[ "$(actions operator)" == '[{"a":["s3:DeleteObject","s3:GetObject"],"r":["arn:aws:s3:::uploads/*"]},{"a":["s3:GetObject"],"r":["arn:aws:s3:::downloads/*"]},{"a":["s3:ListBucket"],"r":["arn:aws:s3:::downloads","arn:aws:s3:::uploads"]}]' ]]
[[ "$(actions publisher)" == '[{"a":["s3:DeleteObject","s3:GetObject","s3:PutObject"],"r":["arn:aws:s3:::downloads/*"]},{"a":["s3:GetObject"],"r":["arn:aws:s3:::uploads/*"]},{"a":["s3:ListBucket"],"r":["arn:aws:s3:::downloads","arn:aws:s3:::uploads"]}]' ]]
[[ "$(actions downloads-anonymous)" == '[{"a":["s3:GetObject"],"r":["arn:aws:s3:::downloads/*"]}]' ]]
jq -e '[.Statement[].Action[]] | index("s3:GetObject") | not' "$pol/intake.json" >/dev/null
jq -e '[.Statement[] | select(.Resource[] | startswith("arn:aws:s3:::downloads/")) | .Action[]] | index("s3:PutObject") | not' "$pol/operator.json" >/dev/null
echo '  test_policy_files: PASS'

cat > "$cfg_file" <<'YAML'
config:
  filestash:properties:
    namespace: prod
    storage:
      size: 5Gi
YAML

echo "=== 5. stack and namespace resolution ==="
expect_failure 'no stack name' env -u PULUMI_STACK "$just_bin" filestash create-buckets
echo '  test_missing_stack_names_flag: PASS'
: > "$log_file"
expect_failure "--namespace 'other' does not match properties.namespace 'prod'" env "PULUMI_STACK=$stack" "$just_bin" filestash create-buckets --namespace other
assert_no_call '^mc '
assert_no_call '^go '
echo '  test_namespace_mismatch_fails_before_mutation: PASS'

echo "=== 6. create-secret ==="
rm -f "$stub_dir"/*.env
printf 'a-long-admin-password\n' | run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash create-secret >/dev/null
rg -q '^CONFIG_SECRET=[A-Za-z0-9]{32}$' "$stub_dir/filestash.env"
rg -q '^ADMIN_PASSWORD=\$2y\$10\$' "$stub_dir/filestash.env"
printf 'ADMIN_PASSWORD=old\nCONFIG_SECRET=keepme\n' > "$stub_dir/filestash.env"
printf 'another-long-password\n' | run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash create-secret >/dev/null
rg -q '^CONFIG_SECRET=keepme$' "$stub_dir/filestash.env"
rg -q '^ADMIN_PASSWORD=\$2y\$10\$' "$stub_dir/filestash.env"
echo '  test_create_secret_keeps_config_secret: PASS'
printf 'short\n' > "$tmp_dir/pw"
expect_failure 'at least 12 characters' env "PULUMI_STACK=$stack" bash -c "\"$just_bin\" filestash create-secret < \"$tmp_dir/pw\""
echo '  test_create_secret_rejects_short_password: PASS'

echo "=== 7. create-users ==="
rm -f "$stub_dir/filestash-users.env"
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash create-users >/dev/null
[[ "$(rg -c '^mc admin user add minio$' "$log_file")" == 4 ]]
[[ "$(rg -c '^mc-stdin-lines 2$' "$log_file")" == 4 ]]
rg -q '^mc admin policy attach minio filestash-publisher --user filestash-publisher$' "$log_file"
while IFS='=' read -r _ value; do
    if rg -qF -- "$value" "$log_file" && [[ "$value" != filestash-* ]]; then
        echo "secret value leaked into argv log" >&2; exit 1
    fi
done < "$stub_dir/filestash-users.env"
assert_no_call 'root-pass-SECRET'
echo '  test_create_users_never_passes_keys_in_argv: PASS'
before=$(cat "$stub_dir/filestash-users.env")
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "STUB_ATTACHED=filestash-intake,filestash-public,filestash-operator,filestash-publisher" "$just_bin" filestash create-users >/dev/null
assert_no_call 'create secret'
assert_no_call 'policy attach'
[[ "$(cat "$stub_dir/filestash-users.env")" == "$before" ]]
echo '  test_create_users_is_idempotent: PASS'

echo "=== 8. buckets and bucket policy ==="
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash create-buckets >/dev/null
rg -q '^mc mb --ignore-existing minio/uploads$' "$log_file"
rg -q '^mc mb --ignore-existing minio/downloads$' "$log_file"
rg -q '^mc anonymous set none minio/uploads$' "$log_file"
echo '  test_create_buckets: PASS'
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash publish-bucket >/dev/null
rg -q '^mc anonymous set-json filestash/policies/downloads-anonymous.json minio/downloads$' "$log_file"
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash unpublish-bucket >/dev/null
rg -q '^mc anonymous set none minio/downloads$' "$log_file"
echo '  test_unpublish_bucket_sets_none: PASS'

echo "=== 9. verify ==="
about="theme_apple application_adobe application_biomed application_photography application_office plg_search_stateless plg_image_c"
run_recipe env "PULUMI_STACK=$stack" "STUB_ABOUT=$about" "$just_bin" filestash verify | rg -q 'all checks passed'
echo '  test_verify_happy_path: PASS'
expect_failure 'state PVC use' env "PULUMI_STACK=$stack" "STUB_ABOUT=$about" "STUB_DF_USE=81" "$just_bin" filestash verify
echo '  test_verify_fails_on_full_pvc: PASS'
expect_failure 'plugin application_adobe missing' env "PULUMI_STACK=$stack" "STUB_ABOUT=theme_apple" "$just_bin" filestash verify
expect_failure '/admin/tty/ returned 200' env "PULUMI_STACK=$stack" "STUB_ABOUT=$about" "STUB_TTY_CODE=200" "$just_bin" filestash verify
echo '  test_verify_checks_plugins: PASS'
printf '{"Statement":[]}' > "$tmp_dir/anon.json"
expect_failure 'downloads anonymous policy differs' env "PULUMI_STACK=$stack" "STUB_ABOUT=$about" "STUB_ANON_FILE=$tmp_dir/anon.json" "$just_bin" filestash verify
echo '  test_verify_checks_bucket_policy: PASS'

echo "=== 10. resize-state and teardown ==="
: > "$log_file"
expect_failure 'not larger than the current 5Gi' env "PULUMI_STACK=$stack" "$just_bin" filestash resize-state --size 1Gi
assert_no_call 'stack config set'
echo '  test_resize_rejects_shrink: PASS'
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash resize-state --size 10Gi >/dev/null
rg -q 'stack config set --folder filestash --stack '"$stack"' --key properties.storage.size --value 10Gi' "$log_file"
rg -q 'stack up --folder filestash' "$log_file"
echo '  test_resize_grows: PASS'
: > "$log_file"
expect_failure 'Pass --delete-pvc yes' env "PULUMI_STACK=$stack" "$just_bin" filestash teardown
assert_no_call 'stack destroy'
echo '  test_teardown_requires_flag: PASS'

echo "=== 11. scaffold-filestash-stack ==="
expect_failure '--state-size' env "PULUMI_STACK=fs-scaffold-ing-$$" "$just_bin" gcp-pulumi scaffold-filestash-stack
[[ ! -e "filestash/Pulumi.fs-scaffold-ing-$$.yaml" ]]
echo '  test_scaffold_requires_state_size: PASS'
for kind in ing gw; do
    ns=dev; [[ "$kind" == gw ]] && ns=prod
    cat > "config/clusters/fs-scaffold-${kind}-$$.yaml" <<YAML
stack: fs-scaffold-${kind}-$$
namespace: ${ns}
kms_secrets_provider: gcpkms://projects/test/locations/x/keyRings/y/cryptoKeys/z
YAML
done
printf 'config: {}\n' > "$gw_file"
run_recipe env "PULUMI_STACK=fs-scaffold-ing-$$" "$just_bin" gcp-pulumi scaffold-filestash-stack --state-size 2Gi >/dev/null
f="filestash/Pulumi.fs-scaffold-ing-$$.yaml"
[[ "$(yq -r '.config."filestash:properties".ingress.enabled' "$f")" == true ]]
[[ "$(yq -r '.config."filestash:properties".namespace' "$f")" == dev ]]
[[ "$(yq -r '.config."filestash:properties".storage.size' "$f")" == 2Gi ]]
run_recipe env "PULUMI_STACK=fs-scaffold-gw-$$" "$just_bin" gcp-pulumi scaffold-filestash-stack --state-size 5Gi --host files.example.org >/dev/null
f="filestash/Pulumi.fs-scaffold-gw-$$.yaml"
[[ "$(yq -r '.config."filestash:properties".ingress.enabled' "$f")" == false ]]
[[ "$(yq -r '.config."filestash:properties".namespace' "$f")" == prod ]]
[[ "$(yq -r '.config."filestash:properties".applicationURL' "$f")" == https://files.example.org ]]
echo '  test_scaffold_selects_ingress_mode: PASS'

echo "All filestash recipe contract tests passed."
