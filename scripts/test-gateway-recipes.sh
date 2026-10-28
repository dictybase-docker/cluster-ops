#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

just_bin="$(command -v just)"
tmp_dir="$(mktemp -d)"
stack="gateway-contract-$$"
cfg_file="gateway/Pulumi.${stack}.yaml"
log_file="${tmp_dir}/calls.log"
trap 'rm -f "$cfg_file"; rm -rf "$tmp_dir"' EXIT

mkdir -p "${tmp_dir}/bin"
printf '{"project_id":"test-project"}\n' > "${tmp_dir}/pulumi-manager.json"

cat > "${tmp_dir}/bin/just" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'just %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    "gateway _require-stack"*)
        printf '%s\n' "${PULUMI_STACK}"
        ;;
    "gateway check"*)
        exit "${STUB_CHECK_STATUS:-0}"
        ;;
    "gateway verify"*)
        exit "${STUB_VERIFY_STATUS:-0}"
        ;;
    "gcp-pulumi ensure-stack"*)
        exit "${STUB_ENSURE_STATUS:-0}"
        ;;
    "gcp-pulumi preview"*)
        exit "${STUB_PREVIEW_STATUS:-0}"
        ;;
    "gcp-pulumi create-resource"*)
        exit "${STUB_CREATE_STATUS:-0}"
        ;;
    *)
        echo "unexpected nested just call: $*" >&2
        exit 1
        ;;
esac
EOF
chmod +x "${tmp_dir}/bin/just"

cat > "${tmp_dir}/bin/pulumi" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'pulumi %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"stack output appNamespace"*) printf 'prod\n' ;;
    *) exit 0 ;;
esac
EOF
chmod +x "${tmp_dir}/bin/pulumi"

cat > "${tmp_dir}/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/"*)
        exit 0
        ;;
    *"get crd gateways.gateway.networking.k8s.io"*)
        exit 0
        ;;
    *"get crd httproutes.gateway.networking.k8s.io"*)
        exit 0
        ;;
    *"get gatewayclass cilium"*"-o json"*)
        if [[ "${STUB_GWCLASS_MISSING:-0}" == "1" ]]; then
            exit 1
        fi
        printf '{"status":{"conditions":[{"type":"Accepted","status":"True"}]}}\n'
        ;;
    *"get deployment cert-manager"*)
        printf '{"spec":{"template":{"spec":{"containers":[{"name":"cert-manager","args":["--v=2","--enable-gateway-api"]}]}}},"status":{"availableReplicas":1}}\n'
        ;;
    *"patch deployment cert-manager"*)
        exit 0
        ;;
    *"rollout status deployment/cert-manager"*)
        exit 0
        ;;
    *"get daemonset cilium"*"-o jsonpath="*|*"get daemonset cilium"*"-o jsonpath="*)
        printf '%s\n' "${STUB_DS_STRATEGY:-OnDelete}"
        ;;
    *"get pods -l k8s-app=cilium -o json"*)
        printf '{"items":[{"metadata":{"name":"cilium-abc"},"spec":{"nodeName":"node-1"}}]}\n'
        ;;
    *"delete pod cilium-abc"*)
        exit 0
        ;;
    *"wait --for=condition=Ready pod -l k8s-app=cilium"*)
        exit 0
        ;;
    *"rollout restart deployment/cilium-operator"*)
        exit 0
        ;;
    *"rollout restart daemonset/cilium"*)
        exit 0
        ;;
    *"rollout status daemonset/cilium"*)
        exit 0
        ;;
    *"rollout status deployment/cilium-operator"*)
        exit 0
        ;;
    *"get service frontpage-api"*) printf '3000\n' ;;
    *"get service stockcenter-api"*) printf '3000\n' ;;
    *"get service publication-api"*) printf '3000\n' ;;
    *"get service genomepage-api"*) printf '3000\n' ;;
    *"get service graphql-api"*) printf '8080\n' ;;
    *"get service minio"*) printf '9000\n' ;;
    *"get service logto-api"*) printf '3001\n' ;;
    *"get service dcr-mcp-api"*) printf '8080\n' ;;
    *"get issuer letsencrypt-issuer"*"-o json"*)
        printf '{"status":{"conditions":[{"type":"Ready","status":"True"}]}}\n'
        ;;
    *"get gateway dictycr-gateway"*"-o jsonpath"*)
        printf '34.120.0.10'
        ;;
    *"get gateway dictycr-gateway"*"-o json"*)
        printf '{"status":{"addresses":[{"value":"34.120.0.10"}],"conditions":[{"type":"Accepted","status":"True"},{"type":"Programmed","status":"True"}]}}\n'
        ;;
    *"get secret frontend-tls"*) printf 'kubernetes.io/tls\n' ;;
    *"get secret graphql-tls"*) printf 'kubernetes.io/tls\n' ;;
    *"get secret minio-tls"*) printf 'kubernetes.io/tls\n' ;;
    *"get secret logto-tls"*) printf 'kubernetes.io/tls\n' ;;
    *"get secret mcp-tls"*) printf 'kubernetes.io/tls\n' ;;
    *"get httproute frontend-route"*"-o json"*)
        printf '{"spec":{"hostnames":["dictycr.org"]},"status":{"parents":[{"conditions":[{"type":"Accepted","status":"True"},{"type":"ResolvedRefs","status":"True"}]}]}}\n'
        ;;
    *"get httproute graphql-route"*"-o json"*)
        printf '{"spec":{"hostnames":["graphql.dictycr.org"]},"status":{"parents":[{"conditions":[{"type":"Accepted","status":"True"},{"type":"ResolvedRefs","status":"True"}]}]}}\n'
        ;;
    *"get httproute minio-route"*"-o json"*)
        printf '{"spec":{"hostnames":["storage.dictycr.org"]},"status":{"parents":[{"conditions":[{"type":"Accepted","status":"True"},{"type":"ResolvedRefs","status":"True"}]}]}}\n'
        ;;
    *"get httproute logto-route"*"-o json"*)
        printf '{"spec":{"hostnames":["auth.dictycr.org"]},"status":{"parents":[{"conditions":[{"type":"Accepted","status":"True"},{"type":"ResolvedRefs","status":"True"}]}]}}\n'
        ;;
    *"get httproute mcp-route"*"-o json"*)
        printf '{"spec":{"hostnames":["mcp.dictycr.org"]},"status":{"parents":[{"conditions":[{"type":"Accepted","status":"True"},{"type":"ResolvedRefs","status":"True"}]}]}}\n'
        ;;
    *)
        echo "unexpected kubectl call: $*" >&2
        exit 1
        ;;
