#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

assert_contains() {
    local haystack="$1" needle="$2" label="$3"
    if [[ "$haystack" != *"$needle"* ]]; then
        echo "FAIL: $label: missing: $needle" >&2
        exit 1
    fi
}

render_dump=$(just --dry-run postgres dump-logical \
    --source-cluster dcr-experiments \
    --output scratch/postgres/test.dump 2>&1)
assert_contains "$render_dump" 'pg_dump' 'dump recipe'
assert_contains "$render_dump" '--format=custom' 'custom archive format'
assert_contains "$render_dump" 'source-cluster' 'source cluster resolution'
assert_contains "$render_dump" '.sha256' 'checksum output'

render_restore=$(just --dry-run postgres restore-logical \
    --archive scratch/postgres/test.dump \
    --app-password test-password 2>&1)
assert_contains "$render_restore" 'pg_restore' 'restore recipe'
assert_contains "$render_restore" '--exit-on-error' 'restore fail-fast'
assert_contains "$render_restore" '--single-transaction' 'restore transaction'
assert_contains "$render_restore" 'bootstrap.recovery' 'physical recovery cleanup'
assert_contains "$render_restore" 'sourceSecret' 'source secret cleanup'

render_reset=$(just --dry-run postgres reset-cluster --reset-data yes 2>&1)
assert_contains "$render_reset" 'cnpg.io/jobRole=full-recovery' 'stale recovery cleanup'
assert_contains "$render_reset" '_clear-own-backup-archive' 'own archive guard'
stack_config="$repo_root/cloudnative-pg-cluster/Pulumi.dcr-kube1.yaml"
! grep -q '^            recovery:' "$stack_config"
! grep -q '^    sourceSecret:' "$stack_config"
echo 'physical source config removed from target stack: PASS'

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT
mkdir -p "$tmp_dir/bin"
printf '{}' > "$tmp_dir/key.json"
printf 'archive' > "$tmp_dir/archive.dump"
if command -v sha256sum >/dev/null 2>&1; then
    digest=$(sha256sum "$tmp_dir/archive.dump" | awk '{print $1}')
else
    digest=$(shasum -a 256 "$tmp_dir/archive.dump" | awk '{print $1}')
fi
printf '%s  archive.dump\n' "$digest" > "$tmp_dir/archive.dump.sha256"
printf 'source_server_version=14.13\nclient_version=16.15\n' > "$tmp_dir/archive.dump.metadata"
log_file="$tmp_dir/calls.log"
: > "$log_file"

cat > "$tmp_dir/bin/just" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'just %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"_require-stack"*) printf 'test-stack\n' ;;
    *"deploy-cluster"*) exit 0 ;;
    *"_clear-own-backup-archive"*) exit 0 ;;
    *) exit 0 ;;
esac
EOF
cat > "$tmp_dir/bin/pulumi" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'GOOGLE_APPLICATION_CREDENTIALS=%s\n' "${GOOGLE_APPLICATION_CREDENTIALS:-}" >> "${STUB_LOG}"
printf 'pulumi %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"config get"*)
        if [[ "${STUB_PULUMI_CONFIG_ERROR:-no}" == yes ]]; then
            echo 'error: stack not found' >&2
            exit 42
        fi
        if [[ "${STUB_PULUMI_MISSING_PHYSICAL:-no}" == yes && "$*" == *"bootstrap.recovery"* ]]; then
            echo "error: configuration key 'properties.clusters[0].cluster.bootstrap.recovery.sourceCluster' not found for stack 'test-stack'" >&2
            exit 1
        fi
        if [[ "${STUB_PULUMI_MISSING_PHYSICAL:-no}" == yes && "$*" == *"sourceSecret"* ]]; then
            echo "error: configuration key 'properties.sourceSecret.name' not found for stack 'test-stack'" >&2
            exit 1
        fi
        ;;
esac
case "$*" in
    *"config get"*"backup.bucketPath"*) printf 'logto\n' ;;
    *"config get"*"backup.bucket"*) printf 'test-bucket\n' ;;
    *"config get"*"backupSecret.filepath"*) printf '%s\n' "${STUB_BACKUP_KEY}" ;;
    *"config get"*"bootstrap.recovery.sourceCluster"*) printf 'logto\n' ;;
    *"config get"*"image.tag"*) printf '%s\n' "${STUB_TARGET_TAG:-16.15-test}" ;;
    *"config rm"*) [[ "${STUB_PULUMI_RM_FAIL:-no}" != yes ]] ;;
    *"refresh"*) exit 0 ;;
    *) exit 0 ;;
