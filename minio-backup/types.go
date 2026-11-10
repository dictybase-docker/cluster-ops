package main

import (
	"fmt"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	L "github.com/IBM/fp-go/v2/optics/lens"
	O "github.com/IBM/fp-go/v2/option"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
)

const (
	defaultServer         = "minio"
	defaultPort           = 9000
	defaultSchedule       = "30 1 * * *"
	defaultResticImage    = "restic/restic"
	defaultResticImageTag = "0.17.0"
	defaultMCImage        = "minio/mc"
	defaultMCImageTag     = "RELEASE.2025-08-13T08-35-41Z"
	defaultStorageName    = "minio-backup-ephemeral"

	scratchMountPath  = "/backup"
	initContainerName = "mc-dump"
	mainContainerName = "restic-backup"
	cronJobName       = "minio-backup-cronjob"
	immediateJobName  = "minio-immediate-backup-job"
)

var (
	secretKeyPairNameLens = L.MakeLens(
		func(s SecretKeyPair) string { return s.Name },
		func(s SecretKeyPair, v string) SecretKeyPair { s.Name = v; return s },
	)
	secretKeyPairKeyLens = L.MakeLens(
		func(s SecretKeyPair) string { return s.Key },
		func(s SecretKeyPair, v string) SecretKeyPair { s.Key = v; return s },
	)

	minioSecretNameLens = L.MakeLens(
		func(s MinioSecretPair) string { return s.Name },
		func(s MinioSecretPair, v string) MinioSecretPair { s.Name = v; return s },
	)
	minioSecretUserKeyLens = L.MakeLens(
		func(s MinioSecretPair) string { return s.UserKey },
		func(s MinioSecretPair, v string) MinioSecretPair { s.UserKey = v; return s },
	)
	minioSecretPassKeyLens = L.MakeLens(
		func(s MinioSecretPair) string { return s.PassKey },
		func(s MinioSecretPair, v string) MinioSecretPair { s.PassKey = v; return s },
	)

	storageClassLens = L.MakeLens(
		func(s StorageConfig) string { return s.Class },
		func(s StorageConfig, v string) StorageConfig { s.Class = v; return s },
	)
	storageSizeLens = L.MakeLens(
		func(s StorageConfig) string { return s.Size },
		func(s StorageConfig, v string) StorageConfig { s.Size = v; return s },
	)
	storageNameLens = L.MakeLens(
		func(s StorageConfig) string { return s.Name },
		func(s StorageConfig, v string) StorageConfig { s.Name = v; return s },
	)

	imageNameLens = L.MakeLens(
		func(i ImageConfig) string { return i.Name },
		func(i ImageConfig, v string) ImageConfig { i.Name = v; return i },
	)
	imageTagLens = L.MakeLens(
		func(i ImageConfig) string { return i.Tag },
		func(i ImageConfig, v string) ImageConfig { i.Tag = v; return i },
	)

	cfgBucketLens = L.MakeLensRef(
		func(c *MinioBackupConfig) string { return c.Bucket },
		func(c *MinioBackupConfig, v string) *MinioBackupConfig { c.Bucket = v; return c },
	)
	cfgNamespaceLens = L.MakeLensRef(
		func(c *MinioBackupConfig) string { return c.Namespace },
		func(c *MinioBackupConfig, v string) *MinioBackupConfig { c.Namespace = v; return c },
	)
	cfgServerLens = L.MakeLensRef(
		func(c *MinioBackupConfig) string { return c.Server },
		func(c *MinioBackupConfig, v string) *MinioBackupConfig { c.Server = v; return c },
	)
	cfgPortLens = L.MakeLensRef(
		func(c *MinioBackupConfig) int { return c.Port },
		func(c *MinioBackupConfig, v int) *MinioBackupConfig { c.Port = v; return c },
	)
	cfgScheduleLens = L.MakeLensRef(
		func(c *MinioBackupConfig) string { return c.Schedule },
		func(c *MinioBackupConfig, v string) *MinioBackupConfig { c.Schedule = v; return c },
	)
	cfgStorageLens = L.MakeLensRef(
		func(c *MinioBackupConfig) StorageConfig { return c.Storage },
		func(c *MinioBackupConfig, v StorageConfig) *MinioBackupConfig { c.Storage = v; return c },
	)
	cfgMinioSecretLens = L.MakeLensRef(
		func(c *MinioBackupConfig) MinioSecretPair { return c.MinioSecret },
		func(c *MinioBackupConfig, v MinioSecretPair) *MinioBackupConfig { c.MinioSecret = v; return c },
	)
	cfgResticSecretLens = L.MakeLensRef(
		func(c *MinioBackupConfig) SecretKeyPair { return c.ResticSecret },
		func(c *MinioBackupConfig, v SecretKeyPair) *MinioBackupConfig { c.ResticSecret = v; return c },
	)
	cfgBucketSecretLens = L.MakeLensRef(
		func(c *MinioBackupConfig) SecretKeyPair { return c.BucketSecret },
		func(c *MinioBackupConfig, v SecretKeyPair) *MinioBackupConfig { c.BucketSecret = v; return c },
	)
	cfgProjectSecretLens = L.MakeLensRef(
		func(c *MinioBackupConfig) SecretKeyPair { return c.ProjectSecret },
		func(c *MinioBackupConfig, v SecretKeyPair) *MinioBackupConfig { c.ProjectSecret = v; return c },
	)
	cfgResticImageLens = L.MakeLensRef(
		func(c *MinioBackupConfig) ImageConfig { return c.ResticImage },
		func(c *MinioBackupConfig, v ImageConfig) *MinioBackupConfig { c.ResticImage = v; return c },
	)
	cfgMCImageLens = L.MakeLensRef(
		func(c *MinioBackupConfig) ImageConfig { return c.MCImage },
		func(c *MinioBackupConfig, v ImageConfig) *MinioBackupConfig { c.MCImage = v; return c },
	)
)