esac
EOF
chmod +x "${tmp_dir}/bin/kubectl"

write_config() {
    local namespace="${1:-prod}"
    local email="${2:-ops@dictycr.org}"
    local fe_enabled="${3:-true}"
    local fe_host="${4:-dictycr.org}"
    local gql_enabled="${5:-true}"
    local gql_host="${6:-graphql.dictycr.org}"
    local minio_enabled="${7:-true}"
    local minio_host="${8:-storage.dictycr.org}"
    local logto_enabled="${9:-false}"
    local logto_host="${10:-<logto-host>}"
    cat > "$cfg_file" <<EOF
config:
  gateway:properties:
    name: dictycr-gateway
    namespace: ${namespace}
    gatewayClassName: cilium
    issuer:
      name: letsencrypt-issuer
      email: ${email}
      server: https://acme-v02.api.letsencrypt.org/directory
      secretName: letsencrypt-account-key
    routes:
      frontend:
        enabled: ${fe_enabled}
        hosts:
          - ${fe_host}
        tlsSecret: frontend-tls
        services:
          - name: frontpage-api
            path: /
            port: 3000
          - name: stockcenter-api
            path: /stockcenter
            port: 3000
          - name: publication-api
            path: /publication
            port: 3000
          - name: genomepage-api
            path: /gene
            port: 3000
      graphql:
        enabled: ${gql_enabled}
        hosts:
          - ${gql_host}
        tlsSecret: graphql-tls
        serviceName: graphql-api
        servicePort: 8080
        path: /
      minio:
        enabled: ${minio_enabled}
        hosts:
          - ${minio_host}
        tlsSecret: minio-tls
        serviceName: minio
        servicePort: 9000
        path: /
      logto:
        enabled: ${logto_enabled}
        hosts:
          - ${logto_host}
        tlsSecret: logto-tls
        serviceName: logto-api
        servicePort: 3001
        path: /
EOF
}

run_recipe() {
    env \
        "PATH=${tmp_dir}/bin:${PATH}" \
        "PULUMI_STACK=${stack}" \
        "PULUMI_BACKEND_URL=gs://pulumi-state-test" \
        "PULUMI_GCP_CREDENTIALS=${tmp_dir}/pulumi-manager.json" \
        "PULUMI_SECRET_PROVIDER=gcpkms://projects/test-project/locations/us-central1/keyRings/test/cryptoKeys/test" \
        "STUB_LOG=${log_file}" \
        "$@"
}

expect_failure() {
    local expected="$1"
    shift
    local output status
    set +e
    output=$(run_recipe "$@" 2>&1)
    status=$?
    set -e
    if [[ "$status" -eq 0 || "$output" != *"$expected"* ]]; then
        printf 'expected failure containing %s, got status=%s output=%s\n' "$expected" "$status" "$output" >&2
        exit 1
    fi
}