esac
EOF
cat > "$tmp_dir/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"get cluster"*)
        if [[ "${STUB_KUBECTL_GET_CLUSTER_ERROR:-no}" == yes ]]; then
            echo 'Error from server (Forbidden): access denied' >&2
            exit 1
        fi
        if [[ "${STUB_CLUSTER_EXISTS:-no}" == yes ]]; then
            exit 0
        fi
        echo 'Error from server (NotFound): clusters.postgresql.cnpg.io "logto" not found' >&2
        exit 1
        ;;
    *"get secret"*"username"*) printf 'bG9ndG8=\n' ;;
    *"get secret"*"password"*) printf 'cHc=\n' ;;
    *"port-forward"*) exit 0 ;;
    *"get pods"*)
        if [[ "${STUB_PODS_REMAIN:-no}" == yes ]]; then
            printf 'pod/logto-1\n'
        fi
        exit 0
        ;;
    *) exit 0 ;;
esac
EOF
cat > "$tmp_dir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"pg_restore --version"*) printf 'pg_restore (PostgreSQL) 16.15\n' ;;
    *"pg_dump --version"*) printf 'pg_dump (PostgreSQL) %s\n' "${STUB_DUMP_CLIENT_VERSION:-16.15}" ;;
    *"psql"*) printf '%s\n' "${STUB_SOURCE_VERSION:-14.13}" ;;
    *"pg_restore --list"*)
        if [[ "${STUB_PG_RESTORE_LIST_FAIL:-no}" == yes ]]; then
            echo 'invalid archive' >&2
            exit 8
        fi
        ;;
esac
exit 0
EOF
cat > "$tmp_dir/bin/gcloud" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'gcloud %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"storage rm"*)
        if [[ "${STUB_GCLOUD_RM_FAIL:-no}" == yes ]]; then
            echo 'permission denied' >&2
            exit 13
        fi
        if [[ "${STUB_GCLOUD_NO_OBJECTS:-no}" == yes ]]; then
            echo 'ERROR: (gcloud.storage.rm) One or more URLs matched no objects.' >&2
            exit 1
        fi
        ;;
    *"storage ls"*)
        if [[ "${STUB_GCLOUD_LS_FAIL:-no}" == yes ]]; then
            echo 'permission denied' >&2
            exit 13
        fi
        if [[ "${STUB_GCLOUD_LS_NO_OBJECTS:-no}" == yes ]]; then
            echo 'ERROR: (gcloud.storage.ls) One or more URLs matched no objects.' >&2
            exit 1
        fi
        ;;
esac
exit 0
EOF
cat > "$tmp_dir/bin/nc" <<'EOF'
#!/usr/bin/env bash
if [[ -f "${STUB_NC_STATE}" ]]; then
    exit 0
fi
touch "${STUB_NC_STATE}"
exit 1
EOF
chmod +x "$tmp_dir/bin/just" "$tmp_dir/bin/pulumi" "$tmp_dir/bin/kubectl" "$tmp_dir/bin/docker" "$tmp_dir/bin/gcloud" "$tmp_dir/bin/nc"

run_stub() {
    run_stub_env no no no no "$1"
}

run_stub_env() {
    local cluster_exists="$1" pulumi_rm_fail="$2" gcloud_rm_fail="$3" gcloud_no_objects="$4" script="$5" missing_physical="${6:-no}"
    local nc_state="$tmp_dir/nc-state-${RANDOM}-$$"
    env \
        "PATH=$tmp_dir/bin:$PATH" \
        "STUB_LOG=$log_file" \
        "STUB_BACKUP_KEY=$tmp_dir/key.json" \
        "PULUMI_GCP_CREDENTIALS=$tmp_dir/key.json" \
        "GOOGLE_APPLICATION_CREDENTIALS=" \
        "PULUMI_STACK=test-stack" \
        "STUB_CLUSTER_EXISTS=$cluster_exists" \
        "STUB_PULUMI_RM_FAIL=$pulumi_rm_fail" \
        "STUB_GCLOUD_RM_FAIL=$gcloud_rm_fail" \
        "STUB_GCLOUD_NO_OBJECTS=$gcloud_no_objects" \
        "STUB_GCLOUD_LS_FAIL=${STUB_GCLOUD_LS_FAIL:-no}" \
        "STUB_GCLOUD_LS_NO_OBJECTS=${STUB_GCLOUD_LS_NO_OBJECTS:-no}" \
        "STUB_PULUMI_MISSING_PHYSICAL=$missing_physical" \
        "STUB_PG_RESTORE_LIST_FAIL=${STUB_PG_RESTORE_LIST_FAIL:-no}" \
        "STUB_KUBECTL_GET_CLUSTER_ERROR=${STUB_KUBECTL_GET_CLUSTER_ERROR:-no}" \
        "STUB_TARGET_TAG=${STUB_TARGET_TAG:-16.15-test}" \
        "STUB_PULUMI_CONFIG_ERROR=${STUB_PULUMI_CONFIG_ERROR:-no}" \
        "STUB_PODS_REMAIN=${STUB_PODS_REMAIN:-no}" \
        "STUB_SOURCE_VERSION=${STUB_SOURCE_VERSION:-14.13}" \
        "STUB_DUMP_CLIENT_VERSION=${STUB_DUMP_CLIENT_VERSION:-16.15}" \
        "STUB_NC_STATE=$nc_state" \
        bash -c "$script"
}

