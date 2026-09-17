#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

just_bin="$(command -v just)"
tmp_dir="$(mktemp -d)"
stack="logto-contract-$$"
cfg_file="log-to/Pulumi.${stack}.yaml"
log_file="${tmp_dir}/calls.log"
trap 'rm -f "$cfg_file"; rm -rf "$tmp_dir"' EXIT

mkdir -p "${tmp_dir}/bin"
printf '{"project_id":"test-project"}\n' > "${tmp_dir}/pulumi-manager.json"

cat > "${tmp_dir}/bin/just" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'just %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    "logto _require-stack"*)
        printf '%s\n' "${PULUMI_STACK}"
        ;;
    "postgres verify"*)
        exit "${STUB_POSTGRES_STATUS:-0}"
        ;;
    "logto check"*)
        exit "${STUB_CHECK_STATUS:-0}"
        ;;
    "logto verify"*)
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
    *"get secret logto-app"*"-o json"*)
        printf '{"data":{"username":"bG9ndG8=","password":"cHc="}}\n'
        ;;
    *"get service logto-rw"*) printf '5432\n' ;;
    *"rollout status deployment/logto"*) exit "${STUB_ROLLOUT_STATUS:-0}" ;;
    *"get deployment logto"*) printf '{"status":{"availableReplicas":1}}\n' ;;
    *"get pods"*"app=logto"*)
        printf '{"items":[{"status":{"phase":"Running","containerStatuses":[{"ready":true}]}}]}\n'
        ;;
    *"get pvc logto-claim"*) printf 'Bound\n' ;;
    *"get service logto-api"*) printf '3001\n' ;;
    *"get service logto-admin"*) printf '3002\n' ;;
    *"get ingress logto-ingress"*)
        printf '{"spec":{"rules":[{"host":"other.example"},{"host":"auth.example.com"}],"tls":[{"secretName":"other-tls"},{"secretName":"logto-tls"}]}}\n'
        ;;
    *"logs deployment/logto"*) printf 'Logto started\n' ;;
    *)
        echo "unexpected kubectl call: $*" >&2
        exit 1
        ;;
esac
EOF
chmod +x "${tmp_dir}/bin/kubectl"

