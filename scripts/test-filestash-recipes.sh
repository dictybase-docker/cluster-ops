#!/usr/bin/env bash
# Contract tests for the `just filestash` recipes.
#
# curl, docker, gcloud, kubectl, mc, pulumi, and `go run ./cmd/cluster-ops forward run`
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
bcfg_file="filestash-backup/Pulumi.${stack}.yaml"
reg_ingress="config/clusters/fs-scaffold-ing-$$.yaml"
reg_gateway="config/clusters/fs-scaffold-gw-$$.yaml"
gw_file="gateway/Pulumi.fs-scaffold-gw-$$.yaml"
cleanup() {
    rm -rf "$tmp_dir"
    rm -f "$cfg_file" "$bcfg_file" "$reg_ingress" "$reg_gateway" "$gw_file" \
        "filestash/Pulumi.fs-scaffold-ing-$$.yaml" "filestash/Pulumi.fs-scaffold-gw-$$.yaml" \
        "filestash-backup/Pulumi.fs-scaffold-ing-$$.yaml" "filestash-backup/Pulumi.fs-scaffold-gw-$$.yaml"
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
out="" url="" wfmt="" method=GET range=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        -o) out="$2"; shift 2 ;;
        -w) wfmt="$2"; shift 2 ;;
        -X) method="$2"; shift 2 ;;
        -H) [[ "$2" == Range:* ]] && range=1; shift 2 ;;
        --data|--retry|-m|--max-time) shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
