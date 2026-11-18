package main

import "fmt"

const (
	statePath  = "/app/data/state"
	backupPath = "/backup"
	backupTag  = "filestash-backup"
	backupHost = "filestash"
)

// copyScript makes an online copy of the Filestash state while Filestash
// keeps running: sqlite3 .backup of each SQLite file (consistent per file,
// not across files and config.json), PRAGMA integrity_check, the row count
// of each table, and config.json. It fails closed: share.sql must exist,
// config.json must be valid JSON with a nonempty general.secret_key, and
// SHA256SUMS covers every file including counts.txt. The manifest records
// the SHA-256 of CONFIG_SECRET, so a restore can reject a different key.
// Any failed step fails the Job before restic uploads anything.
const copyScript = `set -eu
S=/app/data/state; O=/backup/state
rm -rf "$O"; mkdir -p "$O/db" "$O/config"
[ -f "$S/db/share.sql" ] || { echo "no share.sql in $S/db"; exit 1; }
: > "$O/counts.txt"
for f in "$S"/db/*.sql; do
  n=$(basename "$f")
  sqlite3 "$f" ".backup '$O/db/$n'"
  r=$(sqlite3 "$O/db/$n" "PRAGMA integrity_check")
  [ "$r" = ok ] || { echo "integrity_check $n: $r"; exit 1; }
  for t in $(sqlite3 "$O/db/$n" "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"); do
    echo "$n $t $(sqlite3 "$O/db/$n" "SELECT count(*) FROM \"$t\"")" >> "$O/counts.txt"
  done
done
[ -f "$S/config/config.json" ] || { echo "no config.json in $S/config"; exit 1; }
cp "$S/config/config.json" "$O/config/config.json"
k=$(sqlite3 :memory: "SELECT coalesce(json_extract(readfile('$O/config/config.json'), '$.general.secret_key'), '')")
[ -n "$k" ] || { echo "config.json has no general.secret_key"; exit 1; }
[ -n "${CONFIG_SECRET:-}" ] || { echo "CONFIG_SECRET is not set"; exit 1; }
fp=$(printf '%s' "$CONFIG_SECRET" | sha256sum | cut -c1-64)
(cd "$O" && { find db config -type f; echo counts.txt; } | sort | xargs sha256sum > SHA256SUMS)
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ); files=$(wc -l < "$O/SHA256SUMS")
printf '{"timestamp":"%s","files":%s,"configSecretSha256":"%s"}\n' "$ts" "$files" "$fp" > "$O/manifest.json"
echo "state copy ready"`

// resticScript initializes the repository only when restic reports that it
// does not exist (exit code 10, restic 0.17), snapshots the copy, and applies
// the retention policy. A wrong password, a lock, or a network error stops
// the Job.
func resticScript(c *BackupConfig) string {
	return fmt.Sprintf(`set -eu
rc=0
restic cat config >/dev/null || rc=$?
case "$rc" in
  0) ;;
  10) echo "repository does not exist: restic init"; restic init ;;
  *) echo "restic repository check failed (exit $rc): not initializing"; exit "$rc" ;;
esac
restic backup %[1]s/state --tag %[2]s --host %[3]s
restic forget --tag %[2]s --host %[3]s --keep-last %[4]d --keep-daily %[5]d --prune`,
		backupPath, backupTag, backupHost, c.KeepLast, c.KeepDaily)
}
