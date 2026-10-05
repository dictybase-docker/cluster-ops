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
check order ".config.\"modware-order:properties\".arangodbSecret.name"
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

echo "backend scaffold contract tests PASSED"