: > "$log_file"
run_recipe "$just_bin" gateway install-crds >/dev/null
rg -q 'apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/' "$log_file"
echo 'install-crds: PASS'

: > "$log_file"
run_recipe "$just_bin" gateway restart-cilium --retries 2 --interval 1 >/dev/null
rg -q 'rollout restart deployment/cilium-operator' "$log_file"
rg -q 'delete pod cilium-abc' "$log_file"
rg -q 'wait --for=condition=Ready pod -l k8s-app=cilium' "$log_file"
rg -q 'rollout status deployment/cilium-operator' "$log_file"
rg -q 'get gatewayclass cilium' "$log_file"
echo 'restart-cilium success: PASS'

expect_failure 'not found or not Accepted' env STUB_GWCLASS_MISSING=1 "$just_bin" gateway restart-cilium --retries 2 --interval 1
echo 'restart-cilium failure guard: PASS'

write_config
: > "$log_file"
run_recipe "$just_bin" gateway check --stack "$stack" >/dev/null
echo 'check success: PASS'

expect_failure 'PULUMI_BACKEND_URL is empty' env -u PULUMI_BACKEND_URL "$just_bin" gateway check --stack "$stack"
echo 'required Pulumi environment guard: PASS'

rm -f "$cfg_file"
expect_failure 'stack config missing' "$just_bin" gateway check --stack "$stack"
echo 'missing-config guard: PASS'

write_config dev
expect_failure "sets namespace 'dev', expected 'prod'" "$just_bin" gateway check --stack "$stack"
echo 'namespace guard: PASS'

write_config prod '<acme-email>'
expect_failure 'empty or placeholder' "$just_bin" gateway check --stack "$stack"
echo 'issuer email placeholder guard: PASS'

write_config prod ops@dictycr.org true '<frontend-host>'
expect_failure 'empty or placeholder' "$just_bin" gateway check --stack "$stack"
echo 'enabled route placeholder guard: PASS'

write_config prod ops@dictycr.org false dictycr.org false graphql.dictycr.org false storage.dictycr.org false auth.dictycr.org
expect_failure 'at least one route' "$just_bin" gateway check --stack "$stack"
echo 'all routes disabled guard: PASS'

write_config
expect_failure 'GatewayClass cilium' env STUB_GWCLASS_MISSING=1 "$just_bin" gateway check --stack "$stack"
echo 'GatewayClass guard: PASS'

write_config
: > "$log_file"
out=$(run_recipe "$just_bin" gateway deploy --stack "$stack" --retries 2 --interval 1)
printf '%s' "$out" | rg -q '34\.120\.0\.10'
printf '%s' "$out" | rg -q 'Next: just gateway verify'
deploy_calls=()
while IFS= read -r line; do
    deploy_calls+=("$line")
done < <(rg '^(just|kubectl) ' "$log_file")
expected_order=(
    "just gateway check"
    "just gcp-pulumi ensure-stack"
    "just gcp-pulumi preview"
    "just gcp-pulumi create-resource"
    "kubectl get gateway dictycr-gateway"
)
last=-1
for expected in "${expected_order[@]}"; do
    index=-1
    for i in "${!deploy_calls[@]}"; do
        if (( i > last )) && [[ "${deploy_calls[$i]}" == "$expected"* ]]; then
            index=$i
            break
        fi
    done
    if (( index < 0 )); then
        printf 'missing or misordered deploy call: %s\n%s\n' "$expected" "${deploy_calls[*]}" >&2
        exit 1
    fi
    last=$index
done
! rg -q '^just gateway verify' "$log_file"
echo 'deploy order and IP output: PASS'

ip_out=$(run_recipe "$just_bin" gateway show-ip --stack "$stack")
[[ "$ip_out" == "34.120.0.10" ]] || { echo "unexpected show-ip output: $ip_out" >&2; exit 1; }
echo 'show-ip: PASS'

: > "$log_file"
if run_recipe env STUB_CHECK_STATUS=1 "$just_bin" gateway deploy --stack "$stack" >/dev/null 2>&1; then
    echo 'check failure unexpectedly succeeded' >&2
    exit 1
fi
! rg -q '^just gcp-pulumi ensure-stack|^just gcp-pulumi preview|^just gcp-pulumi create-resource' "$log_file"
echo 'deploy check fail-fast: PASS'

: > "$log_file"
run_recipe "$just_bin" gateway verify --stack "$stack" >/tmp/gateway-verify-output
rg -q 'Gateway checks passed\.' /tmp/gateway-verify-output
! rg -q 'get httproute logto-route' "$log_file"
echo 'verify enabled routes: PASS'

echo 'Gateway recipe contract: PASS'
