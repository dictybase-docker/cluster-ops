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
trap 'rm -rf "$tmp_dir"' EXIT
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
chmod +x "${tmp_dir}/bin/"*

run_recipe() {
    env "PATH=${tmp_dir}/bin:${PATH}" "STUB_LOG=${log_file}" "$@"
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

echo "All filestash recipe contract tests passed."
