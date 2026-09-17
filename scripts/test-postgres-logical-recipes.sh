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

printf '%s\n' 'logical postgres recipe contract: PASS'