// SecretKeyPair names a Kubernetes Secret and the data key inside it.
type SecretKeyPair struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// MinioSecretPair names the MinIO root credentials Secret and its user/password keys.
type MinioSecretPair struct {
	Name    string `json:"name"`
	UserKey string `json:"userKey"`
	PassKey string `json:"passKey"`
}

// StorageConfig describes the ephemeral scratch volume for the MinIO bucket dump.
type StorageConfig struct {
	Class string `json:"class"`
	Size  string `json:"size"`
	Name  string `json:"name,omitempty"`
}

// ImageConfig names a container image and tag.
type ImageConfig struct {
	Name string `json:"name,omitempty"`
	Tag  string `json:"tag,omitempty"`
}

// MinioBackupConfig holds the configuration for the MinIO GCS backup stack.
type MinioBackupConfig struct {
	Bucket        string          `json:"bucket"`
	Namespace     string          `json:"namespace"`
	Server        string          `json:"server,omitempty"`
	Port          int             `json:"port,omitempty"`
	Schedule      string          `json:"schedule,omitempty"`
	Storage       StorageConfig   `json:"storage"`
	MinioSecret   MinioSecretPair `json:"minioSecret"`
	ResticSecret  SecretKeyPair   `json:"resticSecret"`
	BucketSecret  SecretKeyPair   `json:"bucketSecret"`
	ProjectSecret SecretKeyPair   `json:"projectSecret"`
	ResticImage   ImageConfig     `json:"resticImage"`
	MCImage       ImageConfig     `json:"mcImage"`
}

func defaultStr(fallback string) func(string) string {
	return F.Flow2(
		O.FromPredicate(S.IsNonEmpty),
		O.GetOrElse(F.Constant(fallback)),
	)
}

func defaultInt(fallback int) func(int) int {
	return F.Flow2(
		O.FromPredicate(P.IsNonZero[int]()),
		O.GetOrElse(F.Constant(fallback)),
	)
}

func defaultStorage(s StorageConfig) StorageConfig {
	name := F.Pipe1(storageNameLens.Get(s), defaultStr(defaultStorageName))
	return F.Pipe1(s, storageNameLens.Set(name))
}

func defaultResticImg(img ImageConfig) ImageConfig {
	name := F.Pipe1(imageNameLens.Get(img), defaultStr(defaultResticImage))
	tag := F.Pipe1(imageTagLens.Get(img), defaultStr(defaultResticImageTag))
	return F.Pipe2(img, imageNameLens.Set(name), imageTagLens.Set(tag))
}

func defaultMCImg(img ImageConfig) ImageConfig {
	name := F.Pipe1(imageNameLens.Get(img), defaultStr(defaultMCImage))
	tag := F.Pipe1(imageTagLens.Get(img), defaultStr(defaultMCImageTag))
	return F.Pipe2(img, imageNameLens.Set(name), imageTagLens.Set(tag))
}

