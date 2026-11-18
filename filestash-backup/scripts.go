package main

import "fmt"

const (
	statePath  = "/app/data/state"
	backupPath = "/backup"
	backupTag  = "filestash-backup"
	backupHost = "filestash"
)

// copyScript makes an online, consistent copy of the Filestash state while
// Filestash keeps running: sqlite3 .backup of each SQLite file,
// PRAGMA integrity_check, the row count of each table, config.json, and
// SHA256SUMS. Any failed step fails the Job, so restic never uploads a
// partial state.
const copyScript = `set -eu
S=/app/data/state; O=/backup/state
rm -rf "$O"; mkdir -p "$O/db" "$O/config"
for f in "$S"/db/*.sql; do
  [ -e "$f" ] || continue
  n=$(basename "$f")
  sqlite3 "$f" ".backup '$O/db/$n'"
  r=$(sqlite3 "$O/db/$n" "PRAGMA integrity_check")
  [ "$r" = ok ] || { echo "integrity_check $n: $r"; exit 1; }
  for t in $(sqlite3 "$O/db/$n" "SELECT name FROM sqlite_master WHERE type='table'"); do
    echo "$n $t $(sqlite3 "$O/db/$n" "SELECT count(*) FROM \"$t\"")" >> "$O/counts.txt"
  done
done
touch "$O/counts.txt"
cp "$S/config/config.json" "$O/config/config.json"
grep -q '"secret_key"' "$O/config/config.json" || { echo "config.json has no general.secret_key"; exit 1; }
(cd "$O" && find db config -type f | sort | xargs sha256sum > SHA256SUMS)
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ); files=$(wc -l < "$O/SHA256SUMS")
printf '{"timestamp":"%s","files":%s}\n' "$ts" "$files" > "$O/manifest.json"
echo "state copy ready"`

// resticScript initializes the repository on first run, snapshots the copy,
// and applies the retention policy.
func resticScript(c *BackupConfig) string {
	return fmt.Sprintf(`set -eu
restic cat config >/dev/null 2>&1 || restic init
restic backup %[1]s/state --tag %[2]s --host %[3]s
restic forget --tag %[2]s --host %[3]s --keep-last %[4]d --keep-daily %[5]d --prune`,
		backupPath, backupTag, backupHost, c.KeepLast, c.KeepDaily)
}
