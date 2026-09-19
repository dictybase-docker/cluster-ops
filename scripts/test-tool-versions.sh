#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

env_name="test"
cluster_name="tool-contract-$$"
env_file=".env.${env_name}.${cluster_name}"
manifest=".tool-versions.${env_name}.${cluster_name}"
cleanup() {
    rm -f "${env_file}" "${manifest}" "${cce_log}"
    if [ "${seeded:-}" = "true" ]; then
        rm -f "${tool_versions_file}"
    fi
}
trap cleanup EXIT

tool_versions_file=".tool-versions"
cce_log=$(mktemp)
rm -f "${env_file}" "${manifest}"
# .tool-versions is gitignored; seed a minimal one when missing and restore
# the original in cleanup — never clobber the operator's live manifest.
seeded=false
if [ ! -f "${tool_versions_file}" ]; then
    printf 'kubectl 1.28.8\n' > "${tool_versions_file}"
    seeded=true
fi

# Phase 1: creation — recipe copies .tool-versions to the per-cluster
# manifest and records the ASDF selector in the env file.
if ! just create-cluster-env \
    --env "${env_name}" \
    --cluster "${cluster_name}" \
    --project test-project >"${cce_log}" 2>&1; then
    sed 's/^/  | /' "${cce_log}" >&2
    exit 1
fi
[ -f "${manifest}" ]
cmp -s "${tool_versions_file}" "${manifest}"
grep -Fx "ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=${manifest}" "${env_file}" >/dev/null

# Phase 2: preservation — a --force re-run must keep the per-cluster
# manifest it already created.
printf 'kubectl preserved-version\n' > "${manifest}"

just create-cluster-env \
    --env "${env_name}" \
    --cluster "${cluster_name}" \
    --project test-project \
    --force yes >"${cce_log}" 2>&1 || {
    sed 's/^/  | /' "${cce_log}" >&2
    rm -f "${cce_log}"
    exit 1
}
rm -f "${cce_log}"

grep -Fx 'kubectl preserved-version' "${manifest}" >/dev/null

grep -Fx "ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=${manifest}" "${env_file}" >/dev/null

echo "tool version manifest creation and preservation: PASS"
