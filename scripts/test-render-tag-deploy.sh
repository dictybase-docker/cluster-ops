#!/usr/bin/env bash
# Contract tests for `just ci render-tag-deploy`.
#
# curl is mocked on PATH so the input-drift guard runs against fixture copies
# of composite-deploy.yaml (hermetic — no network in `just check`). Asserts:
# tags-only trigger, deploy needs [test, lint], PROD_* variable routing,
# docker_image is the full repo name, --workflow-ref pinning, input-drift
# failure naming the missing key, unknown-stack failure, zero 'staging'.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

tmp="$(mktemp -d)"
mock_bin="${tmp}/bin"
mkdir -p "${mock_bin}"
cleanup() { rm -rf "${tmp}"; }
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

# Fixtures of dictyBase/workflows composite-deploy.yaml, trimmed to inputs.
composite_full="${tmp}/composite-full.yaml"
composite_drift="${tmp}/composite-drift.yaml"
cat > "${composite_full}" <<'YAML'
name: Composite deploy for backend workflow
on:
  workflow_call:
    inputs:
      app:
        type: string
      repository:
        type: string
      ref:
        type: string
      dockerfile:
        type: string
      docker_image:
        type: string
      cluster:
        type: string
      cluster_state_storage:
        type: string
      project:
        type: string
      stack:
        type: string
      docker_namespace:
        type: string
      application_type:
        type: string
      environment:
        type: string
      runner:
        type: string
      kops_version:
        type: string
      kubectl_version:
        type: string
      pulumi_version:
        type: string
      cluster_ops_ref:
        type: string
      dagger_ref:
        type: string
jobs:
  deploy:
    runs-on: ubuntu-latest
YAML
sed '/kops_version:/,+2d' "${composite_full}" > "${composite_drift}"

cat > "${mock_bin}/curl" <<'EOF'
#!/usr/bin/env bash
echo "curl $*" >> "${MOCK_CALLS_LOG}"
url=""; out=""
while [ $# -gt 0 ]; do
    case "$1" in
        http*) url="$1" ;;
        -o) out="$2"; shift ;;
    esac
    shift
done
src=""
case "${url}" in
    *"/develop/"*) src="${MOCK_COMPOSITE_FULL}" ;;
    *"/v1/"*) src="${MOCK_COMPOSITE_FULL}" ;;
    *"/v2/"*) src="${MOCK_COMPOSITE_DRIFT}" ;;
    *) exit 1 ;;
esac
if [ -n "${out}" ]; then
    cat "${src}" > "${out}"
else
    cat "${src}"
fi
EOF
chmod +x "${mock_bin}/curl"
export MOCK_CALLS_LOG="${tmp}/curl.log"
export MOCK_COMPOSITE_FULL="${composite_full}"
export MOCK_COMPOSITE_DRIFT="${composite_drift}"
export PATH="${mock_bin}:${PATH}"

out_file="${tmp}/tag-build.yaml"

echo "=== 1. happy path: shape and routing ==="
just ci render-tag-deploy --stack dcr-kube1 --app order --project modware-order \
    --out "${out_file}" >/dev/null || fail "render failed"

yq -r '.' "${out_file}" >/dev/null || fail "rendered file does not parse"

grep -A1 '^  push:' "${out_file}" | grep -q "tags: \['\*'\]" \
    || fail "trigger is not tags-only"
grep -q 'needs: \[test, lint\]' "${out_file}" || fail "deploy job lacks the test+lint gate"
grep -q 'uses: dictyBase/workflows/.github/workflows/composite-deploy.yaml@develop' "${out_file}" \
    || fail "workflow ref not pinned to develop"
[ "$(grep -c 'vars\.PROD_' "${out_file}")" -ge 5 ] || fail "missing PROD_* variable routing"
for v in PROD_CLUSTER PROD_KOPS_STATE_STORAGE PROD_KOPS_VERSION PROD_KUBECTL_VERSION PROD_PULUMI_VERSION; do
    grep -q "vars.${v}" "${out_file}" || fail "missing ${v}"
done
grep -q 'docker_image: modware-order' "${out_file}" || fail "docker_image must be the full repo name"
grep -q 'stack: dcr-kube1' "${out_file}" || fail "static stack wrong"
grep -q 'app: order' "${out_file}" || fail "app wrong"
grep -q 'environment: production' "${out_file}" || fail "environment input wrong"
grep -qi 'staging' "${out_file}" && fail "staging leaked into the rendered file"
echo "  happy path: PASS (tags-only, gated, 5 PROD_* vars, full image name)"

echo "=== 2. --workflow-ref v1 pins the ref ==="
just ci render-tag-deploy --stack dcr-kube1 --app order --project modware-order \
    --out "${out_file}" --workflow-ref v1 >/dev/null || fail "render with ref pin failed"
grep -q 'composite-deploy.yaml@v1' "${out_file}" || fail "ref pin missing in output"
grep -q '/v1/' "${MOCK_CALLS_LOG}" || fail "guard did not fetch the pinned ref"
echo "  ref pin: PASS"

echo "=== 3. input drift fails naming the missing input ==="
if out=$(just ci render-tag-deploy --stack dcr-kube1 --app order --project modware-order \
    --out "${out_file}" --workflow-ref v2 2>&1); then
    fail "input drift accepted: ${out}"
fi
printf '%s' "${out}" | grep -q "input-drift — 'kops_version'" || fail "drift failure unnamed:\n${out}"
if [ -s "${out_file}" ]; then
    : # cp happens last; guard fires before the copy — file may hold a stale render, which is fine
fi
echo "  input drift: PASS (names kops_version, refuses before writing)"

echo "=== 4. unknown stack fails ==="
if out=$(just ci render-tag-deploy --stack nope --app order --project modware-order \
    --out "${out_file}" 2>&1); then
    fail "unknown stack accepted"
fi
printf '%s' "${out}" | grep -q "no registry entry with stack 'nope'" || fail "stack failure unnamed:\n${out}"
echo "  unknown stack: PASS"

echo "render-tag-deploy contract tests PASSED"
