package main

import (
	"fmt"
	"strconv"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	P "github.com/IBM/fp-go/v2/predicate"
)

const (
	argRepository = "-r"
	argNoLock     = "--no-lock"
	argRestore    = "restore"
	argTarget     = "--target"
)

var (
	noLockFlags = F.Pipe1(
		func(noLock bool) bool { return noLock },
		P.Fold(
			F.Constant1[bool, []string](nil),
			F.Constant1[bool]([]string{argNoLock}),
		),
	)

	overwriteEnvValue = F.Pipe1(
		func(overwrite bool) bool { return overwrite },
		P.Fold(
			F.Constant1[bool]("false"),
			F.Constant1[bool]("true"),
		),
	)
)

func resticRepository(cfg *RestoreConfig) string {
	return fmt.Sprintf("gs:%s:/", cfgBucketLens.Get(cfg))
}

func scratchDumpPath() string {
	return fmt.Sprintf("%s/%s", scratchMountPath, dumpFilename)
}

func buildResticRestoreArgs(cfg *RestoreConfig) []string {
	prefix := noLockFlags(cfgNoLockLens.Get(cfg))
	core := []string{
		argRepository,
		resticRepository(cfg),
		argRestore,
		cfgSnapshotLens.Get(cfg),
		argTarget,
		scratchMountPath,
	}
	return A.ArrayConcatAll(prefix, core)
}

// buildRedisRestoreScript starts a local temporary redis-server on 127.0.0.1:6380
// loaded from the restored RDB file (/restore/redis-backup.rdb), migrates all
// keys across logical databases 0..15 into the target Redis service, and triggers
// BGSAVE and BGREWRITEAOF on the target so both RDB and AOF persistence stay current.
func buildRedisRestoreScript() string {
	return `set -eu
test -s /restore/redis-backup.rdb
cp /restore/redis-backup.rdb /tmp/dump.rdb
redis-server --dir /tmp --dbfilename dump.rdb \
  --appendonly no --save "" --port 6380 --bind 127.0.0.1 --daemonize yes
i=0
until redis-cli -h 127.0.0.1 -p 6380 ping 2>/dev/null | grep -q PONG; do
  i=$((i + 1))
  [ "$i" -le 30 ] || { echo "local redis-server failed to start" >&2; exit 1; }
  sleep 1
done
redis-cli -h "$REDIS_TARGET_HOST" -p "$REDIS_TARGET_PORT" ping | grep -q PONG
if [ "$REDIS_OVERWRITE" = "true" ]; then
  redis-cli -h "$REDIS_TARGET_HOST" -p "$REDIS_TARGET_PORT" FLUSHALL
fi
for db in 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  count=$(redis-cli -h 127.0.0.1 -p 6380 -n "$db" DBSIZE | tr -d '\r')
  [ "${count:-0}" -gt 0 ] || continue
  redis-cli -h 127.0.0.1 -p 6380 -n "$db" --scan | while IFS= read -r key; do
    [ -n "$key" ] || continue
    if [ "$REDIS_OVERWRITE" = "true" ]; then
      redis-cli -h 127.0.0.1 -p 6380 -n "$db" \
        MIGRATE "$REDIS_TARGET_HOST" "$REDIS_TARGET_PORT" \
        "$key" "$db" 60000 COPY REPLACE >/dev/null
    else
      redis-cli -h 127.0.0.1 -p 6380 -n "$db" \
        MIGRATE "$REDIS_TARGET_HOST" "$REDIS_TARGET_PORT" \
        "$key" "$db" 60000 COPY >/dev/null
    fi
  done
done
redis-cli -h "$REDIS_TARGET_HOST" -p "$REDIS_TARGET_PORT" BGSAVE || true
redis-cli -h "$REDIS_TARGET_HOST" -p "$REDIS_TARGET_PORT" BGREWRITEAOF || true
redis-cli -h 127.0.0.1 -p 6380 SHUTDOWN NOSAVE || true`
}

func buildRedisRestoreArgs() []string {
	return []string{"-c", buildRedisRestoreScript()}
}

func resticImageRef(cfg *RestoreConfig) string {
	img := cfgResticImageLens.Get(cfg)
	return fmt.Sprintf("%s:%s", imageNameLens.Get(img), imageTagLens.Get(img))
}

func redisImageRef(cfg *RestoreConfig) string {
	img := cfgRedisImageLens.Get(cfg)
	return fmt.Sprintf("%s:%s", imageNameLens.Get(img), imageTagLens.Get(img))
}

func portString(cfg *RestoreConfig) string {
	return strconv.Itoa(cfgPortLens.Get(cfg))
}
