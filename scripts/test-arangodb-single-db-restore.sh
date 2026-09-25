#!/usr/bin/env bash
# Contract tests for the single-database restore path: the arangodb
# restore-database / import-database composites, configure-restore
# --database, and the read-only probe helpers they share (_restic-ls,
# _database-exists, _preflight-database-restore).
#
# Everything cloud-facing is mocked (kubectl, pulumi) and every nested
# `just arangodb ...` call is intercepted, so this runs in `just check`
# without credentials, a cluster, or Docker. It asserts the two things a
# destructive composite cannot get wrong: read-only preflights run before the
# first mutation, and the stack's single-database keys are cleared on every
# exit path.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
just_bin="$(command -v just)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"

export MOCK_CALLS="$tmp/calls.log"
export MOCK_BIN="$tmp/bin"
export REAL_JUST="$just_bin"
: > "$MOCK_CALLS"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

# Line numbers of every recorded call matching a pattern.
calls_matching() {
    grep -n -e "$1" "$MOCK_CALLS" | cut -d: -f1
}

# assert_order <earlier-pattern> <later-pattern>
assert_order() {
    local first last
    first=$(calls_matching "$1" | head -n1)
    last=$(calls_matching "$2" | tail -n1)
    [[ -n "$first" ]] || fail "no recorded call matches '$1'"
    [[ -n "$last" ]] || fail "no recorded call matches '$2'"
    if (( first >= last )); then
        echo "--- recorded calls ---" >&2
        cat "$MOCK_CALLS" >&2
        fail "expected '$1' (line $first) before '$2' (line $last)"
    fi
}

# Intercepting `just`: recipes listed as passthrough run for real (so the
# probe helpers' own logic is exercised against the mocked kubectl), the rest
# are recorded and answered from canned values. MOCK_INNER=1 makes the shim
# transparent for the rest of the subtree — without it, a real recipe's own
# `just arangodb ...` calls would re-enter the shim forever.
cat > "$MOCK_BIN/just" <<'MOCK_JUST'
#!/usr/bin/env bash
set -euo pipefail
printf 'just %s\n' "$*" >> "$MOCK_CALLS"

if [[ "${MOCK_INNER:-0}" == "1" ]]; then
    exec "$REAL_JUST" "$@"
fi

case "${2:-}" in
    _require-stack)
        printf '%s\n' dcr-kube1
        ;;
    _restic-ls|_database-exists|_arangosh-probe|_database-has-collections|_reset-single-database-config)
        exec env MOCK_INNER=1 "$REAL_JUST" "$@"
        ;;
    _restic-snapshots-json)
        printf '%s\n' '[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","time":"2026-09-25T10:00:00Z"}]'
        ;;
    apply-restore)
        exit "${MOCK_APPLY_RESTORE_RC:-0}"
        ;;
    *)
        exit 0
        ;;
esac
MOCK_JUST
chmod +x "$MOCK_BIN/just"

cat > "$MOCK_BIN/kubectl" <<'MOCK_KUBECTL'
#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "$MOCK_CALLS"
args=" $* "

case "${1:-}" in
    get)
        case "${2:-}" in
            secret) printf '{"data":{"password":"cm9vdA=="}}\n' ;;
            svc) printf 'arangodb\n' ;;
            jobs) printf '{"items":[]}\n' ;;
            namespace) exit 0 ;;
            *) echo "unexpected kubectl get: $*" >&2; exit 1 ;;
        esac
        ;;
    run)
        case "$args" in
            *restic-ls-*)
                if [[ "${MOCK_SNAPSHOT_HAS_PATH:-yes}" == "yes" ]]; then
                    printf 'snapshot a1b2c3 of [/arangodump] at 2026-09-25T10:00:00Z:\n'
                    printf '/arangodump/mydb\n/arangodump/mydb/ENCRYPTION\n'
                else
                    printf 'snapshot a1b2c3 of [/arangodump] at 2026-09-25T10:00:00Z:\n'
                    printf '/arangodump/other\n'
                fi
                ;;
            *DATABASE_EMPTY*)
                if [[ "${MOCK_DATABASE_EMPTY:-no}" == "yes" ]]; then
                    printf 'DATABASE_EMPTY\n'
                else
                    printf 'DATABASE_OK 12\n'
                fi
                ;;
            *DATABASE_MISSING*)
                if [[ "${MOCK_DATABASE_STATE:-absent}" == "present" ]]; then
                    printf 'DATABASE_PRESENT\n'
                else
                    printf 'DATABASE_MISSING\n'
                fi
                ;;
            *)
                echo "unexpected kubectl run: $*" >&2
                exit 1
                ;;
        esac
        ;;
    *)
        echo "unexpected kubectl call: $*" >&2
        exit 1
        ;;
