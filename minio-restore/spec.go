package main

import (
	"fmt"
	"strconv"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
)

const (
	argRepository = "-r"
	argNoLock     = "--no-lock"
	argRestore    = "restore"
	argTarget     = "--target"
	argInclude    = "--include"
)

var (
	noLockFlags = F.Pipe1(
		func(noLock bool) bool { return noLock },
		P.Fold(
			F.Constant1[bool, []string](nil),
			F.Constant1[bool]([]string{argNoLock}),
		),
	)

	s3BucketIncludeFlags = F.Pipe1(
		S.IsNonEmpty,
		P.Fold(
			F.Constant1[string, []string](nil),
			buildBucketIncludeSlice,
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

func buildBucketIncludeSlice(bucket string) []string {
	return []string{
		argInclude,
		fmt.Sprintf("/backup/buckets/%s", bucket),
		argInclude,
		fmt.Sprintf("/backup/policies/%s.json", bucket),
	}
}

func resticRepository(cfg *RestoreConfig) string {
	return fmt.Sprintf("gs:%s:/", cfgBucketLens.Get(cfg))
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
	includes := s3BucketIncludeFlags(cfgS3BucketLens.Get(cfg))
	return A.ArrayConcatAll(prefix, core, includes)
}

// buildMCRestoreScript mirrors each restored bucket under /restore/backup/buckets
// into the target MinIO service and applies its saved anonymous policy JSON.
func buildMCRestoreScript() string {
	return `set -eu
export MC_CONFIG_DIR=/tmp/mc
mc alias set dst "http://${MINIO_HOST}:${MINIO_PORT}" "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}" --quiet
if [ -d /restore/backup/buckets ]; then
  for dir in /restore/backup/buckets/*; do
    [ -d "$dir" ] || continue
    b=$(basename "$dir")
    if [ -n "${MINIO_TARGET_BUCKET:-}" ] && [ "$b" != "$MINIO_TARGET_BUCKET" ]; then
      continue
    fi
    mc mb --ignore-existing "dst/$b"
    if [ "${MINIO_OVERWRITE:-false}" = "true" ]; then
      mc mirror --preserve --overwrite --retry "$dir" "dst/$b"
    else
      mc mirror --preserve --retry "$dir" "dst/$b"
    fi
    if [ -s "/restore/backup/policies/${b}.json" ]; then
      mc anonymous set-json "/restore/backup/policies/${b}.json" "dst/$b" || mc anonymous set none "dst/$b"
    fi
  done
fi`
}

func buildMCRestoreArgs() []string {
	return []string{"-c", buildMCRestoreScript()}
}

func resticImageRef(cfg *RestoreConfig) string {
	img := cfgResticImageLens.Get(cfg)
	return fmt.Sprintf("%s:%s", imageNameLens.Get(img), imageTagLens.Get(img))
}

func mcImageRef(cfg *RestoreConfig) string {
	img := cfgMCImageLens.Get(cfg)
	return fmt.Sprintf("%s:%s", imageNameLens.Get(img), imageTagLens.Get(img))
}

func portString(cfg *RestoreConfig) string {
	return strconv.Itoa(cfgPortLens.Get(cfg))
}
