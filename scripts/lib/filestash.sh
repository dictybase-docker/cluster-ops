# shellcheck shell=bash
# Shared helpers for just_modules/filestash.justfile. Source it from a recipe
# that runs at the repository root.

fs_die() {
    echo "Error: $*" >&2
    exit 1
}

# fs_resolve <stack-flag> <namespace-flag>
# Sets STACK, CFG, NS. The namespace comes from properties.namespace in
# filestash/Pulumi.<stack>.yaml. A --namespace flag that does not match fails
# here, before any change.
fs_resolve() {
    STACK="${1:-${PULUMI_STACK:-}}"
    [[ -n "$STACK" ]] || fs_die "no stack name — pass --stack or set PULUMI_STACK (via cluster env)."
    [[ "$STACK" =~ ^[A-Za-z0-9._-]+$ ]] || fs_die "invalid stack name '$STACK'."
    CFG="filestash/Pulumi.${STACK}.yaml"
    [[ -f "$CFG" ]] || fs_die "stack config $CFG missing — run: just gcp-pulumi scaffold-filestash-stack --state-size <q>"
    local cfg_ns
    cfg_ns=$(fs_prop namespace)
    [[ -n "$cfg_ns" && "$cfg_ns" != *"<"* ]] || fs_die "properties.namespace is empty or a placeholder in $CFG."
    if [[ -n "${2:-}" && "$2" != "$cfg_ns" ]]; then
        fs_die "--namespace '$2' does not match properties.namespace '$cfg_ns' in $CFG."
    fi
    NS="$cfg_ns"
}

# fs_prop <dotted.path> prints one value of filestash:properties, or "".
fs_prop() {
    yq -r ".config.\"filestash:properties\".$1 // \"\"" "$CFG"
}

# fs_secret_value <secret> <key> prints a decoded Secret value, or "".
fs_secret_value() {
    kubectl -n "$NS" get secret "$1" -o "jsonpath={.data.$2}" 2>/dev/null | base64 -d 2>/dev/null || true
}

fs_secret_exists() {
    kubectl -n "$NS" get secret "$1" >/dev/null 2>&1
}

# fs_random <n> prints n random letters and digits. The pipe reads a fixed
# block, so no stage gets SIGPIPE under pipefail.
fs_random() {
    head -c 4096 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9' | cut -c "1-$1"
}

# fs_pct <string> percent-encodes a value for URL userinfo.
fs_pct() {
    local s="$1" out="" i c
    for ((i = 0; i < ${#s}; i++)); do
        c=${s:i:1}
        case "$c" in
            [A-Za-z0-9._~-]) out+="$c" ;;
            *) printf -v c '%%%02X' "'$c"; out+="$c" ;;
        esac
    done
    printf '%s' "$out"
}

# fs_minio_run <access-key> <secret-key> <script>
# Runs <script> with the mc alias `minio` on a port-forward to Service minio.
# Keys travel in the environment only, never in argv. The mc config lives in
# a throwaway directory.
fs_minio_run() {
    FS_U="$(fs_pct "$1")"
    FS_P="$(fs_pct "$2")"
    FS_SCRIPT="$3"
    MC_CONFIG_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mc-config.XXXXXX")"
    export FS_U FS_P FS_SCRIPT MC_CONFIG_DIR
    # shellcheck disable=SC2016
    local status=0
    go run ./cmd/cluster-ops forward run --ns "$NS" --svc minio --remote-port 9000 -- \
        bash -c 'set -euo pipefail; export MC_HOST_minio="http://${FS_U}:${FS_P}@localhost:${FORWARD_PORT}"; eval "$FS_SCRIPT"' \
        || status=$?
    rm -rf "$MC_CONFIG_DIR"
    unset FS_U FS_P FS_SCRIPT
    return "$status"
}

# fs_minio_root <script> runs <script> with the MinIO root credential from
# Secret minio-root. Only bucket and user administration uses it.
fs_minio_root() {
    local user pass
    user=$(fs_secret_value minio-root rootUser)
    pass=$(fs_secret_value minio-root rootPassword)
    [[ -n "$user" && -n "$pass" ]] || fs_die "could not read rootUser/rootPassword from Secret minio-root in '$NS'."
    fs_minio_run "$user" "$pass" "$1"
}

# fs_minio_as <role> <script> runs <script> as filestash-<role> with keys
# from Secret filestash-users.
fs_minio_as() {
    local ak sk
    ak=$(fs_secret_value filestash-users "${1}AccessKey")
    sk=$(fs_secret_value filestash-users "${1}SecretKey")
    [[ -n "$ak" && -n "$sk" ]] || fs_die "Secret filestash-users has no $1 keys — run: just filestash create-users"
    fs_minio_run "$ak" "$sk" "$2"
}

# fs_filestash_run <script> runs <script> with FS_URL on a port-forward to
# Service filestash.
fs_filestash_run() {
    FS_SCRIPT="$1"
    export FS_SCRIPT
    # shellcheck disable=SC2016
    go run ./cmd/cluster-ops forward run --ns "$NS" --svc filestash --remote-port 8334 -- \
        bash -c 'set -euo pipefail; export FS_URL="http://localhost:${FORWARD_PORT}"; eval "$FS_SCRIPT"'
}

# fs_downloads_host prints the public MinIO host for direct file URLs: a
# `storage.*` host of the Gateway minio route, else its first host, else the
# minio apiIngress hostname (clusters with no Gateway stack).
fs_downloads_host() {
    local gw="gateway/Pulumi.${STACK}.yaml" mi="minio/Pulumi.${STACK}.yaml" host=""
    if [[ -f "$gw" && "$(yq -r '.config."gateway:properties".routes.minio.enabled // false' "$gw")" == "true" ]]; then
        host=$(yq -r '[.config."gateway:properties".routes.minio.hosts[]] | ((map(select(startswith("storage."))) + .)[0]) // ""' "$gw")
    elif [[ -f "$mi" && "$(yq -r '.config."minio:properties".apiIngress.enabled // false' "$mi")" == "true" ]]; then
        host=$(yq -r '.config."minio:properties".apiIngress.hostname // ""' "$mi")
    fi
    [[ -n "$host" && "$host" != *"<"* ]] || fs_die "no public MinIO host for stack '$STACK' — pass --url-host."
    printf '%s' "$host"
}

# fs_lock_take takes the publication Lease or fails with its holder.
# Sets FS_HOLDER. Release it with fs_lock_release in an EXIT trap.
fs_lock_take() {
    FS_HOLDER="$(id -un)@$(hostname -s 2>/dev/null || echo host):$$"
    FS_HOLDER="${FS_HOLDER//[^A-Za-z0-9@:._-]/_}"
    if ! sed -e "s|__NAMESPACE__|${NS}|" -e "s|__HOLDER__|${FS_HOLDER}|" \
        filestash/manifests/publish-lease.yaml | kubectl create -f - >/dev/null 2>&1; then
        local holder
        holder=$(kubectl -n "$NS" get lease filestash-publish -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
        fs_die "another publication holds lease/filestash-publish (holder '${holder}'). After a crash: just filestash release-lock --holder '${holder}'"
    fi
}

# fs_lock_release deletes the Lease only if this process holds it.
fs_lock_release() {
    local holder
    holder=$(kubectl -n "$NS" get lease filestash-publish -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
    if [[ -n "${FS_HOLDER:-}" && "$holder" == "$FS_HOLDER" ]]; then
        kubectl -n "$NS" delete lease filestash-publish >/dev/null
    fi
}