run_stub_flag() {
    local name="$1" value="$2"
    shift 2
    export "$name=$value"
    set +e
    run_stub_env "$@"
    local status=$?
    set -e
    unset "$name"
    return "$status"
}

dump_env="$tmp_dir/source.env"
dump_kubeconfig="$tmp_dir/source-kubeconfig.yaml"
touch "$dump_kubeconfig"
printf 'CLUSTER_NAME=fixture-source\nKUBECONFIG=%s\n' "$dump_kubeconfig" > "$dump_env"
dump_render=$(just --dry-run postgres dump-logical --source-cluster fixture-source --source-kubeconfig "$dump_kubeconfig" --source-env-file "$dump_env" --output "$tmp_dir/dump.dump" 2>&1)
restore_render=$(just --dry-run postgres restore-logical --archive "$tmp_dir/archive.dump" --app-password test-password 2>&1)
restore_replace_render=$(just --dry-run postgres restore-logical --archive "$tmp_dir/archive.dump" --app-password test-password --replace-data yes 2>&1)

: > "$log_file"
if run_stub_flag STUB_SOURCE_VERSION 17.13 no no no no "$dump_render" >/dev/null 2>&1; then
    echo 'FAIL: dump source-major mismatch unexpectedly succeeded' >&2
    exit 1
fi
! grep -q 'pg_dump -h' "$log_file"
! test -f "$tmp_dir/dump.dump"
echo 'dump source-major guard/no mutation: PASS'

: > "$log_file"
cp "$tmp_dir/archive.dump.sha256" "$tmp_dir/bad.sha256"
printf 'bad  archive.dump\n' > "$tmp_dir/archive.dump.sha256"
if run_stub "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: checksum failure unexpectedly succeeded' >&2
    exit 1
fi
! grep -qE 'pulumi|kubectl|gcloud|docker' "$log_file"
cp "$tmp_dir/bad.sha256" "$tmp_dir/archive.dump.sha256"
echo 'checksum guard/no mutation: PASS'

: > "$log_file"
if run_stub "$(just --dry-run postgres restore-logical --archive "$tmp_dir/archive.dump" --replace-data yes 2>&1)" >/dev/null 2>&1; then
    echo 'FAIL: missing password unexpectedly succeeded' >&2
    exit 1
fi
! grep -qE 'pulumi|kubectl|gcloud|docker' "$log_file"
echo 'password guard/no mutation: PASS'

: > "$log_file"
if run_stub_env yes no no no "$restore_render" >/dev/null 2>&1; then
    echo 'FAIL: existing target without replace unexpectedly succeeded' >&2
    exit 1
fi
! grep -qE 'pulumi config rm|gcloud|docker|kubectl delete|just deploy-cluster' "$log_file"
echo 'replace-data guard/no mutation: PASS'

: > "$log_file"
if run_stub_env no yes no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: Pulumi config failure unexpectedly succeeded' >&2
    exit 1
fi
if ! grep -q 'pulumi .*config rm' "$log_file"; then
    echo 'FAIL: Pulumi config rm was not reached; calls:' >&2
    cat "$log_file" >&2
    exit 1
fi
! grep -q 'deploy-cluster' "$log_file"
echo 'Pulumi config failure propagation: PASS'

: > "$log_file"
helper_render=$(just --dry-run postgres _clear-own-backup-archive --folder cloudnative-pg-cluster --cluster logto --stack test-stack 2>&1)
if run_stub_env no no yes no "$helper_render" >/dev/null 2>&1; then
    echo 'FAIL: GCS cleanup failure unexpectedly succeeded' >&2
    exit 1
