#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

env_name="test"
cluster_name="tool-contract-$$"
env_file=".env.${env_name}.${cluster_name}"
manifest=".tool-versions.${env_name}.${cluster_name}"
cleanup() {
    rm -f "${env_file}" "${manifest}"
}
trap cleanup EXIT

rm -f "${env_file}" "${manifest}"

just create-cluster-env \
    --env "${env_name}" \
    --cluster "${cluster_name}" \
    --project test-project >/dev/null

[ -f "${manifest}" ]
cmp -s .tool-versions "${manifest}"
grep -Fx "ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=${manifest}" "${env_file}" >/dev/null

printf 'kubectl preserved-version\n' > "${manifest}"
cce_log=$(mktemp)
just --verbose create-cluster-env \
    --env "${env_name}" \
    --cluster "${cluster_name}" \
    --project test-project \
    --force yes 2>&1 | tee "${cce_log}" || {
    sed 's/^/  | /' "${cce_log}" >&2
    rm -f "${cce_log}"
    exit 1
}
rm -f "${cce_log}"

grep -Fx 'kubectl preserved-version' "${manifest}" >/dev/null

grep -Fx "ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=${manifest}" "${env_file}" >/dev/null

echo "tool version manifest creation and preservation: PASS"