func (cfg *MinioBackupConfig) applyDefaults() {
	cfg.Server = F.Pipe1(cfgServerLens.Get(cfg), defaultStr(defaultServer))
	cfg.Port = F.Pipe1(cfgPortLens.Get(cfg), defaultInt(defaultPort))
	cfg.Schedule = F.Pipe1(cfgScheduleLens.Get(cfg), defaultStr(defaultSchedule))
	cfg.Storage = F.Pipe1(cfgStorageLens.Get(cfg), defaultStorage)
	cfg.ResticImage = F.Pipe1(cfgResticImageLens.Get(cfg), defaultResticImg)
	cfg.MCImage = F.Pipe1(cfgMCImageLens.Get(cfg), defaultMCImg)
}

func isValidSecretPair(pair SecretKeyPair) bool {
	return S.IsNonEmpty(secretKeyPairNameLens.Get(pair)) &&
		S.IsNonEmpty(secretKeyPairKeyLens.Get(pair))
}

func isValidMinioSecret(pair MinioSecretPair) bool {
	return S.IsNonEmpty(minioSecretNameLens.Get(pair)) &&
		S.IsNonEmpty(minioSecretUserKeyLens.Get(pair)) &&
		S.IsNonEmpty(minioSecretPassKeyLens.Get(pair))
}

func hasBucket(cfg *MinioBackupConfig) bool {
	return S.IsNonEmpty(cfgBucketLens.Get(cfg))
}

func hasNamespace(cfg *MinioBackupConfig) bool {
	return S.IsNonEmpty(cfgNamespaceLens.Get(cfg))
}

func hasStorageClass(cfg *MinioBackupConfig) bool {
	return S.IsNonEmpty(storageClassLens.Get(cfgStorageLens.Get(cfg)))
}

func hasStorageSize(cfg *MinioBackupConfig) bool {
	return S.IsNonEmpty(storageSizeLens.Get(cfgStorageLens.Get(cfg)))
}

func hasMinioSecret(cfg *MinioBackupConfig) bool {
	return isValidMinioSecret(cfgMinioSecretLens.Get(cfg))
}

func hasResticSecret(cfg *MinioBackupConfig) bool {
	return isValidSecretPair(cfgResticSecretLens.Get(cfg))
}

func hasBucketSecret(cfg *MinioBackupConfig) bool {
	return isValidSecretPair(cfgBucketSecretLens.Get(cfg))
}

func hasProjectSecret(cfg *MinioBackupConfig) bool {
	return isValidSecretPair(cfgProjectSecretLens.Get(cfg))
}

func errBucketEmpty(*MinioBackupConfig) error {
	return fmt.Errorf("bucket cannot be empty")
}

func errNamespaceEmpty(*MinioBackupConfig) error {
	return fmt.Errorf("namespace cannot be empty")
}

func errStorageClassEmpty(*MinioBackupConfig) error {
	return fmt.Errorf("storage.class cannot be empty")
}

func errStorageSizeEmpty(*MinioBackupConfig) error {
	return fmt.Errorf("storage.size cannot be empty")
}

func errMinioSecretInvalid(*MinioBackupConfig) error {
	return fmt.Errorf("minioSecret name, userKey, and passKey are required")
}

func errResticSecretInvalid(*MinioBackupConfig) error {
	return fmt.Errorf("resticSecret name and key are required")
}

func errBucketSecretInvalid(*MinioBackupConfig) error {
	return fmt.Errorf("bucketSecret name and key are required")
}

func errProjectSecretInvalid(*MinioBackupConfig) error {
	return fmt.Errorf("projectSecret name and key are required")
}

// Validate checks that the configuration is complete.
func (cfg *MinioBackupConfig) Validate() error {
	return F.Pipe9(
		E.Of[error](cfg),
		E.Chain(E.FromPredicate(hasBucket, errBucketEmpty)),
		E.Chain(E.FromPredicate(hasNamespace, errNamespaceEmpty)),
		E.Chain(E.FromPredicate(hasStorageClass, errStorageClassEmpty)),
		E.Chain(E.FromPredicate(hasStorageSize, errStorageSizeEmpty)),
		E.Chain(E.FromPredicate(hasMinioSecret, errMinioSecretInvalid)),
		E.Chain(E.FromPredicate(hasResticSecret, errResticSecretInvalid)),
		E.Chain(E.FromPredicate(hasBucketSecret, errBucketSecretInvalid)),
		E.Chain(E.FromPredicate(hasProjectSecret, errProjectSecretInvalid)),
		E.ToError[*MinioBackupConfig],
	)
}
