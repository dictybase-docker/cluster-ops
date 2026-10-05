#!/usr/bin/env bash
# Contract tests for `just gcp-cluster register-cluster`.
#
# Fixtures are a throwaway .env.<env>.<cluster> + .tool-versions.<env>.<cluster>
# pair at the repo root (both gitignored patterns) and a throwaway registry
# entry under config/clusters/, all removed on exit. The real dcr-kube1 entry
# proves the overwrite refusal. Asserts: values derive from the env file and
# tool manifest (v-prefix normalized), namespace/ci_env defaults and overrides,
# the generated entry passes registry-show, and every missing source fails
# with a named cause.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

env_name="testreg"
cluster="reg-cluster-$$"
env_file=".env.${env_name}.${cluster}"
manifest=".tool-versions.${env_name}.${cluster}"
entry="config/clusters/${cluster}.yaml"
cleanup() {
    rm -f "${env_file}" "${manifest}" "${entry}"
}
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

write_env_fixture() { # write_env_fixture [extra sed on manifest]
    cat > "${env_file}" <<EOF
PROJECT_ID=proj-${cluster}
PULUMI_SECRET_PROVIDER=gcpkms://projects/proj-${cluster}/locations/us-central1/keyRings/${cluster}/cryptoKeys/pulumi
PULUMI_BACKEND_URL=gs://pulumi-state-${cluster}
PULUMI_STACK=${cluster}
ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=${manifest}
EOF
    printf 'kubectl 1.35.8\nkops v1.36.1\npulumi 3.255.0\n' > "${manifest}"
}

yq_get() { yq -r "$2" "$1"; }

echo "=== 1. happy path derives every value and validates ==="
write_env_fixture
out=$(just gcp-cluster register-cluster --env "${env_name}" --cluster "${cluster}") \
    || fail "register failed:\n${out}"

[ -f "${entry}" ] || fail "registry entry not created"
[ "$(yq_get "${entry}" '.cluster')" = "${cluster}" ] || fail "cluster key wrong"
[ "$(yq_get "${entry}" '.stack')" = "${cluster}" ] || fail "stack not from env PULUMI_STACK"
[ "$(yq_get "${entry}" '.kops_state')" = "gs://kops-state-${cluster}" ] || fail "kops_state convention wrong"
[ "$(yq_get "${entry}" '.gcp_project')" = "proj-${cluster}" ] || fail "project not from env file"
[ "$(yq_get "${entry}" '.kms_secrets_provider')" = "gcpkms://projects/proj-${cluster}/locations/us-central1/keyRings/${cluster}/cryptoKeys/pulumi" ] || fail "kms not from env file"
[ "$(yq_get "${entry}" '.pulumi_state')" = "gs://pulumi-state-${cluster}" ] || fail "pulumi_state not from env file"
[ "$(yq_get "${entry}" '.namespace')" = "${env_name}" ] || fail "namespace default (env name) wrong"
[ "$(yq_get "${entry}" '.ci_env')" = "TESTREG" ] || fail "ci_env default (uppercased env) wrong"
[ "$(yq_get "${entry}" '.kops_version')" = "1.36.1" ] || fail "kops version v-prefix not stripped"
[ "$(yq_get "${entry}" '.kubectl_version')" = "1.35.8" ] || fail "kubectl version wrong"
[ "$(yq_get "${entry}" '.pulumi_version')" = "3.255.0" ] || fail "pulumi version wrong"
printf '%s' "${out}" | grep -q "Registry entry: ${entry}" || fail "registry-show validation did not run on the new entry"
printf '%s' "${out}" | grep -q "Next: just gcp-pulumi update --folder namespace-bootstrap" || fail "next-step line missing"
echo "  happy path: PASS (11 keys derived, v-prefix stripped, validated)"

echo "=== 2. namespace / ci-env / kops-state overrides ==="
rm -f "${entry}"
just gcp-cluster register-cluster --env "${env_name}" --cluster "${cluster}" \
    --namespace prod --ci-env PROD2 --kops-state "gs://alt-state-${cluster}" >/dev/null \
    || fail "override run failed"
[ "$(yq_get "${entry}" '.namespace')" = "prod" ] || fail "namespace override not applied"
[ "$(yq_get "${entry}" '.ci_env')" = "PROD2" ] || fail "ci-env override not applied"
[ "$(yq_get "${entry}" '.kops_state')" = "gs://alt-state-${cluster}" ] || fail "kops-state override not applied"
echo "  overrides: PASS"

echo "=== 3. overwrite refusal (throwaway + real dcr-kube1) ==="
if out=$(just gcp-cluster register-cluster --env "${env_name}" --cluster "${cluster}" 2>&1); then
    fail "overwrite accepted"
fi
printf '%s' "${out}" | grep -q "already exists" || fail "overwrite refusal unnamed:\n${out}"
if out=$(just gcp-cluster register-cluster --env prod --cluster dcr-kube1 2>&1); then
    fail "real dcr-kube1 overwrite accepted"
fi
printf '%s' "${out}" | grep -q "already exists" || fail "dcr-kube1 refusal unnamed"
echo "  overwrite refusal: PASS (incl. real dcr-kube1)"

echo "=== 4. missing env file fails with the create-cluster-env hint ==="
rm -f "${env_file}" "${manifest}" "${entry}"
if out=$(just gcp-cluster register-cluster --env "${env_name}" --cluster "${cluster}" 2>&1); then
    fail "missing env file accepted"
fi
printf '%s' "${out}" | grep -q "just create-cluster-env --env ${env_name} --cluster ${cluster}" \
    || fail "env-file failure lacks the create hint:\n${out}"
[ ! -e "${entry}" ] || fail "entry created despite missing env file"
echo "  missing env file: PASS"

echo "=== 5. missing tool in manifest fails naming it ==="
write_env_fixture
printf 'kubectl 1.35.8\npulumi 3.255.0\n' > "${manifest}"
if out=$(just gcp-cluster register-cluster --env "${env_name}" --cluster "${cluster}" 2>&1); then
    fail "missing tool accepted"
fi
printf '%s' "${out}" | grep -q "'kops' missing from ${manifest}" || fail "tool failure unnamed:\n${out}"
[ ! -e "${entry}" ] || fail "entry created despite missing tool"
echo "  missing tool: PASS (names kops, no entry written)"

echo "register-cluster contract tests PASSED"
