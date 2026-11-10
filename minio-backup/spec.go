package main

import (
	"fmt"
	"strconv"
)

func resticRepository(cfg *MinioBackupConfig) string {
	return fmt.Sprintf("gs:%s:/", cfgBucketLens.Get(cfg))
}

// buildMCDumpScript mirrors every bucket on the in-cluster MinIO server into
// /backup/buckets/<bucket> and exports each bucket's anonymous S3 policy into
// /backup/policies/<bucket>.json so restic snapshots both objects and policies.
func buildMCDumpScript() string {
	return `set -eu
export MC_CONFIG_DIR=/tmp/mc
mc alias set src "http://${MINIO_HOST}:${MINIO_PORT}" "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}" --quiet
mkdir -p /backup/buckets /backup/policies
for b in $(mc ls src --json | sed -n 's/.*"key":"\([^/"]*\)\/\?".*/\1/p'); do
  [ -n "$b" ] || continue
  mkdir -p "/backup/buckets/$b"
  mc mirror --preserve --overwrite --retry "src/$b" "/backup/buckets/$b"
  mc anonymous get-json "src/$b" > "/backup/policies/${b}.json" || true
done`
}

func buildMCDumpArgs() []string {
	return []string{"-c", buildMCDumpScript()}
}

// buildResticBackupScript initializes the GCS restic repository on first run
// and snapshots /backup (both buckets and anonymous policies).
func buildResticBackupScript() string {
	return `set -eu
restic -r "$RESTIC_REPOSITORY" cat config >/dev/null 2>&1 || restic -r "$RESTIC_REPOSITORY" init
restic -r "$RESTIC_REPOSITORY" backup /backup --tag minio-backup`
}

func buildResticBackupArgs() []string {
	return []string{"-c", buildResticBackupScript()}
}

func resticImageRef(cfg *MinioBackupConfig) string {
	img := cfgResticImageLens.Get(cfg)
	return fmt.Sprintf("%s:%s", imageNameLens.Get(img), imageTagLens.Get(img))
}

func mcImageRef(cfg *MinioBackupConfig) string {
	img := cfgMCImageLens.Get(cfg)
	return fmt.Sprintf("%s:%s", imageNameLens.Get(img), imageTagLens.Get(img))
}

func portString(cfg *MinioBackupConfig) string {
	return strconv.Itoa(cfgPortLens.Get(cfg))
}
