#!/usr/bin/env bash
# Contract tests for the Pulumi Automation wrappers:
#   - bootstrap-service → `deploy service` (composite: preflight→…→up→rollout)
#   - ensure-stack → `stack ensure`
#   - failing converge exits non-zero from each wrapper
#   - deploy-backend-services batch path runs `deploy backend` after the
#     scaffolds and before the deployer-secret publish (order contract)
#
# go, pulumi, yq and kubectl are mocked on ONE shared argv log. Go-side
# behavior (gate, redaction, serial order) is covered by Go unit tests.
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
exit "${MOCK_GO_EXIT:-0}"
MOCK
cat > "${mock_bin}/pulumi" <<'MOCK'
#!/usr/bin/env bash
echo "pulumi $*" >> "${MOCK_CALLS_LOG}"
exit 0
MOCK
cat > "${mock_bin}/yq" <<'MOCK'
#!/usr/bin/env bash
case "$*" in
    *appName*) echo "order" ;;
    *namespace*) echo "dev" ;;
    *image.name*) echo "dictybase/${MOCK_PROJECT:-modware-order}" ;;
    *) echo "t1" ;;
esac
MOCK
cat > "${mock_bin}/kubectl" <<'MOCK'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "$*" in
    *jsonpath*) echo "dictybase/${MOCK_PROJECT:-modware-order}:${MOCK_TAG:-v1}" ;;
    *) exit 1 ;;
esac
MOCK
chmod +x "${mock_bin}"/*
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

echo "=== 1. bootstrap-service delegates to deploy service ==="
: > "${calls}"
just gcp-pulumi bootstrap-service --folder modware-order --stack s1 --image-tag v1 >/dev/null 2>&1 \
    || fail "bootstrap-service failed: $(cat "${calls}")"
grep -q "deploy service .*--folder modware-order --stack s1 --image-tag v1" "${calls}" \
    || fail "deploy service argv wrong: $(cat "${calls}")"
echo "  composite argv: PASS"

echo "=== 2. ensure-stack delegates to stack ensure ==="
mkdir -p "${tmp}/proj"
: > "${tmp}/proj/Pulumi.s1.yaml"
: > "${calls}"
just gcp-pulumi ensure-stack --folder "${tmp}/proj" --stack s1 >/dev/null 2>&1 \
    || fail "ensure-stack failed: $(cat "${calls}")"
grep -q "stack ensure .*--folder ${tmp}/proj --stack s1" "${calls}" \
    || fail "stack ensure argv wrong: $(cat "${calls}")"
echo "  ensure argv: PASS"

echo "=== 3. a failing converge exits non-zero ==="
if MOCK_GO_EXIT=1 just gcp-pulumi bootstrap-service --folder modware-order --stack s1 --image-tag v1 >/dev/null 2>&1; then
    fail "bootstrap-service exited 0 although the deploy failed"
fi
echo "  exit propagated: PASS"

echo "=== 4. deploy-backend-services batches deploys after scaffolds ==="
plan=$(just --dry-run ci deploy-backend-services --stack s1 --image-tag v1 --services order 2>&1 || true)
echo "${plan}" | grep -q "deploy backend" || fail "batch deploy missing from the plan: ${plan}"
scaffold_line=$(echo "${plan}" | grep -n "scaffold" | head -1 | cut -d: -f1)
deploy_line=$(echo "${plan}" | grep -n "deploy backend" | head -1 | cut -d: -f1)
secret_line=$(echo "${plan}" | grep -n "set-deploy-secret" | head -1 | cut -d: -f1)
[ "${scaffold_line}" -lt "${deploy_line}" ] || fail "deploy backend must run after the scaffolds"
[ "${deploy_line}" -lt "${secret_line}" ] || fail "deploy backend must run before the deployer-secret publish"
echo "  batch order: PASS"

echo "=== 5. stack preview and create-resource pass --folder and --stack ==="
: > "${calls}"
just gcp-pulumi preview --folder arangodb-single --stack s1 >/dev/null 2>&1 \
    || fail "preview failed: $(cat "${calls}")"
grep -q "stack preview --folder arangodb-single --stack s1" "${calls}" \
    || fail "stack preview argv wrong: $(cat "${calls}")"
: > "${calls}"
just gcp-pulumi create-resource --folder arangodb-single --stack s1 >/dev/null 2>&1 \
    || fail "create-resource failed: $(cat "${calls}")"
grep -q "stack up --folder arangodb-single --stack s1" "${calls}" \
    || fail "stack up argv wrong: $(cat "${calls}")"
echo "  preview/create-resource argv: PASS"

echo "pulumi automation contract tests PASSED"
