#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
just_bin="$(command -v just)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
: > "$tmp/calls.log"
: > "$tmp/kubectl.log"
: > "$tmp/pulumi.log"
: > "$tmp/credentials.json"

cat > "$tmp/bin/just" <<'MOCK_JUST'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "arangodb" && "${2:-}" == "_require-stack" ]]; then
    printf '%s\n' dcr-kube1
    exit 0
fi
printf '%s\n' "$*" >> "$MOCK_JUST_CALLS"
MOCK_JUST
chmod +x "$tmp/bin/just"

cat > "$tmp/bin/pulumi" <<'MOCK_PULUMI'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$MOCK_PULUMI_CALLS"
MOCK_PULUMI
chmod +x "$tmp/bin/pulumi"

cat > "$tmp/bin/kubectl" <<'MOCK_KUBECTL'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$MOCK_KUBECTL_CALLS"
if [[ "${MOCK_MODE:-}" == "verify" ]]; then
    if [[ "${1:-}" == "get" && "${2:-}" == "nodes" ]]; then
        cat <<'JSON'
{"items":[
  {"spec":{"taints":[{"key":"dedicated","value":"database","effect":"NoSchedule"}]}},
  {"spec":{"taints":[{"key":"dedicated","value":"database","effect":"NoSchedule"}]}},
  {"spec":{"taints":[{"key":"dedicated","value":"database","effect":"NoSchedule"}]}}
]}
JSON
        exit 0
    fi
    if [[ "${1:-}" == "get" && "${2:-}" == "pods" && "$*" == *"app.kubernetes.io/name=kube-arangodb"* ]]; then
        if [[ "$*" == *"-n prod "* ]]; then
            printf '{"items":[{"status":{"phase":"Running"}}]}\n'
        else
            printf '{"items":[]}\n'
        fi
        exit 0
    fi
    if [[ "${1:-}" == "get" && "${2:-}" == "pods" && "$*" == *"arango_deployment=arangodb"* ]]; then
        exit 1
    fi
    if [[ "${1:-}" == "get" && "${2:-}" == "sc" ]]; then exit 0; fi
    if [[ "${1:-}" == "get" && "${2:-}" == "arangodeployment" ]]; then exit 0; fi
    printf 'unexpected verify kubectl call: %s\n' "$*" >&2
    exit 1
fi
if [[ "${1:-}" == "get" && "${2:-}" == "namespace" ]]; then
    exit 0
fi
if [[ "${1:-}" == "get" && "${2:-}" == "jobs" ]]; then
    printf '{"items":[]}\n'
    exit 0
fi
if [[ "${1:-}" == "get" && "${2:-}" == "secret" ]]; then
    if [[ "${3:-}" == "-n" ]]; then
        secret="${5:-}"
    else
        secret="${3:-}"
    fi
    args=" $* "
    if [[ "$secret" == "arangodb-pass" && "$args" == *"jsonpath={.data.username}"* ]]; then
        printf 'cm9vdA==\n'
    elif [[ "$secret" == "arangodb-pass" && "$args" == *"jsonpath={.data.password}"* ]]; then
        printf 'cm9vdC1wYXNzd29yZA==\n'
    elif [[ "$secret" == "arangodb-pass" ]]; then
        printf '{"data":{"username":"cm9vdA==","password":"cm9vdC1wYXNzd29yZA=="}}\n'
    elif [[ "$secret" == "arangodb-jwt" ]]; then
        printf '{"data":{"token":"dG9rZW4="}}\n'
    elif [[ "$secret" == "dictycr" ]]; then
        printf '{"metadata":{"name":"dictycr"}}\n'
    elif [[ "$secret" == "backend" && "$args" == *"-o json"* ]]; then
        printf '{"data":{"user":"ZGljdHktYXBw","password":"dGVzdC1wYXNz"}}\n'
    elif [[ "$secret" == "backend-no-user" ]]; then
        printf '{"data":{"password":"dGVzdC1wYXNz"}}\n'
    elif [[ "$secret" == "backend-empty-user" ]]; then
        printf '{"data":{"user":"","password":"dGVzdC1wYXNz"}}\n'
    elif [[ "$secret" == "backend" ]]; then
        printf '{"metadata":{"name":"backend"}}\n'
    else
        printf 'unexpected secret: %s\n' "$secret" >&2
        exit 1
    fi
    exit 0
fi
printf 'unexpected kubectl call: %s\n' "$*" >&2
exit 1
MOCK_KUBECTL
chmod +x "$tmp/bin/kubectl"

export PATH="$tmp/bin:$PATH"
export MOCK_JUST_CALLS="$tmp/calls.log"
export MOCK_KUBECTL_CALLS="$tmp/kubectl.log"
export MOCK_PULUMI_CALLS="$tmp/pulumi.log"
export PULUMI_GCP_CREDENTIALS="$tmp/credentials.json"
export PULUMI_STACK=dcr-kube1
export KUBECONFIG="$tmp/source.kubeconfig"
export CLUSTER_ENV=dev
export CLUSTER_NAME=dcr-experiments
: > "$KUBECONFIG"
cd "$repo_root"

: > "$MOCK_KUBECTL_CALLS"
source_user=$("$just_bin" arangodb source-app-user --namespace dev)
[[ "$source_user" == "dicty-app" ]]
! printf '%s\n' "$source_user" | rg -q 'test-pass'

