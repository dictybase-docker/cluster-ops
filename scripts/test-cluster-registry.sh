#!/usr/bin/env bash
# Contract tests for `just gcp-cluster registry-show` — the read path over the
# cluster registry (config/clusters/<cluster>.yaml).
#
# The recipe is read-only and cloud-free, so the tests run against throwaway
# registry entries inside config/clusters/ (removed on exit) plus the real
# dcr-kube1 entry. They assert the fail-closed contract: a missing file, a
# missing key, a non-GCS state URI, or an env-var reference in place of a
# concrete bucket each fail with a named cause.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

cluster="test-registry-$$"
entry="config/clusters/${cluster}.yaml"
cleanup() { rm -f "${entry}"; }
trap cleanup EXIT

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

# assert_fail <description> <expected-substring> -- command...
assert_fail() {
    local desc="$1"; shift
    local want="$1"; shift
    if out=$(just gcp-cluster registry-show --cluster "${cluster}" 2>&1); then
        fail "${desc}: expected failure, got success:\n${out}"
    fi
    if ! printf '%s' "${out}" | grep -q "${want}"; then
        fail "${desc}: failure did not name the cause ('${want}'):\n${out}"
    fi
    echo "  ${desc}: PASS (named cause: ${want})"
}

cat > "${entry}" <<'YAML'
cluster: test-registry
stack: test-registry
kops_state: gs://kops-state-test-registry
gcp_project: test-project
kms_secrets_provider: gcpkms://projects/test-project/locations/us-central1/keyRings/test/cryptoKeys/pulumi
pulumi_state: gs://pulumi-state-test-registry
namespace: test
ci_env: TEST
kops_version: 1.36.1
kubectl_version: 1.35.8
pulumi_version: 3.255.0
YAML

echo "=== 1. valid entry prints every required key ==="
out=$(just gcp-cluster registry-show --cluster "${cluster}") || fail "valid entry rejected:\n${out}"
for key in cluster= stack= kops_state= gcp_project= kms_secrets_provider= \
    pulumi_state= namespace= ci_env= kops_version= kubectl_version= pulumi_version=; do
    printf '%s' "${out}" | grep -q "  ${key}" || fail "output missing '${key}':\n${out}"
done
echo "  valid entry: PASS (all 11 keys printed)"

echo "=== 2. real dcr-kube1 entry passes unchanged ==="
just gcp-cluster registry-show --cluster dcr-kube1 >/dev/null || fail "dcr-kube1 registry entry rejected"
echo "  dcr-kube1: PASS"

echo "=== 3. missing registry file fails with the expected path ==="
rm -f "${entry}"
assert_fail "missing file" "no registry entry for cluster '${cluster}'"

echo "=== 4. missing key fails naming the key ==="
sed '/^ci_env:/d' /dev/null 2>/dev/null || true
cat > "${entry}" <<'YAML'
cluster: test-registry
stack: test-registry
kops_state: gs://kops-state-test-registry
gcp_project: test-project
kms_secrets_provider: gcpkms://projects/test-project/locations/us-central1/keyRings/test/cryptoKeys/pulumi
pulumi_state: gs://pulumi-state-test-registry
namespace: test
kops_version: 1.36.1
kubectl_version: 1.35.8
pulumi_version: 3.255.0
YAML
assert_fail "missing key" "ci_env"

echo "=== 5. env-var reference in place of a bucket fails ==="
cat > "${entry}" <<'YAML'
cluster: test-registry
stack: test-registry
kops_state: gs://kops-state-test-registry
gcp_project: test-project
kms_secrets_provider: gcpkms://projects/test-project/locations/us-central1/keyRings/test/cryptoKeys/pulumi
pulumi_state: ${PULUMI_STATE_STORAGE}
namespace: test
ci_env: TEST
kops_version: 1.36.1
kubectl_version: 1.35.8
pulumi_version: 3.255.0
YAML
assert_fail "env reference" "not an env reference"

echo "=== 6. non-GCS state URI fails ==="
cat > "${entry}" <<'YAML'
cluster: test-registry
stack: test-registry
kops_state: s3://kops-state-test-registry
gcp_project: test-project
kms_secrets_provider: gcpkms://projects/test-project/locations/us-central1/keyRings/test/cryptoKeys/pulumi
pulumi_state: gs://pulumi-state-test-registry
namespace: test
ci_env: TEST
kops_version: 1.36.1
kubectl_version: 1.35.8
pulumi_version: 3.255.0
YAML
assert_fail "non-GCS URI" "must be a gs:// URI"

echo "cluster registry contract tests PASSED"
