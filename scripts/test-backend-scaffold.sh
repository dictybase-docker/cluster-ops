#!/usr/bin/env bash
# Contract tests for `just gcp-pulumi scaffold-backend-stack`.
#
# `pulumi` is mocked on PATH: it records every call and, for `stack init`,
# writes the secretsprovider + encryptedkey header exactly as the real CLI
# does. Everything else runs for real against the repo template and the
# real dcr-kube1 registry entry, inside throwaway folders under a temp dir.
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

# --- mock pulumi -----------------------------------------------------------
cat > "${mock_bin}/pulumi" <<'EOF'
#!/usr/bin/env bash
# Minimal pulumi mock: records the call; `stack init` writes the provider header.
echo "pulumi $*" >> "${MOCK_CALLS_LOG}"
if [ "$1" = "-C" ]; then folder="$2"; shift 2; else folder="."; fi
if [ "$1" = "stack" ] && [ "$2" = "init" ]; then
    stack="$3"
    # extract --secrets-provider value (last arg)
    kms="${!#}"
    {
        echo "secretsprovider: ${kms}"
        echo "encryptedkey: mock-encrypted-key"
    } > "${folder}/Pulumi.${stack}.yaml"
    exit 0
fi
exit 0
EOF
chmod +x "${mock_bin}/pulumi"
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

yq_get() { # yq_get <file> <path>
    yq -r "$2" "$1"
}

run_scaffold() { # run_scaffold <folder> [extra args...]
    local folder="$1"; shift
    just gcp-pulumi scaffold-backend-stack --folder "${folder}" "$@"
}

echo "=== 1. unknown stack fails naming the registry ==="
mkdir -p "${tmp}/modware-x"
out=$(just gcp-pulumi scaffold-backend-stack --folder "${tmp}/modware-x" --stack nope 2>&1) \
    && fail "unknown stack accepted: ${out}"
printf '%s' "${out}" | grep -q "no registry entry with stack 'nope'" \
    || fail "unknown stack failure did not name the cause:\n${out}"
[ "$(grep -c 'stack init' "${calls}")" = "0" ] || fail "stack init ran for an unknown stack"
echo "  unknown stack: PASS"

echo "=== 2. existing stack file is never overwritten ==="
folder="${tmp}/modware-order"
mkdir -p "${folder}"
cfg="${folder}/Pulumi.dcr-kube1.yaml"
printf 'secretsprovider: existing\nencryptedkey: existing\n' > "${cfg}"
out=$(just gcp-pulumi scaffold-backend-stack --folder "${folder}" --stack dcr-kube1 2>&1) \
    && fail "overwrite accepted:\n${out}"
printf '%s' "${out}" | grep -q "already exists" || fail "overwrite refusal lacks cause:\n${out}"
[ "$(grep -c 'stack init' "${calls}")" = "0" ] || fail "stack init ran despite existing file"
echo "  overwrite refusal: PASS"
rm -f "${cfg}"

echo "=== 3. happy path renders provider header + config block ==="
out=$(run_scaffold "${folder}" --stack dcr-kube1 --port 9250) || fail "happy path failed:\n${out}"

[ -f "${cfg}" ] || fail "stack file not created"
[ "$(grep -c 'stack init' "${calls}")" = "1" ] || fail "expected exactly one stack init call"
grep -q -- "--secrets-provider gcpkms://projects/dcr-kube1" "${calls}" \
    || fail "stack init did not use the registry KMS provider:\n$(cat "${calls}")"

yq_get "${cfg}" '.secretsprovider' | grep -q '^gcpkms://' || fail "provider header missing"
check() { # check <want> <path>
    local got
    got=$(yq_get "${cfg}" "$2")
    [ "${got}" = "$1" ] || fail "config $2 = '${got}', want '$1'"
}
check order ".config.\"modware-order:properties\".appName"
check dictybase/modware-order ".config.\"modware-order:properties\".image.name"
check bootstrap ".config.\"modware-order:properties\".image.tag"
check prod ".config.\"modware-order:properties\".namespace"
check 9250 ".config.\"modware-order:properties\".port"
check backend ".config.\"modware-order:properties\".arangodbSecret.name"
check password ".config.\"modware-order:properties\".arangodbSecret.passkey"
check user ".config.\"modware-order:properties\".arangodbSecret.userkey"
check start-server ".config.\"modware-order:properties\".command"
grep -q ':latest' "${cfg}" && fail "mutable :latest tag in rendered config"
yq -r '.config' "${cfg}" >/dev/null || fail "rendered file does not parse"
printf '%s' "${out}" | grep -q "Next: just gcp-pulumi check-backend-prereqs" \
    || fail "output lacks the next-step line"