esac
MOCK_KUBECTL
chmod +x "$MOCK_BIN/kubectl"

cat > "$MOCK_BIN/pulumi" <<'MOCK_PULUMI'
#!/usr/bin/env bash
set -euo pipefail
printf 'pulumi %s\n' "$*" >> "$MOCK_CALLS"
exit 0
MOCK_PULUMI
chmod +x "$MOCK_BIN/pulumi"

export PATH="$MOCK_BIN:$PATH"
export PULUMI_GCP_CREDENTIALS="$tmp/credentials.json"
: > "$PULUMI_GCP_CREDENTIALS"
export PULUMI_STACK=dcr-kube1
cd "$repo_root"

echo "=== 1. _restic-ls proves the snapshot holds the database subdirectory ==="
: > "$MOCK_CALLS"
out=$("$just_bin" arangodb _restic-ls --namespace dev \
    --bucket restic-arangodb-backup-prod --secret dictycr \
    --snapshot latest --path /arangodump/mydb)
grep -q '^/arangodump/mydb$' <<<"$out" || fail "_restic-ls did not print the matching path: $out"
grep -q '^/arangodump/mydb/ENCRYPTION$' <<<"$out" || fail "_restic-ls dropped the nested path: $out"

set +e
out=$(MOCK_SNAPSHOT_HAS_PATH=no "$just_bin" arangodb _restic-ls --namespace dev \
    --bucket restic-arangodb-backup-prod --secret dictycr \
    --snapshot latest --path /arangodump/mydb 2>&1)
rc=$?
set -e
[[ "$rc" -ne 0 ]] || fail "_restic-ls accepted a snapshot without the path"
grep -q "no '/arangodump/mydb'" <<<"$out" || fail "missing explanation for the absent path: $out"

echo "=== 2. _database-exists distinguishes absent (3) from present (0) ==="
set +e
"$just_bin" arangodb _database-exists --namespace dev --server arangodb \
    --database mydb --stack dcr-kube1 >/dev/null 2>&1
rc=$?
set -e
[[ "$rc" -eq 3 ]] || fail "absent database must exit 3, got $rc"

set +e
MOCK_DATABASE_STATE=present "$just_bin" arangodb _database-exists --namespace dev \
    --server arangodb --database mydb --stack dcr-kube1 >/dev/null 2>&1
rc=$?
set -e
[[ "$rc" -eq 0 ]] || fail "present database must exit 0, got $rc"

echo "=== 3. preflight fails closed on an existing database without --overwrite ==="
: > "$MOCK_CALLS"
set +e
out=$(MOCK_DATABASE_STATE=present "$just_bin" arangodb _preflight-database-restore \
    --namespace dev --server arangodb --database mydb \
    --bucket restic-arangodb-backup-prod --secret dictycr \
    --snapshot latest --stack dcr-kube1 2>&1)
rc=$?
set -e
[[ "$rc" -ne 0 ]] || fail "preflight accepted an existing database without --overwrite"
grep -q -- '--overwrite yes' <<<"$out" || fail "abort message must name --overwrite yes: $out"
! grep -q '^pulumi ' "$MOCK_CALLS" || fail "failing preflight must not mutate any Pulumi config"

MOCK_DATABASE_STATE=present "$just_bin" arangodb _preflight-database-restore \
    --namespace dev --server arangodb --database mydb \
    --bucket restic-arangodb-backup-prod --secret dictycr \
    --snapshot latest --stack dcr-kube1 --overwrite yes > "$tmp/preflight-overwrite.out" 2>&1
grep -q 'overwrite yes was given' "$tmp/preflight-overwrite.out" || fail "explicit overwrite not acknowledged"

"$just_bin" arangodb _preflight-database-restore \
    --namespace dev --server arangodb --database mydb \
    --bucket restic-arangodb-backup-prod --secret dictycr \
    --snapshot latest --stack dcr-kube1 > "$tmp/preflight-absent.out" 2>&1
grep -q 'does not exist yet' "$tmp/preflight-absent.out" || fail "absent database not reported as creatable"

echo "=== 4. configure-restore --database writes the keys after the preflight ==="
: > "$MOCK_CALLS"
"$just_bin" arangodb configure-restore --namespace dev --server arangodb \
    --database mydb --overwrite yes --restore-id dbrestore-test \
    --stack dcr-kube1 > "$tmp/configure-db.out"