fi
grep -q 'gcloud storage rm' "$log_file"
echo 'GCS cleanup failure propagation: PASS'

: > "$log_file"
run_stub_env no no no yes "$helper_render" >/dev/null
printf 'no-object cleanup: PASS\n'

: > "$log_file"
run_stub_env no no no no "$restore_replace_render" >/dev/null
refresh_line=$(grep -n 'pulumi .*refresh' "$log_file" | head -1 | cut -d: -f1)
deploy_line=$(grep -n 'just .*deploy-cluster' "$log_file" | head -1 | cut -d: -f1)
[[ -n "$refresh_line" && -n "$deploy_line" && "$refresh_line" -lt "$deploy_line" ]]
! grep -q 'PGPASSWORD=pw' "$log_file"
pulumi_credential_line=$(grep '^GOOGLE_APPLICATION_CREDENTIALS=' "$log_file" | head -1)
[[ "$pulumi_credential_line" == "GOOGLE_APPLICATION_CREDENTIALS=$tmp_dir/key.json" ]]
echo 'refresh-before-deploy/password argv hygiene: PASS'

: > "$log_file"
if run_stub_flag STUB_PG_RESTORE_LIST_FAIL yes no no no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: pg_restore --list failure unexpectedly succeeded' >&2
    exit 1
fi
! grep -qE 'pulumi config rm|gcloud|kubectl delete|deploy-cluster' "$log_file"
echo 'archive format guard/no mutation: PASS'

printf 'source_server_version=17.13\nclient_version=17.13\n' > "$tmp_dir/archive.dump.metadata"
if run_stub_env no no no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: source-major mismatch unexpectedly succeeded' >&2
    exit 1
fi
! grep -qE 'pulumi config rm|gcloud|kubectl delete|deploy-cluster' "$log_file"
printf 'source_server_version=14.13\nclient_version=16.15\n' > "$tmp_dir/archive.dump.metadata"
echo 'source-major guard/no mutation: PASS'

: > "$log_file"
if run_stub_flag STUB_TARGET_TAG 17.13 no no no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: target/client major mismatch unexpectedly succeeded' >&2
    exit 1
fi
! grep -qE 'pulumi config rm|gcloud|kubectl delete|deploy-cluster' "$log_file"
echo 'target-major guard/no mutation: PASS'

: > "$log_file"
if run_stub_flag STUB_KUBECTL_GET_CLUSTER_ERROR yes no no no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: Kubernetes connectivity error unexpectedly succeeded' >&2
    exit 1
fi
! grep -q 'pulumi .*config rm' "$log_file"
echo 'Kubernetes error propagation: PASS'

: > "$log_file"
if run_stub_flag STUB_PULUMI_CONFIG_ERROR yes no no no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: Pulumi stack error unexpectedly succeeded' >&2
    exit 1
fi
! grep -q 'pulumi .*config rm' "$log_file"
echo 'Pulumi read error propagation: PASS'

: > "$log_file"
missing_output=$(run_stub_env no no no no "$restore_render" yes 2>&1) || {
    echo 'FAIL: exact missing-key diagnostic was not tolerated' >&2
    printf '%s\n' "$missing_output" >&2
    exit 1
}
echo 'exact missing-key tolerance: PASS'

: > "$log_file"
if run_stub_flag STUB_GCLOUD_LS_FAIL yes no no no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: GCS list error unexpectedly succeeded' >&2
    exit 1
fi
! grep -q 'pulumi .*config rm' "$log_file"
echo 'GCS list error propagation: PASS'

: > "$log_file"
if ! run_stub_flag STUB_GCLOUD_LS_NO_OBJECTS yes no no no no "$restore_replace_render" >/dev/null 2>&1; then
    echo 'FAIL: exact no-object diagnostic was not tolerated' >&2
    exit 1
fi
echo 'exact no-object tolerance: PASS'

reset_render=$(just --dry-run postgres reset-cluster --reset-data yes --retries 1 --interval 0 2>&1)
: > "$log_file"
run_stub_env yes no no no "$reset_render" >/dev/null 2>&1 || true
job_delete_line=$(grep -n 'kubectl delete jobs,pods' "$log_file" | head -1 | cut -d: -f1)
pod_wait_line=$(grep -n 'kubectl get pods' "$log_file" | head -1 | cut -d: -f1)
[[ -n "$job_delete_line" && -n "$pod_wait_line" && "$job_delete_line" -lt "$pod_wait_line" ]]
echo 'stale recovery cleanup ordering: PASS'