echo "  happy path: PASS (11 config keys, registry KMS, no :latest)"

echo "=== 4. --secret-name override and --port override ==="
folder2="${tmp}/modware-stock"
mkdir -p "${folder2}"
run_scaffold "${folder2}" --stack dcr-kube1 --port 9251 --secret-name legacy-secret \
    || fail "override run failed"
cfg2="${folder2}/Pulumi.dcr-kube1.yaml"
[ "$(yq_get "${cfg2}" '.config."modware-stock:properties".appName')" = "stock" ] \
    || fail "appName derivation broke for modware-stock"
[ "$(yq_get "${cfg2}" '.config."modware-stock:properties".arangodbSecret.name')" = "legacy-secret" ] \
    || fail "secret-name override not applied"
[ "$(yq_get "${cfg2}" '.config."modware-stock:properties".port')" = "9251" ] \
    || fail "port override not applied"
echo "  overrides: PASS"

echo "=== 5. scaffold-graphql-stack renders graphql_server config with minio-root ==="
: > "${calls}"
gql_cfg="graphql_server/Pulumi.dcr-kube1.yaml"
gql_bak=""
if [ -f "${gql_cfg}" ]; then
    gql_bak="${tmp}/Pulumi.dcr-kube1.yaml.bak"
    mv "${gql_cfg}" "${gql_bak}"
fi
out=$(just gcp-pulumi scaffold-graphql-stack --stack dcr-kube1 2>&1) || {
    [ -n "${gql_bak}" ] && mv "${gql_bak}" "${gql_cfg}"
    fail "scaffold-graphql-stack failed:\n${out}"
}
[ -f "${gql_cfg}" ] || fail "graphql_server stack file not created"
[ "$(yq_get "${gql_cfg}" '.config."graphql_server:properties".name')" = "graphql" ] || fail "name wrong"
[ "$(yq_get "${gql_cfg}" '.config."graphql_server:properties".namespace')" = "prod" ] || fail "namespace wrong"
[ "$(yq_get "${gql_cfg}" '.config."graphql_server:properties".authEnabled')" = "false" ] || fail "authEnabled wrong"
[ "$(yq_get "${gql_cfg}" '.config."graphql_server:properties".ingress.enabled')" = "false" ] || fail "ingress.enabled wrong"
[ "$(yq_get "${gql_cfg}" '.config."graphql_server:properties".secrets.minio.name')" = "minio-root" ] || fail "minio.name wrong"
[ "$(yq_get "${gql_cfg}" '.config."graphql_server:properties".secrets.minio.userKey')" = "rootUser" ] || fail "minio.userKey wrong"
[ "$(yq_get "${gql_cfg}" '.config."graphql_server:properties".secrets.minio.passKey')" = "rootPassword" ] || fail "minio.passKey wrong"
if [ -n "${gql_bak}" ]; then
    mv "${gql_bak}" "${gql_cfg}"
else
    rm -f "${gql_cfg}"
fi
echo "  scaffold-graphql-stack: PASS"

echo "=== 6. scaffold-backend-stack uses services.yaml overrides for modware-annotation-feature ==="
: > "${calls}"
folder3="${tmp}/modware-annotation-feature"
mkdir -p "${folder3}"
run_scaffold "${folder3}" --stack dcr-kube1 --port 9250 \
    || fail "modware-annotation-feature scaffold failed"
cfg3="${folder3}/Pulumi.dcr-kube1.yaml"
[ "$(yq_get "${cfg3}" '.config."modware-annotation-feature:properties".appName')" = "anno-feat" ] \
    || fail "appName must be anno-feat"
