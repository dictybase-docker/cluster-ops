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
printf 'source_server_version=14.13\n' > "$tmp_dir/archive.dump.metadata"
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
printf 'pulumi %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"config get"*"backup.bucketPath"*) printf 'logto\n' ;;
    *"config get"*"backup.bucket"*) printf 'test-bucket\n' ;;
    *"config get"*"backupSecret.filepath"*) printf '%s\n' "${STUB_BACKUP_KEY}" ;;
    *"config get"*"bootstrap.recovery.sourceCluster"*) printf 'logto\n' ;;
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
    *"get cluster"*) [[ "${STUB_CLUSTER_EXISTS:-no}" == yes ]] ;;
    *"get secret"*"username"*) printf 'bG9ndG8=\n' ;;
    *"get secret"*"password"*) printf 'cHc=\n' ;;
    *"port-forward"*) exit 0 ;;
    *"get pods"*) exit 0 ;;
    *) exit 0 ;;
esac
EOF
cat > "$tmp_dir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >> "${STUB_LOG}"
case "$*" in
    *"pg_restore --version"*) printf 'pg_restore (PostgreSQL) 16.15\n' ;;
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
            echo 'One or more URLs matched no objects.' >&2
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
    local cluster_exists="$1" pulumi_rm_fail="$2" gcloud_rm_fail="$3" gcloud_no_objects="$4" script="$5"
    env \
        "PATH=$tmp_dir/bin:$PATH" \
        "STUB_LOG=$log_file" \
        "STUB_BACKUP_KEY=$tmp_dir/key.json" \
        "PULUMI_GCP_CREDENTIALS=$tmp_dir/key.json" \
        "PULUMI_STACK=test-stack" \
        "STUB_CLUSTER_EXISTS=$cluster_exists" \
        "STUB_PULUMI_RM_FAIL=$pulumi_rm_fail" \
        "STUB_GCLOUD_RM_FAIL=$gcloud_rm_fail" \
        "STUB_GCLOUD_NO_OBJECTS=$gcloud_no_objects" \
        "STUB_NC_STATE=$tmp_dir/nc-state-$$" \
        bash -c "$script"
}

restore_render=$(just --dry-run postgres restore-logical --archive "$tmp_dir/archive.dump" --app-password test-password 2>&1)
restore_replace_render=$(just --dry-run postgres restore-logical --archive "$tmp_dir/archive.dump" --app-password test-password --replace-data yes 2>&1)

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
grep -q 'pulumi .*config rm' "$log_file"
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
echo 'refresh-before-deploy: PASS'

source_env="$repo_root/.env.dev.dcr-experiments"
source_kubeconfig=$(sed -n 's/^KUBECONFIG=//p' "$source_env" | tail -1)
source_cluster_name=$(sed -n 's/^CLUSTER_NAME=//p' "$source_env" | tail -1)
source_kubeconfig="${source_kubeconfig//\$\{PWD\}/$repo_root}"
source_kubeconfig="${source_kubeconfig//\$PWD/$repo_root}"
source_kubeconfig="${source_kubeconfig//\$\{CLUSTER_NAME\}/$source_cluster_name}"
source_kubeconfig="${source_kubeconfig//\$CLUSTER_NAME/$source_cluster_name}"
test -f "$source_kubeconfig"
echo 'source env kubeconfig resolution: PASS'

printf '%s\n' 'logical postgres recipe contract: PASS'