[[ "${STUB_CURL_FAIL:-}" == *"$url"* ]] && exit 22
case "$url" in
    http://localhost:19000/healthz) exit 0 ;;
    http://localhost:19000/about) printf '%s' "${STUB_ABOUT:-}"; exit 0 ;;
    http://localhost:19000/admin/tty/) printf '%s' "${STUB_TTY_CODE:-404}"; exit 0 ;;
    https://storage.test/*)
        code=200
        if [[ "$method" != GET || "$url" == */downloads/ || "$url" == */uploads/* ]]; then code=403
        elif [[ "$range" == 1 ]]; then code="${STUB_RANGE_CODE:-206}"; fi
        printf '%s' "$code"; exit 0 ;;
    https://files.test/*)
        : > "${out:-/dev/null}"
        if [[ "${STUB_ADMIN_LEAK:-}" == "$url" ]]; then printf '<script src="router_backoffice.js">' > "$out"; printf '200 '
        elif [[ "${STUB_ADMIN_JSON_LEAK:-}" == "$url" ]]; then printf '{"status":"error","message":"Not Authorized"}' > "$out"; printf '401 '
        elif [[ "${STUB_ADMIN_NOREDIRECT:-}" == "$url" ]]; then printf '200 '
        elif [[ "${STUB_ADMIN_DOWN:-0}" == 1 ]]; then exit 7
        else printf '302 https://files.test/'; fi
        exit 0 ;;
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
    *" get secret/"*|*" get configmap/"*|*" get deployment/"*|*" get pvc/"*) exit 0 ;;
    *"get namespace ${STUB_NS_MISSING:-none}"*) exit 1 ;;
    *"get namespace filestash-restore-new"*) exit 1 ;;
    *"get namespace ${STUB_NS_EXISTS:-none} -o jsonpath"*) printf '%s' "${STUB_NS_LABEL:-}" ;;
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
    *"apply -f -"*)
        body=$(cat)
        case "$body" in
            *NetworkPolicy*) printf 'kubectl-applied NetworkPolicy\n' >> "${STUB_LOG}" ;;
            *) printf 'kubectl-applied %s %s %s\n' "$(jq -r .kind <<<"$body")" "$(jq -r .metadata.name <<<"$body")" \
                   "$(jq -r .metadata.namespace <<<"$body")" >> "${STUB_LOG}" ;;
        esac ;;
    *"exec deployment/filestash -c filestash -- cat /app/data/state/db/share.sql"*) cat "${STUB_SHARE_DB}" ;;
    *"get secret filestash -o json"*|*"get secret filestash-backup-auth -o json"*|*"get configmap filestash-seed -o json"*)
        kind=Secret; [[ "$args" == *configmap* ]] && kind=ConfigMap
        name="${args#*get }"; name="${name#* }"; name="${name%% *}"
        printf '{"apiVersion":"v1","kind":"%s","metadata":{"name":"%s","namespace":"prod","uid":"u"}}' "$kind" "$name" ;;
    *"get secret filestash-backup-auth"*) [[ "${STUB_NO_BACKUP_AUTH:-0}" == 1 ]] && exit 1; exit 0 ;;
    *"get secret filestash"*|*"get configmap filestash-seed"*) exit 0 ;;
    *"get cronjob filestash-state-backup -o jsonpath={.status.lastSuccessfulTime}"*)
        if [[ -n "${STUB_BACKUP_AGE_H:-}" ]]; then jq -rn --argjson h "$STUB_BACKUP_AGE_H" 'now - ($h * 3600) | todate'; fi ;;
    *"logs job/filestash-state-backup-now-"*"-c restic"*) echo "Files: 3 new"; echo "snapshot 4f2a9c1b saved" ;;
    *"exec deployment/filestash -c filestash -- curl"*) exit 0 ;;
    *"get pvc filestash-state -o jsonpath={.metadata.labels"*) printf '%s' "${STUB_PVC_EXCLUDE-true}" ;;
    *"get pods -l app.kubernetes.io/name=filestash -o name"*) exit 0 ;;
    *"create job --from=cronjob/filestash-state-backup"*) exit 0 ;;
    *"wait --for=condition=complete job/filestash-state-backup-now-"*) [[ "${STUB_BACKUP_FAIL:-0}" == 1 ]] && exit 1; exit 0 ;;
    *"wait --for=condition=complete job/filestash-state-restore"*) [[ "${STUB_RESTORE_FAIL:-0}" == 1 ]] && exit 1; exit 0 ;;
    *"wait --for=condition=complete job/filestash-state-check"*) [[ "${STUB_CHECK_FAIL:-0}" == 1 ]] && exit 1; exit 0 ;;
    *"delete job filestash-state-check"*) exit 0 ;;
    *"wait --for=condition=complete"*) exit 0 ;;
    *" logs "*) echo "restored state from snapshot" ;;
    *"delete job filestash-state-restore"*) exit 0 ;;
    *"get deployment filestash -o json"*) printf '{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"filestash","namespace":"prod","uid":"u1"},"status":{}}' ;;
    *"get deployment filestash"*) exit 0 ;;
    *"scale deployment/filestash"*) exit 0 ;;
    *"create namespace"*|*"label namespace"*|*"delete namespace"*) exit 0 ;;
    *"get service ${STUB_SERVICE_MISSING:-none}"*) exit 1 ;;
    *"get service"*) exit 0 ;;
    *"rollout status"*) exit 0 ;;
    *"get pvc filestash-state -o jsonpath={.status.phase}"*) printf 'Bound' ;;
    *"get pvc filestash-state -o jsonpath={.spec.resources.requests.storage}"*) printf '%s' "${STUB_PVC_SIZE:-5Gi}" ;;
    *"get pvc filestash-state -o jsonpath={.spec.storageClassName}"*) printf 'dictycr-balanced' ;;
    *"get pvc filestash-state"*) [[ "${STUB_PVC_EXISTS:-0}" == 1 ]] ;;
    *"patch cronjob filestash-state-backup"*) exit 0 ;;
    *"get pods -l app.kubernetes.io/component=state-backup"*) printf '%s' "${STUB_BACKUP_ACTIVE:-}" ;;
    *"get storageclass dictycr-balanced"*) printf 'true' ;;
    *"exec deployment/filestash -c filestash -- df -P"*)
        printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/sdb 5000 500 4500 %s%% /app/data/state\n' "${STUB_DF_USE:-10}" ;;
    *"exec deployment/filestash -c filestash -- ls"*) echo "share.sql" ;;
    *"create -f -"*)
        body=$(cat)
        name=$(awk '/^  name:/ {print $2; exit}' <<<"$body")
        if [[ "$body" == *"kind: Lease"* ]]; then
            [[ "${STUB_LEASE_HELD:-0}" == 1 ]] && { echo 'Error from server (AlreadyExists)' >&2; exit 1; }
            [[ "${STUB_LEASE_FORBIDDEN:-0}" == 1 ]] && { echo 'Error from server (Forbidden): leases is forbidden' >&2; exit 1; }
            printf '%s\n' "$body" > "${STUB_DIR}/lease-${name}.yaml"
        else
            printf '%s\n' "$body" > "${STUB_DIR}/created.yaml"
            printf '%s\n' "$body" > "${STUB_DIR}/created-${name}.yaml"
            printf 'kubectl-created %s %s\n' "$(awk '/^kind:/ {print $2; exit}' <<<"$body")" "$name" >> "${STUB_LOG}"
        fi ;;
    *"get lease "*" -o jsonpath={.spec.holderIdentity}"*)
        lname="${args#*get lease }"; lname="${lname%% *}"
        if [[ -n "${STUB_LEASE_HOLDER:-}" ]]; then printf '%s' "$STUB_LEASE_HOLDER"
        elif [[ -f "${STUB_DIR}/lease-${lname}.yaml" ]]; then awk '/holderIdentity:/ {printf "%s", $2}' "${STUB_DIR}/lease-${lname}.yaml"
        else exit 1; fi ;;
    *"delete lease "*) lname="${args##*delete lease }"; rm -f "${STUB_DIR}/lease-${lname%% *}.yaml" ;;
    *"get cronjob filestash-state-backup -o jsonpath={.spec.suspend}"*) printf '%s' "${STUB_SUSPEND:-false}" ;;
    *"get jobs -l app.kubernetes.io/component=state-backup -o json"*)
        jobs_default='{"items":[]}'
        printf '%s' "${STUB_JOBS_JSON:-$jobs_default}" ;;
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
# stack config set: --value V or standard input. Writes the value into the
# stack file (secrets as {secure: ...}) and keeps the plaintext for `pulumi
# config get`. Records the byte count of standard input, never the value.
if [[ "$*" == "run ./cmd/cluster-ops stack config set"* ]]; then
    folder="" stack="" key="" value="" secret=0 has_value=0
    shift 5
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --folder) folder="$2"; shift 2 ;;
            --stack) stack="$2"; shift 2 ;;
            --key) key="$2"; shift 2 ;;
            --value) value="$2"; has_value=1; shift 2 ;;
            --secret) secret=1; shift ;;
            *) shift ;;
        esac
    done
    if [[ "$has_value" == 0 ]]; then value=$(cat); printf 'go-stdin-bytes %s %s\n' "$key" "${#value}" >> "${STUB_LOG}"; fi
    f="$folder/Pulumi.$stack.yaml"
    path=".config.\"$folder:properties\".${key#properties.}"
    if [[ "$secret" == 1 ]]; then
        yq -y "$path = {secure: \"v1:enc\"}" "$f" > "$f.tmp"
    else
        yq -y --arg v "$value" "$path = \$v" "$f" > "$f.tmp"
    fi
    mv "$f.tmp" "$f"
    printf '%s' "$value" > "${STUB_DIR}/cfg-$folder-${key#properties.}"
fi
MOCK

# mc records argv only. `admin user add` reads keys from stdin; the mock
# records that it got two lines, never the values.
cat > "${tmp_dir}/bin/mc" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'mc %s\n' "$*" >> "${STUB_LOG}"
{ printf 'mc-args'; printf ' [%s]' "$@"; printf '\n'; } >> "${STUB_LOG}"
case "$*" in
    "ls --recursive --json"*) printf '%s\n' "${STUB_MC_KEYS:-}" | jq -cR 'select(length > 0) | {type: "file", key: .}' ;;
    "stat "*) [[ -n "${STUB_MC_EXISTING:-}" && "${@: -1}" == *"${STUB_MC_EXISTING}" ]] || exit 1 ;;
    "admin user list"*) for u in ${STUB_MC_USERS:-}; do printf '{"accessKey":"%s"}\n' "$u"; done ;;
    "admin user add"*) n=$(wc -l | tr -d ' '); printf 'mc-stdin-lines %s\n' "$n" >> "${STUB_LOG}" ;;
    "admin user info"*) [[ "${STUB_MC_NO_USERS:-}" == 1 ]] && exit 1; printf '{"policyName":"%s"}\n' "${STUB_ATTACHED:-}" ;;
    "anonymous get-json"*) cat "${STUB_ANON_FILE:-filestash/policies/downloads-anonymous.json}" ;;
    "anonymous get minio/uploads") echo 'Access permission for `minio/uploads` is `private`' ;;
esac
MOCK



cat > "${tmp_dir}/bin/gcloud" <<'MOCK'
#!/usr/bin/env bash
printf 'gcloud %s [as %s]\n' "$*" "${CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE:-ambient}" >> "${STUB_LOG}"
case "$*" in
    "iam service-accounts describe"*)
        case "${STUB_SA_DESCRIBE:-ok}" in
            ok) exit 0 ;;
            missing) echo 'ERROR: (gcloud.iam.service-accounts.describe) NOT_FOUND: Unknown service account' >&2; exit 1 ;;
            denied) echo 'ERROR: (gcloud.iam.service-accounts.describe) PERMISSION_DENIED' >&2; exit 1 ;;
        esac ;;
esac
exit 0
MOCK

cat > "${tmp_dir}/bin/htpasswd" <<'MOCK'
#!/usr/bin/env bash
cat >/dev/null
printf ':$2y$10$fakehashfakehashfakehashfakehashfakehashfakehashfake\n'
MOCK

cat > "${tmp_dir}/bin/pulumi" <<'MOCK'
#!/usr/bin/env bash
printf 'pulumi %s\n' "$*" >> "${STUB_LOG}"
if [[ "$*" == *"config get --path "* ]]; then
    all="$*"; k="${all##*config get --path }"; k="${k%% *}"; folder="${k%%:*}"; k="${k#*:properties.}"
    cat "${STUB_DIR}/cfg-$folder-$k"
fi
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
    image:
      name: machines/filestash
      tag: latest
      digest: sha256:1111111111111111111111111111111111111111111111111111111111111111
    storage:
      size: 5Gi
    backup:
      maxAgeHours: 6
YAML
cat > "$bcfg_file" <<'YAML'
config:
  filestash-backup:properties:
    namespace: prod
    bucket: restic-filestash-backup-test
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
cfgget() { yq -r ".config.\"filestash:properties\".$1 // \"\"" "$cfg_file"; }
printf 'a-long-admin-password\n' | run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash create-secret >/dev/null
[[ "$(cfgget secrets.configSecret.secure)" == "v1:enc" ]]
[[ "$(cfgget secrets.adminPasswordHash.secure)" == "v1:enc" ]]
[[ "$(cat "$stub_dir/cfg-filestash-secrets.configSecret")" =~ ^[A-Za-z0-9]{32}$ ]]
[[ "$(cat "$stub_dir/cfg-filestash-secrets.adminPasswordHash")" == \$2y\$10\$* ]]
rg -q '^go-stdin-bytes properties.secrets.configSecret 32$' "$log_file"
assert_no_call 'a-long-admin-password'
assert_no_call "$(cat "$stub_dir/cfg-filestash-secrets.configSecret")"
assert_no_call 'create secret'
echo '  test_create_secret_stores_encrypted_stack_values: PASS'
first=$(cat "$stub_dir/cfg-filestash-secrets.configSecret")
: > "$log_file"
printf 'another-long-password\n' | run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash create-secret >/dev/null
assert_no_call 'key properties.secrets.configSecret'
[[ "$(cat "$stub_dir/cfg-filestash-secrets.configSecret")" == "$first" ]]
rg -q 'key properties.secrets.adminPasswordHash --secret' "$log_file"
echo '  test_create_secret_keeps_config_secret: PASS'
printf 'short\n' > "$tmp_dir/pw"
expect_failure 'at least 12 characters' env "PULUMI_STACK=$stack" bash -c "\"$just_bin\" filestash create-secret < \"$tmp_dir/pw\""
echo '  test_create_secret_rejects_short_password: PASS'
cp "$cfg_file" "$tmp_dir/cfg.bak"
yq -y 'del(.config."filestash:properties".secrets)' "$tmp_dir/cfg.bak" > "$cfg_file"
printf 'a-long-admin-password\n' > "$tmp_dir/pw"
: > "$log_file"
expect_failure 'pvc/filestash-state exists' env "PULUMI_STACK=$stack" "STUB_PVC_EXISTS=1" bash -c "\"$just_bin\" filestash create-secret < \"$tmp_dir/pw\""
assert_no_call 'stack config set'
cp "$tmp_dir/cfg.bak" "$cfg_file"
echo '  test_create_secret_refuses_new_config_secret_with_state: PASS'

echo "=== 7. create-users ==="
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash create-users >/dev/null
for role in intake public operator publisher; do
    [[ "$(cfgget users.$role.accessKey)" == "filestash-$role" ]]
    [[ "$(cfgget users.$role.secretKey.secure)" == "v1:enc" ]]
    rg -q "^go-stdin-bytes properties.users.$role.secretKey 40$" "$log_file"
done
[[ "$(rg -c '^mc admin user add minio$' "$log_file")" == 4 ]]
[[ "$(rg -c '^mc-stdin-lines 2$' "$log_file")" == 4 ]]
rg -q '^mc admin policy attach minio filestash-publisher --user filestash-publisher$' "$log_file"
for role in intake public operator publisher; do
    assert_no_call "$(cat "$stub_dir/cfg-filestash-users.$role.secretKey")"
done
assert_no_call 'root-pass-SECRET'
echo '  test_create_users_never_passes_keys_in_argv: PASS'
before=$(cat "$stub_dir/cfg-filestash-users.intake.secretKey")
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "STUB_MC_USERS=filestash-intake filestash-public filestash-operator filestash-publisher" \
    "STUB_ATTACHED=filestash-intake,filestash-public,filestash-operator,filestash-publisher" "$just_bin" filestash create-users >/dev/null
assert_no_call 'stack config set'
assert_no_call 'policy attach'
[[ "$(cat "$stub_dir/cfg-filestash-users.intake.secretKey")" == "$before" ]]
echo '  test_create_users_is_idempotent: PASS'
cp "$cfg_file" "$tmp_dir/cfg.bak"
yq -y 'del(.config."filestash:properties".users.intake)' "$tmp_dir/cfg.bak" > "$cfg_file"
: > "$log_file"
expect_failure 'MinIO user filestash-intake exists' env "PULUMI_STACK=$stack" "STUB_MC_USERS=filestash-intake" "$just_bin" filestash create-users
assert_no_call 'stack config set'
assert_no_call 'admin user add'
cp "$tmp_dir/cfg.bak" "$cfg_file"
echo '  test_create_users_refuses_new_keys_for_existing_users: PASS'
# Secret filestash-users as `deploy` makes it, for the publish tests below.
for role in intake public operator publisher; do
    printf '%sAccessKey=filestash-%s\n%sSecretKey=%s\n' "$role" "$role" "$role" \
        "$(cat "$stub_dir/cfg-filestash-users.$role.secretKey")"
done > "$stub_dir/filestash-users.env"

: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash check | rg -q 'prerequisites ready'
yq -y 'del(.config."filestash:properties".users.public)' "$cfg_file" > "$tmp_dir/cfg2" && cp "$cfg_file" "$tmp_dir/cfg.bak" && cp "$tmp_dir/cfg2" "$cfg_file"
expect_failure 'has no keys for public' env "PULUMI_STACK=$stack" "$just_bin" filestash check
cp "$tmp_dir/cfg.bak" "$cfg_file"
echo '  test_check_reads_stack_values: PASS'

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
vok=(env "PULUMI_STACK=$stack" "STUB_ABOUT=$about" "STUB_BACKUP_AGE_H=1")
run_recipe "${vok[@]}" "$just_bin" filestash verify | rg -q 'all checks passed'
echo '  test_verify_happy_path: PASS'
expect_failure 'state PVC use' "${vok[@]}" "STUB_DF_USE=81" "$just_bin" filestash verify
echo '  test_verify_fails_on_full_pvc: PASS'
expect_failure 'plugin application_adobe missing' "${vok[@]}" "STUB_ABOUT=theme_apple" "$just_bin" filestash verify
expect_failure '/admin/tty/ returned 200' "${vok[@]}" "STUB_TTY_CODE=200" "$just_bin" filestash verify
echo '  test_verify_checks_plugins: PASS'
printf '{"Statement":[]}' > "$tmp_dir/anon.json"
expect_failure 'downloads anonymous policy differs' "${vok[@]}" "STUB_ANON_FILE=$tmp_dir/anon.json" "$just_bin" filestash verify
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
gcp_project: proj-${kind}
kms_secrets_provider: gcpkms://projects/test/locations/x/keyRings/y/cryptoKeys/z
YAML
done
printf 'config: {}\n' > "$gw_file"
run_recipe env "PULUMI_STACK=fs-scaffold-ing-$$" "$just_bin" gcp-pulumi scaffold-filestash-stack --state-size 2Gi >/dev/null
f="filestash/Pulumi.fs-scaffold-ing-$$.yaml"
[[ "$(yq -r '.config."filestash:properties".ingress.enabled' "$f")" == false ]]
[[ "$(yq -r '.config."filestash:properties".ingress.tlsSecret' "$f")" == filestash-tls-secret ]]
[[ "$(yq -r '.config."filestash:properties".namespace' "$f")" == dev ]]
[[ "$(yq -r '.config."filestash:properties".storage.size' "$f")" == 2Gi ]]
b="filestash-backup/Pulumi.fs-scaffold-ing-$$.yaml"
[[ "$(yq -r '.config."filestash-backup:properties".namespace' "$b")" == dev ]]
[[ "$(yq -r '.config."filestash-backup:properties".bucket' "$b")" == restic-filestash-backup-proj-ing ]]
[[ "$(yq -r '.config."gcp:project"' "$b")" == proj-ing ]]
out=$(run_recipe env "PULUMI_STACK=fs-scaffold-gw-$$" "$just_bin" gcp-pulumi scaffold-filestash-stack --state-size 5Gi --host files.example.org)
printf '%s' "$out" | rg -q 'public route: Gateway route'
f="filestash/Pulumi.fs-scaffold-gw-$$.yaml"
[[ "$(yq -r '.config."filestash:properties".ingress.enabled' "$f")" == false ]]
[[ "$(yq -r '.config."filestash:properties".namespace' "$f")" == prod ]]
[[ "$(yq -r '.config."filestash:properties".applicationURL' "$f")" == https://files.example.org ]]
b="filestash-backup/Pulumi.fs-scaffold-gw-$$.yaml"
[[ "$(yq -r '.config."filestash-backup:properties".bucket' "$b")" == restic-filestash-backup-proj-gw ]]
rg -q "pulumi -C filestash-backup stack init fs-scaffold-gw-$$" "$log_file"
echo '  test_scaffold_selects_ingress_mode: PASS'

echo "=== 12. publish, unpublish-file, release-lock ==="
keys=$'table.csv\nraw/x.unknown\nmy file #1 %ü.csv\na*b.csv'
rm -f "$stub_dir/lease-filestash-publish.yaml"
: > "$log_file"
out=$(run_recipe env "PULUMI_STACK=$stack" "STUB_MC_KEYS=$keys" "$just_bin" filestash publish --source alice/batch --dataset ds1 --url-host storage.example.org)
rg -qF 'mc-args [cp] [--attr] [Content-Type=text/csv] [minio/uploads/alice/batch/table.csv] [minio/downloads/ds1/table.csv]' "$log_file"
rg -qF 'mc-args [cp] [--attr] [Content-Type=application/octet-stream] [minio/uploads/alice/batch/raw/x.unknown] [minio/downloads/ds1/raw/x.unknown]' "$log_file"
echo '  test_publish_sets_content_type: PASS'
rg -qF 'mc-args [stat] [minio/downloads/ds1/my file #1 %ü.csv]' "$log_file"
rg -qF 'mc-args [cp] [--attr] [Content-Type=text/csv] [minio/uploads/alice/batch/my file #1 %ü.csv] [minio/downloads/ds1/my file #1 %ü.csv]' "$log_file"
echo '  test_publish_quotes_special_keys: PASS'
printf '%s' "$out" | rg -qF 'https://storage.example.org/downloads/ds1/my%20file%20%231%20%25%C3%BC.csv'
printf '%s' "$out" | rg -qF 'https://storage.example.org/downloads/ds1/raw/x.unknown'
printf '%s' "$out" | rg -qF 'https://storage.example.org/downloads/ds1/a%2Ab.csv'
echo '  test_publish_encodes_urls: PASS'
rg -q '^kubectl -n prod delete lease filestash-publish$' "$log_file"
[[ ! -e "$stub_dir/lease-filestash-publish.yaml" ]]
assert_no_call '^kubectl apply'
echo '  test_publish_never_applies_lease: PASS'
rg -q 'secret filestash-users -o jsonpath=\{.data.publisherAccessKey\}' "$log_file"
assert_no_call 'jsonpath=\{.data.rootUser\}'
assert_no_call 'operatorAccessKey'
echo '  test_publish_uses_publisher_keys: PASS'

: > "$log_file"
expect_failure 'publish never replaces a file' env "PULUMI_STACK=$stack" "STUB_MC_KEYS=$keys" "STUB_MC_EXISTING=ds1/table.csv" "$just_bin" filestash publish --source alice/batch --dataset ds1 --url-host storage.example.org
assert_no_call '^mc cp '
echo '  test_publish_refuses_existing_key: PASS'

: > "$log_file"
expect_failure "invalid --source 'alice/../bob'" env "PULUMI_STACK=$stack" "$just_bin" filestash publish --source alice/../bob --dataset ds1 --url-host storage.example.org
assert_no_call '^mc '
assert_no_call 'create -f -'
echo '  test_publish_rejects_dotdot: PASS'
expect_failure "invalid --dataset 'Bad Name'" env "PULUMI_STACK=$stack" "$just_bin" filestash publish --source alice/batch --dataset 'Bad Name'

: > "$log_file"
expect_failure "holder 'bob@ci:1'" env "PULUMI_STACK=$stack" "STUB_LEASE_HELD=1" "STUB_LEASE_HOLDER=bob@ci:1" "$just_bin" filestash publish --source alice/batch --dataset ds1 --url-host storage.example.org
assert_no_call '^mc '
assert_no_call 'delete lease'
expect_failure 'could not create lease/filestash-publish' env "PULUMI_STACK=$stack" "STUB_LEASE_FORBIDDEN=1" "$just_bin" filestash publish --source alice/batch --dataset ds1 --url-host storage.example.org
echo '  test_publish_fails_when_lease_held: PASS'

expect_failure 'pass --url-host' env "PULUMI_STACK=$stack" "STUB_MC_KEYS=$keys" "$just_bin" filestash publish --source alice/batch --dataset ds1
out=$(run_recipe env "PULUMI_STACK=dcr-kube1" "STUB_MC_KEYS=table.csv" "$just_bin" filestash publish --source alice/batch --dataset ds1)
printf '%s' "$out" | rg -qF 'https://storage.dictycr.org/downloads/ds1/table.csv'
echo '  test_publish_url_host_from_gateway: PASS'

expect_failure 'Pass --confirm yes' env "PULUMI_STACK=$stack" "$just_bin" filestash unpublish-file --key ds1/table.csv
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash unpublish-file --key ds1/table.csv --confirm yes >/dev/null
rg -qF 'mc-args [rm] [minio/downloads/ds1/table.csv]' "$log_file"
echo '  test_unpublish_file_requires_confirm: PASS'

printf 'spec:\n  holderIdentity: alice@host:9\n' > "$stub_dir/lease-filestash-publish.yaml"
: > "$log_file"
expect_failure "lease holder is 'alice@host:9', not 'other'" env "PULUMI_STACK=$stack" "$just_bin" filestash release-lock --holder other
assert_no_call 'delete lease'
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash release-lock --holder alice@host:9 >/dev/null
[[ ! -e "$stub_dir/lease-filestash-publish.yaml" ]]
echo '  test_release_lock_checks_holder: PASS'

echo "=== 13. backup and restore ==="
assert_no_call() { if rg -q -- "$1" "$log_file"; then printf 'unexpected call matching %s:\n' "$1" >&2; cat "$log_file" >&2; exit 1; fi; }
line_of() { rg -n -m1 -- "$1" "$log_file" | cut -d: -f1; }
expect_failure "newest successful state backup age '7h'" "${vok[@]}" "STUB_BACKUP_AGE_H=7" "$just_bin" filestash verify
expect_failure "newest successful state backup age 'noneh'" env "PULUMI_STACK=$stack" "STUB_ABOUT=$about" "$just_bin" filestash verify
echo '  test_verify_fails_on_stale_backup: PASS'
expect_failure 'no velero.io/exclude-from-backup=true label' "${vok[@]}" "STUB_PVC_EXCLUDE=" "$just_bin" filestash verify
echo '  test_verify_requires_velero_exclusion: PASS'
expect_failure 'Secret filestash-backup-auth missing' "${vok[@]}" "STUB_NO_BACKUP_AUTH=1" "$just_bin" filestash verify
echo '  test_verify_requires_backup_secret: PASS'
cp "$cfg_file" "$tmp_dir/cfg.bak"
yq -y '.config."filestash:properties".backup.maxAgeHours = 0' "$tmp_dir/cfg.bak" > "$cfg_file"
expect_failure 'backup.maxAgeHours is unset' "${vok[@]}" "$just_bin" filestash verify
cp "$tmp_dir/cfg.bak" "$cfg_file"
echo '  test_verify_requires_max_age: PASS'

: > "$log_file"
out=$(run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash backup-now)
rg -q '^kubectl -n prod create job --from=cronjob/filestash-state-backup filestash-state-backup-now-' "$log_file"
printf '%s' "$out" | rg -q 'Snapshot 4f2a9c1b saved'
printf '%s' "$out" | rg -qF 'restore-state --in-place yes --snapshot 4f2a9c1b'
assert_no_call 'scale deployment'
assert_no_call 'velero'
expect_failure 'a backup job is running' env "PULUMI_STACK=$stack" "STUB_BACKUP_ACTIVE=pod/b1" "$just_bin" filestash backup-now
expect_failure 'state backup job' env "PULUMI_STACK=$stack" "STUB_BACKUP_FAIL=1" "$just_bin" filestash backup-now
echo '  test_backup_now_is_online_and_prints_snapshot: PASS'

# deploy-backup and configure-backup
: > "$log_file"
expect_failure 'has no properties.secret.resticPass' env "PULUMI_STACK=$stack" "$just_bin" filestash deploy-backup
assert_no_call 'stack up'
touch "$tmp_dir/sa.json"
printf '{"client_email":"sa-manager@test.iam.gserviceaccount.com"}' > "$tmp_dir/mgr.json"
printf '{"client_email":"kops-cluster-creator@test.iam.gserviceaccount.com"}' > "$tmp_dir/creator.json"
cb=(filestash configure-backup --project test --key-file "$tmp_dir/sa.json" --manager-key "$tmp_dir/mgr.json")
: > "$log_file"
expect_failure "does not match project 'other'" env "PULUMI_STACK=$stack" "$just_bin" filestash configure-backup --project other --key-file "$tmp_dir/sa.json" --manager-key "$tmp_dir/mgr.json"
assert_no_call 'stack config set'
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" "${cb[@]}" >/dev/null
rg -q 'add-iam-policy-binding test .*title=filestash-backup-bucket-writer' "$log_file"
[[ "$(rg -c '^gcloud ' "$log_file")" == "$(rg -c "^gcloud .* \[as $tmp_dir/mgr.json\]$" "$log_file")" ]]
assert_no_call 'config get-value account'
assert_no_call 'service-accounts create'
echo '  test_setup_sa_binds_manager_identity: PASS'
for key in resticPass gcsProject serviceAccountFile; do
    [[ "$(yq -r ".config.\"filestash-backup:properties\".secret.$key.secure" "$bcfg_file")" == "v1:enc" ]]
done
rg -q '^go-stdin-bytes properties.secret.resticPass 40$' "$log_file"
pass1=$(cat "$stub_dir/cfg-filestash-backup-secret.resticPass")
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" "${cb[@]}" >/dev/null
assert_no_call 'key properties.secret.resticPass'
[[ "$(cat "$stub_dir/cfg-filestash-backup-secret.resticPass")" == "$pass1" ]]
: > "$log_file"
expect_failure 'not sa-manager@test' env "PULUMI_STACK=$stack" "$just_bin" filestash configure-backup --project test --key-file "$tmp_dir/sa.json" --manager-key "$tmp_dir/creator.json"
assert_no_call '^gcloud '
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "STUB_SA_DESCRIBE=missing" "$just_bin" "${cb[@]}" >/dev/null
rg -q 'service-accounts create filestash-backup-sa' "$log_file"
: > "$log_file"
expect_failure 'PERMISSION_DENIED' env "PULUMI_STACK=$stack" "STUB_SA_DESCRIBE=denied" "$just_bin" "${cb[@]}"
assert_no_call 'service-accounts create'
assert_no_call 'add-iam-policy-binding'
echo '  test_setup_sa_classifies_describe_errors: PASS'
echo '  test_configure_backup_keeps_restic_password: PASS'
: > "$log_file"
expect_failure 'pvc/filestash-state not found' env "PULUMI_STACK=$stack" "$just_bin" filestash deploy-backup
assert_no_call 'stack up'
run_recipe env "PULUMI_STACK=$stack" "STUB_PVC_EXISTS=1" "$just_bin" filestash deploy-backup >/dev/null
rg -q "stack up --folder filestash-backup --stack $stack" "$log_file"
echo '  test_deploy_backup: PASS'

# restore-state: test restore
: > "$log_file"
expect_failure "is the live namespace 'prod'" env "PULUMI_STACK=$stack" "$just_bin" filestash restore-state --target-namespace prod
expect_failure "namespace 'filestash-restore-old' exists" env "PULUMI_STACK=$stack" "$just_bin" filestash restore-state --target-namespace filestash-restore-old
expect_failure "invalid --snapshot 'x;rm'" env "PULUMI_STACK=$stack" "$just_bin" filestash restore-state --target-namespace filestash-restore-new --snapshot 'x;rm'
expect_failure 'Secret filestash-backup-auth not found' env "PULUMI_STACK=$stack" "STUB_NO_BACKUP_AUTH=1" "$just_bin" filestash restore-state --target-namespace filestash-restore-new
assert_no_call 'create namespace'
echo '  test_restore_state_refuses_before_mutation: PASS'
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash restore-state --target-namespace filestash-restore-new --snapshot 4f2a9c1b >/dev/null
created=$(line_of 'kubectl create namespace filestash-restore-new')
netpol=$(line_of 'kubectl-applied NetworkPolicy')
copies=$(line_of 'kubectl-applied ConfigMap filestash-seed filestash-restore-new')
pvc=$(line_of 'kubectl-applied PersistentVolumeClaim filestash-state filestash-restore-new')
job=$(line_of 'kubectl-created Job filestash-state-restore')
deploy=$(line_of 'kubectl-applied Deployment filestash filestash-restore-new')
(( created < netpol && netpol < copies && copies < job && pvc < job && job < deploy )) \
    || { echo "test restore order wrong: $created $netpol $copies $pvc $job $deploy" >&2; exit 1; }
rg -q 'kubectl-applied Secret filestash filestash-restore-new' "$log_file"
rg -q 'kubectl-applied Secret filestash-backup-auth filestash-restore-new' "$log_file"
rg -q 'value: "gs:restic-filestash-backup-test:/"' "$stub_dir/created-filestash-state-restore.yaml"
rg -q 'value: "4f2a9c1b"' "$stub_dir/created-filestash-state-restore.yaml"
rg -q 'claimName: filestash-state' "$stub_dir/created-filestash-state-restore.yaml"
assert_no_call 'scale deployment'
echo '  test_restore_state_test_restore_order: PASS'

# restore-state: in place
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash restore-state --in-place yes >/dev/null
suspend=$(line_of 'patch cronjob filestash-state-backup .*"suspend":true')
check=$(line_of 'kubectl-created Job filestash-state-check')
down=$(line_of 'scale deployment/filestash --replicas=0')
job=$(line_of 'kubectl-created Job filestash-state-restore')
up=$(line_of 'scale deployment/filestash --replicas=1')
(( suspend < check && check < down && down < job && job < up )) || { echo "in-place order wrong" >&2; exit 1; }
rg -q 'patch cronjob filestash-state-backup .*"suspend":false' "$log_file"
rg -q 'delete lease filestash-maintenance' "$log_file"
! rg -q 'claimName' "$stub_dir/created-filestash-state-check.yaml" || { echo "the check job must not mount the state PVC" >&2; exit 1; }
rg -q 'value: "?latest"?$' "$stub_dir/created-filestash-state-check.yaml"
echo '  test_restore_state_in_place: PASS'
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "STUB_SUSPEND=true" "$just_bin" filestash restore-state --in-place yes >/dev/null
assert_no_call '"suspend":false'
echo '  test_restore_state_in_place_restores_suspend_value: PASS'
: > "$log_file"
expect_failure 'a backup job is running' env "PULUMI_STACK=$stack" 'STUB_JOBS_JSON={"items":[{"status":{}}]}' "$just_bin" filestash restore-state --in-place yes
assert_no_call 'replicas=0'
expect_failure "snapshot '4f2a9c1b' fails its checks. Nothing changed" env "PULUMI_STACK=$stack" "STUB_CHECK_FAIL=1" "$just_bin" filestash restore-state --in-place yes --snapshot 4f2a9c1b
assert_no_call 'replicas=0'
expect_failure 'another run holds lease/filestash-maintenance' env "PULUMI_STACK=$stack" "STUB_LEASE_HELD=1" "STUB_LEASE_HOLDER=x@y:1" "$just_bin" filestash restore-state --in-place yes
echo '  test_restore_state_in_place_preflight: PASS'
: > "$log_file"
expect_failure 'the restore job failed' env "PULUMI_STACK=$stack" "STUB_RESTORE_FAIL=1" "$just_bin" filestash restore-state --in-place yes
assert_no_call 'replicas=1'
rg -q '"suspend":false' "$log_file"
echo '  test_restore_state_in_place_keeps_down_on_failure: PASS'

expect_failure "is not a restore test namespace" env "PULUMI_STACK=$stack" "STUB_NS_EXISTS=other" "$just_bin" filestash restore-cleanup --target-namespace other
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "STUB_NS_EXISTS=filestash-restore-done" "STUB_NS_LABEL=true" "$just_bin" filestash restore-cleanup --target-namespace filestash-restore-done >/dev/null
rg -q '^kubectl delete namespace filestash-restore-done$' "$log_file"
echo '  test_restore_cleanup_checks_label: PASS'

echo "=== 14. operations ==="
newd=sha256:2222222222222222222222222222222222222222222222222222222222222222
: > "$log_file"
run_recipe "${vok[@]}" "$just_bin" filestash upgrade --tag latest --digest "$newd" > "$tmp_dir/upgrade.out" 2>&1 || { cat "$tmp_dir/upgrade.out"; tail -30 "$log_file"; exit 1; }
backup=$(line_of 'create job --from=cronjob/filestash-state-backup')
prev=$(line_of 'key properties.previousImage.digest --value sha256:1111')
pin=$(line_of "key properties.image.digest --value $newd")
up=$(line_of 'stack up --folder filestash')
(( backup < prev && prev < pin && pin < up )) || { echo "upgrade order wrong: $backup $prev $pin $up" >&2; exit 1; }
echo '  test_upgrade_runs_backup_first: PASS'
expect_failure 'already pins' env "PULUMI_STACK=$stack" "$just_bin" filestash upgrade --tag latest --digest "$newd"
expect_failure 'pass --digest' env "PULUMI_STACK=$stack" "$just_bin" filestash upgrade --tag latest --digest latest

: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash rollback >/dev/null
[[ "$(yq -r '.config."filestash:properties".image.digest' "$cfg_file")" == sha256:1111111111111111111111111111111111111111111111111111111111111111 ]]
cp "$cfg_file" "$tmp_dir/cfg.bak"
yq -y 'del(.config."filestash:properties".previousImage)' "$tmp_dir/cfg.bak" > "$cfg_file"
: > "$log_file"
expect_failure 'no previousImage' env "PULUMI_STACK=$stack" "$just_bin" filestash rollback
assert_no_call 'stack up'
cp "$tmp_dir/cfg.bak" "$cfg_file"
echo '  test_rollback_requires_previous_image: PASS'

expect_failure 'pass --older-than' env "PULUMI_STACK=$stack" "$just_bin" filestash purge-uploads
echo '  test_purge_uploads_requires_age: PASS'
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash purge-uploads --older-than 90 >/dev/null
rg -qF 'mc-args [find] [minio/uploads] [--older-than] [90d]' "$log_file"
assert_no_call '^mc rm'
rg -q 'jsonpath=\{.data.operatorAccessKey\}' "$log_file"
: > "$log_file"
run_recipe env "PULUMI_STACK=$stack" "$just_bin" filestash purge-uploads --older-than 90 --confirm yes >/dev/null
rg -qF 'mc-args [rm] [--recursive] [--force] [--older-than] [90d] [minio/uploads]' "$log_file"
echo '  test_purge_uploads_dry_run_default: PASS'

share_db="$tmp_dir/share.sql"
sqlite3 "$share_db" "CREATE TABLE Share(id, related_backend, related_path, params, auth);
INSERT INTO Share VALUES('pub1','b','/downloads/','{\"can_read\":true,\"can_upload\":false,\"can_write\":false}','a');
INSERT INTO Share VALUES('up1','b','/uploads/alice/','{\"can_read\":false,\"can_upload\":true,\"can_write\":false,\"users\":\"alice@example.org\",\"expire\":1999999999000}','a');"
run_recipe env "PULUMI_STACK=$stack" "STUB_SHARE_DB=$share_db" "$just_bin" filestash audit-shares | rg -q 'All share links follow the rules'
sqlite3 "$share_db" "INSERT INTO Share VALUES('up2','b','/uploads/bob/','{\"can_read\":false,\"can_upload\":true,\"can_write\":false,\"users\":\"bob@example.org\"}','a');"
expect_failure 'share rule break' env "PULUMI_STACK=$stack" "STUB_SHARE_DB=$share_db" "$just_bin" filestash audit-shares
echo '  test_audit_shares_flags_no_expiry: PASS'

run_recipe "$just_bin" filestash probe-public --host storage.test --key ds1/table.csv | rg -q 'all checks passed'
expect_failure 'anonymous Range GET: got 200' env "STUB_RANGE_CODE=200" "$just_bin" filestash probe-public --host storage.test --key ds1/table.csv
: > "$log_file"
run_recipe "$just_bin" filestash probe-public --host storage.test --key ds1/table.csv >/dev/null
assert_no_call 'DELETE https://storage.test/downloads/ds1/table.csv'
rg -q 'X DELETE https://storage.test/downloads/probe-[0-9]+-absent' "$log_file"
echo '  test_probe_public: PASS'
: > "$log_file"
run_recipe "$just_bin" filestash probe-admin --host files.test | rg -q 'no admin path variant'
[[ "$(rg -c '^curl .*https://files.test' "$log_file")" == "$(rg -c '^curl .*--path-as-is.*https://files.test' "$log_file")" ]]
rg -qF 'https://files.test/%61dmin' "$log_file"
echo '  test_probe_admin_uses_path_as_is: PASS'
expect_failure 'reached admin content' env "STUB_ADMIN_LEAK=https://files.test/%61dmin" "$just_bin" filestash probe-admin --host files.test
rg -qF 'https://files.test/%61dmin/api/session' "$log_file"
rg -qF 'https://files.test/admin%2Fapi/session' "$log_file"
expect_failure 'reached admin content' env "STUB_ADMIN_JSON_LEAK=https://files.test/admin%2Fapi/config" "$just_bin" filestash probe-admin --host files.test
expect_failure 'no response' env "STUB_ADMIN_DOWN=1" "$just_bin" filestash probe-admin --host files.test
expect_failure 'did not get the block redirect' env "STUB_ADMIN_NOREDIRECT=https://files.test/admin/api/config" "$just_bin" filestash probe-admin --host files.test
expect_failure 'encoded admin path not blocked' env "STUB_ADMIN_NOREDIRECT=https://files.test/%61dmin" "$just_bin" filestash probe-admin --host files.test
echo '  test_probe_admin_detects_leak: PASS'

echo "All filestash recipe contract tests passed."