[ "$(yq_get "${cfg3}" '.config."modware-annotation-feature:properties".command')" = "start-feature-server" ] \
    || fail "command must be start-feature-server"
[ "$(yq_get "${cfg3}" '.config."modware-annotation-feature:properties".image.name')" = "dictybase/modware-annotation" ] \
    || fail "image.name must be dictybase/modware-annotation"
echo "  modware-annotation-feature scaffold: PASS"

echo "=== 7. scaffold-event-messenger-stack renders event-messenger config with email-event and issue-event ==="
: > "${calls}"
em_cfg="event-messenger/Pulumi.dcr-kube1.yaml"
em_bak=""
if [ -f "${em_cfg}" ]; then
    em_bak="${tmp}/event-messenger-Pulumi.dcr-kube1.yaml.bak"
    mv "${em_cfg}" "${em_bak}"
fi
out=$(just gcp-pulumi scaffold-event-messenger-stack --stack dcr-kube1 2>&1) || {
    [ -n "${em_bak}" ] && mv "${em_bak}" "${em_cfg}"
    fail "scaffold-event-messenger-stack failed:\n${out}"
}
[ -f "${em_cfg}" ] || fail "event-messenger stack file not created"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".namespace')" = "prod" ] || fail "namespace wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".logLevel')" = "error" ] || fail "logLevel wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".nats.subject')" = "OrderService.Create" ] || fail "nats.subject wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".image.name')" = "dictybase/event-messenger" ] || fail "image.name wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".image.tag')" = "bootstrap" ] || fail "image.tag wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".emailDeployment.name')" = "event-messenger-email" ] || fail "emailDeployment.name wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".emailDeployment.secrets.name')" = "email-event" ] || fail "emailDeployment.secrets.name wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".emailDeployment.secrets.values.ncbiTool')" = "eventmessenger" ] || fail "ncbiTool wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".issueDeployment.name')" = "event-messenger-issue" ] || fail "issueDeployment.name wrong"
[ "$(yq_get "${em_cfg}" '.config."event-messenger:properties".issueDeployment.secrets.name')" = "issue-event" ] || fail "issueDeployment.secrets.name wrong"
grep -q ':latest' "${em_cfg}" && fail "mutable :latest tag in rendered config"
if [ -n "${em_bak}" ]; then
    mv "${em_bak}" "${em_cfg}"
else
    rm -f "${em_cfg}"
fi
echo "  scaffold-event-messenger-stack: PASS"

echo "=== 8. scaffold-gateway-stack renders gateway config with cilium and default routes ==="
: > "${calls}"
gw_cfg="gateway/Pulumi.dcr-kube1.yaml"
gw_bak=""
if [ -f "${gw_cfg}" ]; then
    gw_bak="${tmp}/gateway-Pulumi.dcr-kube1.yaml.bak"
    mv "${gw_cfg}" "${gw_bak}"
fi
out=$(just gcp-pulumi scaffold-gateway-stack --stack dcr-kube1 2>&1) || {
    [ -n "${gw_bak}" ] && mv "${gw_bak}" "${gw_cfg}"
    fail "scaffold-gateway-stack failed:\n${out}"
}
[ -f "${gw_cfg}" ] || fail "gateway stack file not created"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".name')" = "dictycr-gateway" ] || fail "name wrong"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".namespace')" = "prod" ] || fail "namespace wrong"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".gatewayClassName')" = "cilium" ] || fail "gatewayClassName wrong"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".issuer.name')" = "letsencrypt-issuer" ] || fail "issuer.name wrong"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".routes.frontend.enabled')" = "true" ] || fail "routes.frontend.enabled wrong"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".routes.graphql.enabled')" = "true" ] || fail "routes.graphql.enabled wrong"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".routes.minio.enabled')" = "true" ] || fail "routes.minio.enabled wrong"
[ "$(yq_get "${gw_cfg}" '.config."gateway:properties".routes.logto.enabled')" = "false" ] || fail "routes.logto.enabled wrong"
if [ -n "${gw_bak}" ]; then
    mv "${gw_bak}" "${gw_cfg}"
else
    rm -f "${gw_cfg}"
fi
echo "  scaffold-gateway-stack: PASS"

echo "backend scaffold contract tests PASSED"
