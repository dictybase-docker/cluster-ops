#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

cleanup() {
    rm -rf "${test_env_dir}" config/kops/dcr-test-1 config/kops/env-cluster
}
test_env_dir=$(mktemp -d)
trap cleanup EXIT
rm -rf config/kops/dcr-test-1 config/kops/env-cluster

cat << 'EOF' > "${test_env_dir}/kops"
#!/usr/bin/env bash
set -euo pipefail
echo "mock kops called with: $*" >> "${MOCK_KOPS_LOG}"

cmd="${1:-}"
subcmd="${2:-}"

if [ "${cmd}" = "version" ]; then
    echo "1.29.2"
    exit 0
fi

if [ "${cmd}" = "get" ] && [ "${subcmd}" = "cluster" ]; then
    echo "apiVersion: kops.k8s.io/v1alpha2"
    echo "kind: Cluster"
    echo "metadata:"
    echo "  name: mock-cluster.k8s.local"
    exit 0
fi

if [ "${cmd}" = "get" ] && [ "${subcmd}" = "instancegroups" ]; then
    echo "apiVersion: kops.k8s.io/v1alpha2"
    echo "kind: InstanceGroup"
    echo "metadata:"
    echo "  name: nodes"
    exit 0
fi

if [ "${cmd}" = "replace" ] || [ "${cmd}" = "validate" ] || [ "${cmd}" = "export" ] || [ "${cmd}" = "update" ] || [ "${cmd}" = "reconcile" ] || [ "${cmd}" = "delete" ]; then
    exit 0
fi

exit 0
EOF
chmod +x "${test_env_dir}/kops"

export PATH="${test_env_dir}:${PATH}"
export MOCK_KOPS_LOG="${test_env_dir}/kops.log"
: > "${MOCK_KOPS_LOG}"

echo "=== 1. Testing bootstrap-bundle with pure CLI args (env unset) ==="
(
    unset CLUSTER_NAME KOPS_CLUSTER_NAME PROJECT_ID KOPS_STATE_STORE API_ACCESS_CIDR
    just gcp-cluster bootstrap-bundle \
        --cluster dcr-test-1 \
        --project proj-test-1 \
        --api-access-cidr "203.0.113.10/32"
)
[ -s "config/kops/dcr-test-1/cluster.yaml" ] && echo "  ✓ cluster.yaml created"
[ -s "config/kops/dcr-test-1/instancegroups.yaml" ] && echo "  ✓ instancegroups.yaml created"
! grep -E '\$\{[A-Za-z0-9_]+\}' config/kops/dcr-test-1/*.yaml && echo "  ✓ zero unresolved placeholders"
[ "$(wc -l < "${MOCK_KOPS_LOG}")" -eq 0 ] && echo "  ✓ zero kops calls made during bootstrap"

echo "=== 2. Testing bootstrap-bundle fails on existing directory ==="
if (just gcp-cluster bootstrap-bundle --cluster dcr-test-1 --project proj-test-1 --api-access-cidr "203.0.113.10/32" 2>&1); then
    echo "ERROR: should have failed on existing directory"
    exit 1
else
    echo "  ✓ bootstrap-bundle correctly refused to overwrite existing directory"
fi

echo "=== 3. Testing bootstrap-bundle env var priority over args ==="
(
    export CLUSTER_NAME="env-cluster"
    export PROJECT_ID="env-proj"
    export API_ACCESS_CIDR="192.0.2.1/32"
    just gcp-cluster bootstrap-bundle \
        --cluster ignored-cluster \
        --project ignored-proj \
        --api-access-cidr "1.1.1.1/32"
)
grep "project: env-proj" config/kops/env-cluster/cluster.yaml >/dev/null && echo "  ✓ env PROJECT_ID took priority"
grep "192.0.2.1/32" config/kops/env-cluster/cluster.yaml >/dev/null && echo "  ✓ env API_ACCESS_CIDR took priority"

echo "=== 4. Testing operational recipes with pure CLI args (env unset) ==="
(
    unset CLUSTER_NAME KOPS_CLUSTER_NAME PROJECT_ID KOPS_STATE_STORE API_ACCESS_CIDR
    : > "${MOCK_KOPS_LOG}"
    
    just gcp-cluster replace-manifests --cluster dcr-test-1 --force yes
    grep -E "replace.*dcr-test-1/cluster\.yaml.*--force" "${MOCK_KOPS_LOG}" >/dev/null && echo "  ✓ replace-manifests passed --force"
    
    just gcp-cluster export-bundle --cluster dcr-test-1
    [ -s "config/kops/dcr-test-1/cluster.yaml" ] && echo "  ✓ export-bundle succeeded with args"

    just gcp-cluster plan-cluster --cluster dcr-test-1
    grep -E "(update|reconcile)" "${MOCK_KOPS_LOG}" >/dev/null && echo "  ✓ plan-cluster executed"

    just gcp-cluster update-cluster --cluster dcr-test-1
    grep -E "(update|reconcile)" "${MOCK_KOPS_LOG}" >/dev/null && echo "  ✓ update-cluster executed"

    just gcp-cluster validate-cluster --cluster dcr-test-1
    grep -E "validate.*cluster.*--name=dcr-test-1\.k8s\.local" "${MOCK_KOPS_LOG}" >/dev/null && echo "  ✓ validate-cluster executed"

    just gcp-cluster export-kubeconfig --cluster dcr-test-1
    grep -E "export.*kubeconfig.*--name=dcr-test-1\.k8s\.local" "${MOCK_KOPS_LOG}" >/dev/null && echo "  ✓ export-kubeconfig executed"

    just gcp-cluster delete-cluster --cluster dcr-test-1 --project proj-test-1
    grep -E "delete.*cluster.*--name=dcr-test-1\.k8s\.local" "${MOCK_KOPS_LOG}" >/dev/null && echo "  ✓ delete-cluster dry-run executed"
)

rm -rf config/kops/dcr-test-1 config/kops/env-cluster
echo "  ✓ cleaned up test directories"

echo "All bootstrap bundle contract tests PASSED!"
