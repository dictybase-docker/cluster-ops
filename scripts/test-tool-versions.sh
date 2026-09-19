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
# CI-only diagnosis: extract the create-cluster-env body and run it under
# bash -x so a silent set -e abort shows its failing line. Runs the real
# recipe normally on the happy path.
if [ "${TOOL_VERSIONS_TRACE:-}" = "yes" ]; then
    just --dump --dump-format json > /tmp/tv-dump.json
    python3 - <<'PYEOF' > /tmp/tv-recipe.sh
import json, os
d = json.load(open('/tmp/tv-dump.json'))
r = d['recipes']['create-cluster-env']
vals = {'env': 'test', 'cluster': os.environ['cluster_name'],
        'project': 'test-project', 'force': 'no',
        'credentials': '', 'ssh_key': '', 'kubeconfig': '',
        'pulumi_gcp_credentials': '', 'pulumi_secret_provider': '',
        'pulumi_backend_url': '', 'pulumi_stack': ''}
def flat(parts):
    out = []
    for p in parts:
        if isinstance(p, str):
            out.append(p)
        else:
            head = None
            for el in p:
                if isinstance(el, str):
                    head = el
                    break
            out.append(vals.get(head, ''))
    return ''.join(out)
for parts in r['body']:
    print(flat(parts))
PYEOF
    chmod +x /tmp/tv-recipe.sh
    env_name="test" cluster_name="${cluster_name}" TOOL_VERSIONS_TRACE=1         bash -x /tmp/tv-recipe.sh test test "${cluster_name}" test-project "" "" "" "" "" "" "" no 2>&1 | tail -60
    exit 1
fi
cce_log=$(mktemp)
if ! just create-cluster-env \
    --env "${env_name}" \
    --cluster "${cluster_name}" \
    --project test-project \
    --force yes >"${cce_log}" 2>&1; then
    sed 's/^/  | /' "${cce_log}" >&2
    rm -f "${cce_log}"
    exit 1
fi
rm -f "${cce_log}"

grep -Fx 'kubectl preserved-version' "${manifest}" >/dev/null

grep -Fx "ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=${manifest}" "${env_file}" >/dev/null

echo "tool version manifest creation and preservation: PASS"