assert_order '_preflight-database-restore' 'gcp-pulumi ensure-stack'
assert_order '_preflight-database-restore' 'config set-all'
grep -q -- '--plaintext arangodb-restore:properties.database=mydb' "$MOCK_CALLS" || fail "database key not written"
grep -q -- '--plaintext arangodb-restore:properties.overwrite=true' "$MOCK_CALLS" || fail "overwrite key not written"
grep -q -- '--plaintext arangodb-restore:properties.confirmTarget=dev/arangodb/dbrestore-test' "$MOCK_CALLS" || fail "confirmTarget not written"
grep -q 'database      : mydb' "$tmp/configure-db.out" || fail "single-database mode not echoed"

echo "=== 5. configure-restore without --database clears stale single-database keys ==="
: > "$MOCK_CALLS"
"$just_bin" arangodb configure-restore --namespace dev --server arangodb \
    --restore-id drill-test --stack dcr-kube1 > "$tmp/configure-whole.out"
grep -q -- 'config rm --stack dcr-kube1 --path properties.database' "$MOCK_CALLS" || fail "stale properties.database not cleared"
grep -q -- 'config rm --stack dcr-kube1 --path properties.overwrite' "$MOCK_CALLS" || fail "stale properties.overwrite not cleared"
! grep -q -- 'properties.database=' "$MOCK_CALLS" || fail "whole-instance run must not write a database key"

echo "=== 6. restore-database: preflight -> configure -> apply -> verify -> cleanup ==="
: > "$MOCK_CALLS"
"$just_bin" arangodb restore-database --namespace dev --database mydb \
    --overwrite yes --restore-id dbrestore-test --stack dcr-kube1 > "$tmp/restore-db.out"
assert_order '_preflight-database-restore' 'arangodb configure-restore'
assert_order 'arangodb configure-restore' 'arangodb apply-restore'
assert_order 'arangodb apply-restore' 'arangodb _database-has-collections'
assert_order 'arangodb _database-has-collections' 'arangodb _reset-single-database-config'
grep -q -- 'arangodb configure-restore --namespace dev --database mydb' "$MOCK_CALLS" || fail "configure-restore not called with the database"
grep -q -- '--overwrite yes' "$MOCK_CALLS" || fail "overwrite not propagated to configure-restore"
grep -q -- '_preflight-database-restore --namespace dev --server arangodb --database mydb --bucket restic-arangodb-backup-prod --secret dictycr' "$MOCK_CALLS" || fail "preflight did not read the stack's own bucket/secret"
grep -q 'users/grants are NOT restored' "$tmp/restore-db.out" || fail "missing users/grants caveat"

echo "=== 7. restore-database: a failed apply-restore still clears the keys ==="
: > "$MOCK_CALLS"
set +e
MOCK_APPLY_RESTORE_RC=1 "$just_bin" arangodb restore-database --namespace dev \
    --database mydb --restore-id dbrestore-fail --stack dcr-kube1 > "$tmp/restore-db-fail.out" 2>&1
rc=$?
set -e
[[ "$rc" -ne 0 ]] || fail "restore-database reported success after apply-restore failed"
grep -q -- 'config rm --stack dcr-kube1 --path properties.database' "$MOCK_CALLS" || fail "trap did not clear properties.database after the failure"

echo "=== 8. import-database: source-bucket preflight, bootstrap, DR reset ==="
: > "$MOCK_CALLS"
"$just_bin" arangodb import-database --namespace prod \
    --bucket source-restic-bucket --database mydb --secret dictycr-source \
    --server arangodb --restore-id import-test --stack dcr-kube1 > "$tmp/import-db.out"
assert_order '_preflight-database-restore' 'arangodb configure-bootstrap'
assert_order 'arangodb configure-bootstrap' 'arangodb apply-restore'
assert_order 'arangodb apply-restore' 'arangodb _database-has-collections'
assert_order 'arangodb _database-has-collections' 'arangodb reset-restore-config'
grep -q -- '--bucket source-restic-bucket --secret dictycr-source' "$MOCK_CALLS" || fail "import preflight did not use the source bucket/secret"
grep -q -- 'arangodb configure-bootstrap --namespace prod --bucket source-restic-bucket --database mydb' "$MOCK_CALLS" || fail "configure-bootstrap not called with the source bucket"

echo "=== 9. reset-restore-config also clears the single-database keys ==="
: > "$MOCK_CALLS"
"$just_bin" arangodb reset-restore-config --stack dcr-kube1 >/dev/null
for key in database overwrite snapshot restoreId confirmTarget; do
    grep -q -- "config rm --stack dcr-kube1 --path properties.$key" "$MOCK_CALLS" \
        || fail "reset-restore-config does not clear properties.$key"
done

echo "ArangoDB single-database restore contract tests PASSED"