: > "$log_file"
if run_stub_flag STUB_PODS_REMAIN yes yes no no no "$reset_render" >/dev/null 2>&1; then
    echo 'FAIL: remaining pods reset unexpectedly succeeded' >&2
    exit 1
fi
! grep -q 'kubectl delete pvc' "$log_file"
echo 'remaining pod protects PVCs: PASS'

source_env="$tmp_dir/source.env"
source_kubeconfig_fixture="$tmp_dir/source-kubeconfig.yaml"
touch "$source_kubeconfig_fixture"
printf 'CLUSTER_NAME=fixture-source\nKUBECONFIG=\${PWD}/\${CLUSTER_NAME}-kubeconfig.yaml\n' > "$source_env"
source_kubeconfig=$(sed -n 's/^KUBECONFIG=//p' "$source_env" | tail -1)
source_cluster_name=$(sed -n 's/^CLUSTER_NAME=//p' "$source_env" | tail -1)
source_kubeconfig="${source_kubeconfig//\$\{PWD\}/$tmp_dir}"
source_kubeconfig="${source_kubeconfig//\$PWD/$tmp_dir}"
source_kubeconfig="${source_kubeconfig//\$\{CLUSTER_NAME\}/$source_cluster_name}"
source_kubeconfig="${source_kubeconfig//\$CLUSTER_NAME/$source_cluster_name}"
# The fixture path is intentionally named after CLUSTER_NAME.
mkdir -p "$(dirname "$source_kubeconfig")"
touch "$source_kubeconfig"
test -f "$source_kubeconfig"
echo 'source env kubeconfig resolution: PASS'

printf '%s\n' 'logical postgres recipe contract: PASS'

assert_before() {
    local render="$1" early="$2" late="$3" label="$4"
    local early_line late_line
    early_line=$(echo "$render" | grep -nF "$early" | head -1 | cut -d: -f1)
    late_line=$(echo "$render" | grep -nF "$late" | head -1 | cut -d: -f1)
    if [ -z "$early_line" ] || [ -z "$late_line" ]; then
        echo "FAIL: $label: marker missing (early='$early' late='$late')" >&2
        exit 1
    fi
    if [ "$early_line" -ge "$late_line" ]; then
        echo "FAIL: $label: '$early' (line $early_line) must precede '$late' (line $late_line)" >&2
        exit 1
    fi
}

# Destructive ordering: every preflight check must appear before the first
# mutating command. Guards the fail-closed contract from commits 4634d6d,
# a5b7e71, d078722, 73659d5 — a regression that reorders restore before
# validation fails here.
assert_before "$render_restore" 'Error: archive checksum mismatch' 'pg_restore -h' 'checksum before restore'
assert_before "$render_restore" 'majors are incompatible' 'pg_restore -h' 'major compat before restore'
assert_before "$render_restore" 'Error: pg_restore cannot read archive' 'pg_restore -h' 'archive readable before restore'

# Post-restore hygiene: reopen the port-forward before ANALYZE (commit 4ae1c91).
# Two port-forward invocations expected (open before restore, reopen after);
# the reopen must precede ANALYZE.
pf_first=$(echo "$render_restore" | grep -n 'port-forward' | head -1 | cut -d: -f1)
pf_reopen=$(echo "$render_restore" | grep -n 'port-forward' | tail -1 | cut -d: -f1)
analyze_line=$(echo "$render_restore" | grep -n 'ANALYZE VERBOSE' | cut -d: -f1)
if [ "$pf_first" = "$pf_reopen" ] || [ -z "$pf_first" ]; then
    echo "FAIL: expected two port-forward invocations (open + reopen), found one" >&2
    exit 1
fi
if [ "$pf_reopen" -ge "$analyze_line" ]; then
    echo "FAIL: reopen port-forward (line $pf_reopen) must precede ANALYZE (line $analyze_line)" >&2
    exit 1
fi

# Idempotency: a second identical dry-run must render byte-identical output.
render_restore_2=$(just --dry-run postgres restore-logical \
    --archive scratch/postgres/test.dump \
    --app-password test-password 2>&1)
if [ "$render_restore" != "$render_restore_2" ]; then
    echo "FAIL: restore-logical dry-run not deterministic across two runs" >&2
    diff <(echo "$render_restore") <(echo "$render_restore_2") | head -20 >&2
    exit 1
fi
echo 'destructive ordering + idempotency: PASS'