write_config() {
    local name="${1:-logto}"
    local database_secret="${2:-logto-app}"
    local image_tag="${3:-1.43.0}"
    local namespace="${4:-prod}"
    local endpoint="${5:-https://auth.example.com}"
    local ingress_host="${6:-auth.example.com}"
    local tls_secret="${7:-logto-tls}"
    local database_yaml="databaseSecret: ${database_secret}"
    if [[ "$database_secret" == "encrypted" ]]; then
        database_yaml=$'databaseSecret:\n      secure: v1:encrypted'
    fi
    cat > "$cfg_file" <<EOF
config:
  log-to:properties:
    name: ${name}
    namespace: ${namespace}
    ${database_yaml}
    storageClass: dictycr-balanced
    diskSize: 50Gi
    endpoint: ${endpoint}
    image:
      name: svhd/logto
      tag: ${image_tag}
    apiPort: 3001
    adminPort: 3002
    ingress:
      tlsSecret: ${tls_secret}
      backendHosts:
        - ${ingress_host}
      label:
        name: kcert.dev/ingress
        value: managed
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

write_config
: > "$log_file"
run_recipe "$just_bin" logto check --stack "$stack" >/dev/null

echo 'check success: PASS'

expect_failure 'PULUMI_BACKEND_URL is empty' env -u PULUMI_BACKEND_URL "$just_bin" logto check --stack "$stack"
echo 'required Pulumi environment guard: PASS'

rm -f "$cfg_file"
expect_failure 'stack config missing' "$just_bin" logto check --stack "$stack"
echo 'missing-config guard: PASS'

write_config logto logto-app 1.43.0 prod
write_config wrong-name
expect_failure 'must set name logto' "$just_bin" logto check --stack "$stack"
echo 'name guard: PASS'

write_config logto encrypted
expect_failure 'must use databaseSecret logto-app' "$just_bin" logto check --stack "$stack"
echo 'encrypted identifier guard: PASS'

write_config logto logto-app 1.43.0 dev
expect_failure "sets namespace 'dev', expected 'prod'" "$just_bin" logto check --stack "$stack"
echo 'namespace guard: PASS'

write_config logto logto-app 1.43.0 prod '<logto-auth-domain>' '<logto-auth-domain>' '<logto-tls-secret>'
expect_failure 'empty or placeholder production value' "$just_bin" logto check --stack "$stack"
echo 'placeholder guard: PASS'

write_config logto logto-app latest
expect_failure 'non-latest Logto image tag' "$just_bin" logto check --stack "$stack"
echo 'mutable image guard: PASS'

write_config
: > "$log_file"
run_recipe "$just_bin" logto install --stack "$stack" --retries 2 --interval 1 >/dev/null
install_calls=()
while IFS= read -r line; do
    install_calls+=("$line")
done < <(rg '^(just|kubectl)' "$log_file")
expected_order=(
    "just logto check"
    "just gcp-pulumi ensure-stack"
    "just gcp-pulumi preview"
    "just gcp-pulumi create-resource"
    "kubectl rollout status deployment/logto"
    "just logto verify"
)
last=-1
for expected in "${expected_order[@]}"; do
    index=-1
    for i in "${!install_calls[@]}"; do
        if (( i > last )) && [[ "${install_calls[$i]}" == "$expected"* ]]; then
            index=$i
            break
        fi
    done
    if (( index < 0 )); then
        printf 'missing or misordered install call: %s\n%s\n' "$expected" "${install_calls[*]}" >&2
        exit 1
    fi
    last=$index
done
echo 'install order: PASS'

: > "$log_file"
if run_recipe env STUB_PREVIEW_STATUS=1 "$just_bin" logto install --stack "$stack" >/dev/null 2>&1; then
    echo 'preview failure unexpectedly succeeded' >&2
    exit 1
fi
! rg -q '^just gcp-pulumi create-resource|^kubectl rollout|^just logto verify' "$log_file"
echo 'install fail-fast: PASS'

: > "$log_file"
if run_recipe env STUB_CHECK_STATUS=1 "$just_bin" logto install --stack "$stack" >/dev/null 2>&1; then
    echo 'check failure unexpectedly succeeded' >&2
    exit 1
fi
! rg -q '^just gcp-pulumi ensure-stack|^just gcp-pulumi preview|^just gcp-pulumi create-resource' "$log_file"
echo 'check fail-fast: PASS'

: > "$log_file"
if run_recipe env STUB_CREATE_STATUS=1 "$just_bin" logto install --stack "$stack" >/dev/null 2>&1; then
    echo 'create failure unexpectedly succeeded' >&2
    exit 1
fi
! rg -q '^kubectl rollout|^just logto verify' "$log_file"
echo 'apply fail-fast: PASS'

: > "$log_file"
run_recipe "$just_bin" logto verify --stack "$stack" >/tmp/logto-verify-output
rg -q 'Logto checks passed\.' /tmp/logto-verify-output
echo 'multi-rule Ingress verification: PASS'

source_text=$(cat just_modules/logto.justfile)
grep -F 'just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"' <<<"${source_text}" >/dev/null
grep -F 'just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"' <<<"${source_text}" >/dev/null
grep -F 'just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"' <<<"${source_text}" >/dev/null
grep -F 'just logto verify' <<<"${source_text}" >/dev/null
grep -F 'app=logto' <<<"${source_text}" >/dev/null
grep -F 'config_name=' <<<"${source_text}" >/dev/null
grep -F 'any(.spec.rules[]?; .host == $host)' <<<"${source_text}" >/dev/null
grep -F 'any(.spec.tls[]?; .secretName == $secret)' <<<"${source_text}" >/dev/null
! grep -F -- '--stack prod' <<<"${source_text}" >/dev/null
! grep -F 'pulumi stack init' <<<"${source_text}" >/dev/null

echo 'Logto recipe contract: PASS'
