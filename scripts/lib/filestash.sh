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

# fs_lock_take [<lease>] takes a Lease (default filestash-publish) or fails
# with its holder. Sets FS_HOLDER and FS_LEASE. Release it with
# fs_lock_release in an EXIT trap.
fs_lock_take() {
    FS_LEASE="${1:-filestash-publish}"
    FS_HOLDER="$(id -un)@$(hostname -s 2>/dev/null || echo host):$$"
    FS_HOLDER="${FS_HOLDER//[^A-Za-z0-9@:._-]/_}"
    local err
    if ! err=$(sed -e "s|__LEASE__|${FS_LEASE}|" -e "s|__NAMESPACE__|${NS}|" -e "s|__HOLDER__|${FS_HOLDER}|" \
        filestash/manifests/publish-lease.yaml | kubectl create -f - 2>&1 >/dev/null); then
        if [[ "$err" != *AlreadyExists* ]]; then
            FS_HOLDER=""
            fs_die "could not create lease/${FS_LEASE}: ${err}"
        fi
        local holder
        holder=$(kubectl -n "$NS" get lease "$FS_LEASE" -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
        FS_HOLDER=""
        fs_die "another run holds lease/${FS_LEASE} (holder '${holder}'). After a crash: just filestash release-lock --lease ${FS_LEASE} --holder '${holder}'"
    fi
}

# fs_lock_release deletes the Lease only if this process holds it.
fs_lock_release() {
    local holder
    [[ -n "${FS_HOLDER:-}" ]] || return 0
    holder=$(kubectl -n "$NS" get lease "$FS_LEASE" -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
    if [[ "$holder" == "$FS_HOLDER" ]]; then
        kubectl -n "$NS" delete lease "$FS_LEASE" >/dev/null
    fi
}

# fs_velero_ns <flag> sets FS_VELERO_NS from the flag, else from
# properties.namespace in install-velero/Pulumi.<stack>.yaml, else fails.
fs_velero_ns() {
    FS_VELERO_NS="${1:-}"
    local f="install-velero/Pulumi.${STACK}.yaml"
    if [[ -z "$FS_VELERO_NS" && -f "$f" ]]; then
        FS_VELERO_NS=$(yq -r '.config."install-velero:properties".namespace // ""' "$f")
    fi
    [[ -n "$FS_VELERO_NS" ]] || fs_die "no Velero namespace — pass --velero-namespace (no $f)."
}

# fs_suspend_backups suspends the state backup CronJob and records the old
# value in FS_SUSPEND_WAS. fs_resume_backups writes the old value back.
fs_suspend_backups() {
    FS_SUSPEND_WAS=$(kubectl -n "$NS" get cronjob filestash-state-backup -o jsonpath='{.spec.suspend}')
    [[ "$FS_SUSPEND_WAS" == "true" ]] || FS_SUSPEND_WAS=false
    kubectl -n "$NS" patch cronjob filestash-state-backup -p '{"spec":{"suspend":true}}' >/dev/null
}

fs_resume_backups() {
    [[ -n "${FS_SUSPEND_WAS:-}" ]] || return 0
    kubectl -n "$NS" patch cronjob filestash-state-backup -p "{\"spec\":{\"suspend\":${FS_SUSPEND_WAS}}}" >/dev/null
}

# fs_render <manifest> <namespace> prints the manifest with its placeholders filled.
fs_render() {
    sed -e "s|__NAMESPACE__|$2|g" -e "s|__SQLITE_IMAGE__|$(fs_sqlite_image)|g" "$1"
}

# fs_sqlite_image prints backup.sqliteImage, or the code default.
fs_sqlite_image() {
    local img
    img=$(fs_prop backup.sqliteImage)
    [[ -n "$img" ]] || img="keinos/sqlite3:3.54.1@sha256:b577e65892c6468d94b390097eda4488e05253470c161bdea68a05abb961fa17"
    printf '%s' "$img"
}

# fs_backup_idle succeeds when no state backup Job is unfinished (a Job can
# exist before its pod) and no state backup pod is pending or running.
fs_backup_idle() {
    local jobs pods
    jobs=$(kubectl -n "$NS" get jobs -l app.kubernetes.io/component=state-backup -o json \
        | jq '[.items[] | select(((.status.succeeded // 0) == 0) and ((.status.failed // 0) == 0))] | length')
    pods=$(kubectl -n "$NS" get pods -l app.kubernetes.io/component=state-backup \
        --field-selector=status.phase!=Succeeded,status.phase!=Failed -o name)
    [[ "$jobs" == "0" && -z "$pods" ]]
}

# fs_state_job <namespace> <mode> [<copy>] runs filestash-state-<mode>
# (mode check: verify a completed copy, changes nothing; mode restore:
# verify, then replace the state) and waits for it. An empty <copy> selects
# the newest completed copy.
fs_state_job() {
    local ns="$1" mode="$2" copy="${3:-}"
    kubectl -n "$ns" delete job "filestash-state-$mode" --ignore-not-found >/dev/null
    fs_render filestash/manifests/restore-job.yaml "$ns" \
        | sed -e "s|__COPY__|${copy}|g" -e "s|__MODE__|${mode}|g" | kubectl create -f -
    if ! kubectl -n "$ns" wait --for=condition=complete "job/filestash-state-$mode" --timeout=600s; then
        kubectl -n "$ns" logs "job/filestash-state-$mode" || true
        return 1
    fi
    kubectl -n "$ns" logs "job/filestash-state-$mode"
}

# fs_wait_no_pods <namespace> waits until no Filestash pod runs.
fs_wait_no_pods() {
    local i
    for ((i = 0; i < 90; i++)); do
        [[ -z "$(kubectl -n "$1" get pods -l app.kubernetes.io/name=filestash -o name)" ]] && return 0
        sleep 2
    done
    fs_die "a Filestash pod still runs in '$1'."
}