if "$just_bin" arangodb source-app-user --namespace dev --secret backend-no-user > "$tmp/missing-user.out" 2>&1; then
    echo "ERROR: source-app-user accepted Secret without user key" >&2
    exit 1
fi
grep -q "missing a valid 'user' key" "$tmp/missing-user.out"
if "$just_bin" arangodb source-app-user --namespace dev --secret backend-empty-user > "$tmp/empty-user.out" 2>&1; then
    echo "ERROR: source-app-user accepted empty user value" >&2
    exit 1
fi
grep -q "empty 'user' value" "$tmp/empty-user.out"

if env -u KUBECONFIG -u CLUSTER_ENV -u CLUSTER_NAME "$just_bin" arangodb source-app-user --namespace dev > "$tmp/no-env.out" 2>&1; then
    echo "ERROR: source-app-user ran without active cluster-env" >&2
    exit 1
fi
grep -q "just cluster-env" "$tmp/no-env.out"

"$just_bin" arangodb finalize-bootstrap \
    --app-user dicty-app \
    --app-password destination-only-test-password \
    --namespace prod \
    --stack dcr-kube1 > "$tmp/success.out"

[[ "$(wc -l < "$MOCK_JUST_CALLS" | tr -d ' ')" -eq 7 ]]
[[ "$(sed -n '1p' "$MOCK_JUST_CALLS")" == "arangodb _assert-no-running-jobs --namespace prod --selector app=arangodb-restore" ]]
[[ "$(sed -n '2p' "$MOCK_JUST_CALLS")" == "arangodb _assert-no-running-jobs --namespace prod --selector app=arangodb-reset-root-password" ]]
[[ "$(sed -n '3p' "$MOCK_JUST_CALLS")" == "arangodb _assert-no-running-jobs --namespace prod --selector app=arangodb-create-databases" ]]
[[ "$(sed -n '4p' "$MOCK_JUST_CALLS")" == "arangodb reset-restore-config --stack dcr-kube1" ]]
[[ "$(sed -n '5p' "$MOCK_JUST_CALLS")" == "arangodb reset-root-password --namespace prod --stack dcr-kube1 --retries 60 --interval 10" ]]
[[ "$(sed -n '6p' "$MOCK_JUST_CALLS")" == "arangodb configure-app-credentials --app-user dicty-app --app-password destination-only-test-password --namespace prod --stack dcr-kube1 --retries 60 --interval 10" ]]
[[ "$(sed -n '7p' "$MOCK_JUST_CALLS")" == "arangodb verify-app-credentials --namespace prod --stack dcr-kube1" ]]
grep -q '1/4 Resetting restore stack' "$tmp/success.out"
grep -q '4/4 Verifying app login' "$tmp/success.out"

: > "$MOCK_JUST_CALLS"
if "$just_bin" arangodb finalize-bootstrap \
    --app-user '' \
    --app-password destination-only-test-password \
    --namespace prod \
    --stack dcr-kube1 > "$tmp/failure.out" 2>&1; then
    echo "ERROR: finalize-bootstrap accepted empty app-user" >&2
    exit 1
fi
grep -q 'both required' "$tmp/failure.out"
[[ ! -s "$MOCK_JUST_CALLS" ]]

if "$just_bin" arangodb finalize-bootstrap \
    --app-user root \
    --app-password destination-only-test-password \
    --namespace prod \
    --stack dcr-kube1 > "$tmp/root-user.out" 2>&1; then
    echo "ERROR: finalize-bootstrap accepted root as the application user" >&2
    exit 1
fi
grep -q 'must not be the root administrator' "$tmp/root-user.out"
[[ ! -s "$MOCK_JUST_CALLS" ]]

: > "$MOCK_JUST_CALLS"
"$just_bin" arangodb configure-app-credentials \
    --app-user dicty-app --app-password destination-only-test-password \
    --namespace prod --stack dcr-kube1 > "$tmp/configure-1.out"
"$just_bin" arangodb configure-app-credentials \
    --app-user dicty-app --app-password destination-only-test-password \
    --namespace prod --stack dcr-kube1 > "$tmp/configure-2.out"
job1=$(sed -n 's/.*--job \([^ ]*\).*/\1/p' "$MOCK_JUST_CALLS" | head -1)
job2=$(sed -n 's/.*--job \([^ ]*\).*/\1/p' "$MOCK_JUST_CALLS" | tail -1)
[[ -n "$job1" && -n "$job2" && "$job1" != "$job2" ]]
grep -q 'properties.createDatabases=false' "$MOCK_PULUMI_CALLS"

export MOCK_MODE=verify
set +e
"$just_bin" arangodb verify > "$tmp/verify-default.out" 2>&1
verify_default_status=$?
set -e
[[ "$verify_default_status" -ne 0 ]]
grep -q 'operator Running in prod' "$tmp/verify-default.out"
! grep -q 'no Running operator pod' "$tmp/verify-default.out"

set +e
"$just_bin" arangodb verify --operator-namespace operators > "$tmp/verify-override.out" 2>&1
verify_override_status=$?
set -e
[[ "$verify_override_status" -ne 0 ]]
grep -q 'no Running operator pod in operators' "$tmp/verify-override.out"
unset MOCK_MODE

echo "ArangoDB post-import finalization contract tests PASSED"
