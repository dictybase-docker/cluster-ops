#!/usr/bin/env bash
# Contract tests for the GCP converge wrappers:
#   - `just gcp-pulumi pulumi-gcs-setup` → `cluster-ops bucket converge` BEFORE `pulumi login`
#   - `just gcp-role add-role-to-sa` → `cluster-ops iam add-binding`
#   - `just gcp-cluster delete-state-bucket` → `cluster-ops bucket delete --confirm yes`
#     only after the operator types `delete` at the prompt
#   - failing converge exits non-zero from each wrapper
#
# go and pulumi are mocked on PATH into ONE shared argv call log.
# Go-side behavior (converge semantics) is covered by Go unit tests.
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
cat > "${mock_bin}/gcloud" <<'MOCK'
#!/usr/bin/env bash
echo "gcloud $*" >> "${MOCK_CALLS_LOG}"
exit 0
MOCK
cat > "${mock_bin}/jq" <<'MOCK'
#!/usr/bin/env bash
echo "test-project"
MOCK
chmod +x "${mock_bin}"/*
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

echo "=== 1. pulumi-gcs-setup converges before pulumi login ==="
printf '{"project_id": "test-project"}' > "${tmp}/fake-key.json"
: > "${calls}"
out=$(just gcp-pulumi pulumi-gcs-setup --sa-json-path "${tmp}/fake-key.json" --gcs-bucket t1 2>&1) \
    || fail "pulumi-gcs-setup failed: ${out}"
grep -q "bucket converge" "${calls}" || fail "bucket converge not invoked: $(cat "${calls}")"
grep -q "pulumi login" "${calls}" || fail "pulumi login not invoked: $(cat "${calls}")"
converge_line=$(grep -n "bucket converge" "${calls}" | head -1 | cut -d: -f1)
login_line=$(grep -n "pulumi login" "${calls}" | head -1 | cut -d: -f1)
[ "${converge_line}" -lt "${login_line}" ] || fail "converge must precede pulumi login"
echo "  converge-before-login: PASS"

echo "=== 2. add-role-to-sa converges the project binding ==="
: > "${calls}"
just gcp-role add-role-to-sa --project test-project --sa-name svc --role roles/viewer >/dev/null 2>&1 \
    || fail "add-role-to-sa failed: $(cat "${calls}")"
grep -q "iam add-binding --project test-project --member serviceAccount:svc@test-project.iam.gserviceaccount.com --role roles/viewer" "${calls}" \
    || fail "iam add-binding argv wrong: $(cat "${calls}")"
echo "  binding argv: PASS"

echo "=== 3. a failing converge exits non-zero ==="
for recipe in "gcp-role add-role-to-sa --project test-project --sa-name svc --role roles/viewer"; do
    if MOCK_GO_EXIT=1 just ${recipe} >/dev/null 2>&1; then
        fail "wrapper exited 0 although the converge failed: ${recipe}"
    fi
done
echo "  exit propagated: PASS"

echo "=== 4. delete-state-bucket gates the delete on the typed confirmation ==="
: > "${calls}"
if printf 'no\n' | just gcp-cluster delete-state-bucket --cluster t1 --confirm yes >/dev/null 2>&1; then
    fail "delete-state-bucket deleted although the prompt was declined"
fi
grep -q -- "--confirm yes" "${calls}" && fail "a confirmed delete ran although the prompt was declined: $(cat "${calls}")"
printf 'delete\n' | just gcp-cluster delete-state-bucket --cluster t1 --confirm yes >/dev/null 2>&1 \
    || fail "confirmed delete failed: $(cat "${calls}")"
grep -q "bucket delete" "${calls}" || fail "bucket delete not invoked after confirmation: $(cat "${calls}")"
grep -q -- "--confirm yes" "${calls}" || fail "delete must carry --confirm yes: $(cat "${calls}")"
echo "  confirm gate: PASS"

echo "gcp converge contract tests PASSED"
