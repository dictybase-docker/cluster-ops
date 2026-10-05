#!/usr/bin/env bash
# Contract tests for `just gcp-pulumi bootstrap-service`.
#
# Nested recipes and cloud CLIs are intercepted: `just` itself is mocked on
# PATH so every `just gcp-pulumi …` call the composite makes is recorded,
# kubectl is mocked for rollout + image check, and pulumi runs behind the
# mocked nested recipes. Asserts the two things a preflight-gated composite
# cannot get wrong: a prereq failure aborts before any mutation, and the
# happy path runs the steps in the fixed order.
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

# --- mock just: record nested recipe calls ---------------------------------
# The recipe calls `just gcp-pulumi <recipe> …`. Intercept only those; the
# outer `just` invocation runs the real binary (mock detects by args).
cat > "${mock_bin}/just" <<'EOF'
#!/usr/bin/env bash
echo "just $*" >> "${MOCK_CALLS_LOG}"
# prereq gate: honor the injected mode
if [ "$1 $2" = "gcp-pulumi check-backend-prereqs" ] && [ "${MOCK_PREREQ_MODE:-ok}" = "fail" ]; then
    echo "MISSING: namespace prod" >&2
    exit 1
fi
exit 0
EOF
# --- mock kubectl ----------------------------------------------------------
cat > "${mock_bin}/kubectl" <<'EOF'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "$*" in
    "rollout status deploy/order-api-server") echo "deployment successfully rolled out" ;;
    *"get deploy/order-api-server"*) echo -n "dictybase/modware-order:${MOCK_IMAGE_TAG:-v1.0.0}" ;;
esac
exit 0
EOF
chmod +x "${mock_bin}/just" "${mock_bin}/kubectl"
export MOCK_CALLS_LOG="${calls}"
# Resolve the real just BEFORE the mock shadows it on PATH.
REAL_JUST="$(command -v just)"
export PATH="${mock_bin}:${PATH}"

# --- fixtures ---------------------------------------------------------------
folder="${tmp}/modware-order"
mkdir -p "${folder}"
cat > "${folder}/Pulumi.dcr-kube1.yaml" <<'YAML'
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

line_of() { grep -n -e "$1" "${calls}" | head -n1 | cut -d: -f1; }

echo "=== 1. prereq failure aborts before any mutation ==="
: > "${calls}"
export MOCK_PREREQ_MODE=fail
if out=$("${REAL_JUST}" gcp-pulumi bootstrap-service --stack dcr-kube1 --folder "${folder}" --image-tag v1.0.0 2>&1); then
    fail "prereq failure did not abort: ${out}"
fi
printf '%s' "${out}" | grep -q "MISSING: namespace prod" || fail "prereq cause missing:\n${out}"
# Only the gate may have run: no ensure-stack/set-config/preview/create-resource
for forbidden in "ensure-stack" "set-config" "preview" "create-resource" "rollout status"; do
    if grep -q "${forbidden}" "${calls}"; then
        fail "mutation-path call '${forbidden}' ran after a prereq failure:\n$(cat "${calls}")"
    fi
done
echo "  prereq abort: PASS (gate only, zero deploy-path calls)"

echo "=== 2. happy path runs steps in the fixed order ==="
: > "${calls}"
export MOCK_PREREQ_MODE=ok
export MOCK_IMAGE_TAG=v1.0.0
out=$("${REAL_JUST}" gcp-pulumi bootstrap-service --stack dcr-kube1 --folder "${folder}" --image-tag v1.0.0) \
    || fail "happy path failed: ${out}"
printf '%s' "${out}" | grep -q "Deployed order-api-server in prod with image dictybase/modware-order:v1.0.0" \
    || fail "missing deployed line:\n${out}"

gate=$(line_of "check-backend-prereqs")       || fail "gate did not run"
ensure=$(line_of "ensure-stack")              || fail "ensure-stack did not run"
settag=$(line_of "set-config")                || fail "set-config did not run"
preview=$(line_of "gcp-pulumi preview")       || fail "preview did not run"
up=$(line_of "create-resource")               || fail "create-resource did not run"
rollout=$(line_of "rollout status")          || fail "rollout did not run"
image_check=$(line_of "get deploy/order-api-server") || fail "image check did not run"
[ "${gate}" -lt "${ensure}" ] || fail "gate ran after ensure-stack"
[ "${ensure}" -lt "${settag}" ] || fail "ensure-stack ran after set-config"
[ "${settag}" -lt "${preview}" ] || fail "set-config ran after preview"
[ "${preview}" -lt "${up}" ] || fail "preview ran after update"
[ "${up}" -lt "${rollout}" ] || fail "update ran after rollout"
[ "${rollout}" -lt "${image_check}" ] || fail "rollout ran after image check"
grep -q -- "--plaintext yes" "${calls}" || fail "image-tag set without --plaintext"
grep -q -- "--key properties.image.tag --value v1.0.0" "${calls}" || fail "image-tag key/value wrong"
echo "  fixed order: PASS (gate → ensure → tag → preview → up → rollout → image check)"

echo "backend bootstrap-service contract tests PASSED"
