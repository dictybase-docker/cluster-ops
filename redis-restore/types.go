package main

import (
	"fmt"
	"regexp"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	L "github.com/IBM/fp-go/v2/optics/lens"
	O "github.com/IBM/fp-go/v2/option"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
)

const (
	defaultServer         = "redis"
	defaultPort           = 6379
	defaultSnapshot       = "latest"
	defaultResticImage    = "restic/restic"
	defaultResticImageTag = "0.17.0"
	defaultRedisImage     = "redis"
	defaultRedisImageTag  = "8.4.6"
	defaultStorageName    = "redis-restore-scratch"

	scratchMountPath  = "/restore"
	dumpFilename      = "redis-backup.rdb"
	initContainerName = "restic-restore"
	mainContainerName = "redis-restore"
	jobNamePrefix     = "redis-restore"

	maxRestoreIDLen = 63 - len(jobNamePrefix) - 1
)

var (
	restoreIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

	secretKeyPairNameLens = L.MakeLens(
		func(s SecretKeyPair) string { return s.Name },
		func(s SecretKeyPair, v string) SecretKeyPair { s.Name = v; return s },
	)
	secretKeyPairKeyLens = L.MakeLens(
		func(s SecretKeyPair) string { return s.Key },
		func(s SecretKeyPair, v string) SecretKeyPair { s.Key = v; return s },
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

	cfgNamespaceLens = L.MakeLensRef(
		func(c *RestoreConfig) string { return c.Namespace },
		func(c *RestoreConfig, v string) *RestoreConfig { c.Namespace = v; return c },
	)
	cfgServerLens = L.MakeLensRef(
		func(c *RestoreConfig) string { return c.Server },
		func(c *RestoreConfig, v string) *RestoreConfig { c.Server = v; return c },
	)
	cfgPortLens = L.MakeLensRef(
		func(c *RestoreConfig) int { return c.Port },
		func(c *RestoreConfig, v int) *RestoreConfig { c.Port = v; return c },
	)
	cfgBucketLens = L.MakeLensRef(
		func(c *RestoreConfig) string { return c.Bucket },
		func(c *RestoreConfig, v string) *RestoreConfig { c.Bucket = v; return c },
	)
	cfgSnapshotLens = L.MakeLensRef(
		func(c *RestoreConfig) string { return c.Snapshot },
		func(c *RestoreConfig, v string) *RestoreConfig { c.Snapshot = v; return c },
	)
	cfgNoLockLens = L.MakeLensRef(
		func(c *RestoreConfig) bool { return c.NoLock },
		func(c *RestoreConfig, v bool) *RestoreConfig { c.NoLock = v; return c },
	)
	cfgOverwriteLens = L.MakeLensRef(
		func(c *RestoreConfig) bool { return c.Overwrite },
		func(c *RestoreConfig, v bool) *RestoreConfig { c.Overwrite = v; return c },
	)
	cfgRestoreIDLens = L.MakeLensRef(
		func(c *RestoreConfig) string { return c.RestoreID },
		func(c *RestoreConfig, v string) *RestoreConfig { c.RestoreID = v; return c },
	)
	cfgConfirmTargetLens = L.MakeLensRef(
		func(c *RestoreConfig) string { return c.ConfirmTarget },
		func(c *RestoreConfig, v string) *RestoreConfig { c.ConfirmTarget = v; return c },
	)
	cfgStorageLens = L.MakeLensRef(
		func(c *RestoreConfig) StorageConfig { return c.Storage },
		func(c *RestoreConfig, v StorageConfig) *RestoreConfig { c.Storage = v; return c },
	)
	cfgResticSecretLens = L.MakeLensRef(
		func(c *RestoreConfig) SecretKeyPair { return c.ResticSecret },
		func(c *RestoreConfig, v SecretKeyPair) *RestoreConfig { c.ResticSecret = v; return c },
	)
	cfgBucketSecretLens = L.MakeLensRef(
		func(c *RestoreConfig) SecretKeyPair { return c.BucketSecret },
		func(c *RestoreConfig, v SecretKeyPair) *RestoreConfig { c.BucketSecret = v; return c },
	)
	cfgProjectSecretLens = L.MakeLensRef(
		func(c *RestoreConfig) SecretKeyPair { return c.ProjectSecret },
		func(c *RestoreConfig, v SecretKeyPair) *RestoreConfig { c.ProjectSecret = v; return c },
	)
	cfgResticImageLens = L.MakeLensRef(
		func(c *RestoreConfig) ImageConfig { return c.ResticImage },
		func(c *RestoreConfig, v ImageConfig) *RestoreConfig { c.ResticImage = v; return c },
	)
	cfgRedisImageLens = L.MakeLensRef(
		func(c *RestoreConfig) ImageConfig { return c.RedisImage },
		func(c *RestoreConfig, v ImageConfig) *RestoreConfig { c.RedisImage = v; return c },
	)
)

// SecretKeyPair names a Kubernetes Secret and the data key inside it.
type SecretKeyPair struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// StorageConfig describes the ephemeral scratch volume for the RDB dump.
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

// RestoreConfig holds the complete configuration for one Redis restore run.
type RestoreConfig struct {
	Namespace     string        `json:"namespace"`
	Server        string        `json:"server,omitempty"`
	Port          int           `json:"port,omitempty"`
	Bucket        string        `json:"bucket"`
	Snapshot      string        `json:"snapshot,omitempty"`
	NoLock        bool          `json:"noLock,omitempty"`
	Overwrite     bool          `json:"overwrite,omitempty"`
	RestoreID     string        `json:"restoreId"`
	ConfirmTarget string        `json:"confirmTarget"`
	Storage       StorageConfig `json:"storage"`
	ResticSecret  SecretKeyPair `json:"resticSecret"`
	BucketSecret  SecretKeyPair `json:"bucketSecret"`
	ProjectSecret SecretKeyPair `json:"projectSecret"`
	ResticImage   ImageConfig   `json:"resticImage"`
	RedisImage    ImageConfig   `json:"redisImage"`
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

func defaultRedisImg(img ImageConfig) ImageConfig {
	name := F.Pipe1(imageNameLens.Get(img), defaultStr(defaultRedisImage))
	tag := F.Pipe1(imageTagLens.Get(img), defaultStr(defaultRedisImageTag))
	return F.Pipe2(img, imageNameLens.Set(name), imageTagLens.Set(tag))
}

func (cfg *RestoreConfig) applyDefaults() {
	cfg.Server = F.Pipe1(cfgServerLens.Get(cfg), defaultStr(defaultServer))
	cfg.Port = F.Pipe1(cfgPortLens.Get(cfg), defaultInt(defaultPort))
	cfg.Snapshot = F.Pipe1(cfgSnapshotLens.Get(cfg), defaultStr(defaultSnapshot))
	cfg.Storage = F.Pipe1(cfgStorageLens.Get(cfg), defaultStorage)
	cfg.ResticImage = F.Pipe1(cfgResticImageLens.Get(cfg), defaultResticImg)
	cfg.RedisImage = F.Pipe1(cfgRedisImageLens.Get(cfg), defaultRedisImg)
}

func (cfg *RestoreConfig) targetIdentity() string {
	return fmt.Sprintf(
		"%s/%s/%s",
		cfgNamespaceLens.Get(cfg),
		cfgServerLens.Get(cfg),
		cfgRestoreIDLens.Get(cfg),
	)
}

func (cfg *RestoreConfig) jobName() string {
	return fmt.Sprintf("%s-%s", jobNamePrefix, cfgRestoreIDLens.Get(cfg))
}

func isValidSecretPair(pair SecretKeyPair) bool {
	return S.IsNonEmpty(secretKeyPairNameLens.Get(pair)) &&
		S.IsNonEmpty(secretKeyPairKeyLens.Get(pair))
}

func hasNamespace(cfg *RestoreConfig) bool {
	return S.IsNonEmpty(cfgNamespaceLens.Get(cfg))
}

func hasBucket(cfg *RestoreConfig) bool {
	return S.IsNonEmpty(cfgBucketLens.Get(cfg))
}

func hasRestoreID(cfg *RestoreConfig) bool {
	return S.IsNonEmpty(cfgRestoreIDLens.Get(cfg))
}

func restoreIDWithinLimit(cfg *RestoreConfig) bool {
	return len(cfgRestoreIDLens.Get(cfg)) <= maxRestoreIDLen
}

func restoreIDMatchesDNS(cfg *RestoreConfig) bool {
	return restoreIDPattern.MatchString(cfgRestoreIDLens.Get(cfg))
}

func hasStorageClass(cfg *RestoreConfig) bool {
	return S.IsNonEmpty(storageClassLens.Get(cfgStorageLens.Get(cfg)))
}

func hasStorageSize(cfg *RestoreConfig) bool {
	return S.IsNonEmpty(storageSizeLens.Get(cfgStorageLens.Get(cfg)))
}

func hasResticSecret(cfg *RestoreConfig) bool {
	return isValidSecretPair(cfgResticSecretLens.Get(cfg))
}

func hasBucketSecret(cfg *RestoreConfig) bool {
	return isValidSecretPair(cfgBucketSecretLens.Get(cfg))
}

func hasProjectSecret(cfg *RestoreConfig) bool {
	return isValidSecretPair(cfgProjectSecretLens.Get(cfg))
}

func confirmTargetMatches(cfg *RestoreConfig) bool {
	return cfgConfirmTargetLens.Get(cfg) == cfg.targetIdentity()
}

func errNamespaceEmpty(*RestoreConfig) error {
	return fmt.Errorf("namespace cannot be empty")
}

func errBucketEmpty(*RestoreConfig) error {
	return fmt.Errorf("bucket cannot be empty")
}

func errRestoreIDEmpty(*RestoreConfig) error {
	return fmt.Errorf("restoreId cannot be empty")
}

func errRestoreIDTooLong(cfg *RestoreConfig) error {
	id := cfgRestoreIDLens.Get(cfg)
	return fmt.Errorf(
		"restoreId %q is %d chars, must be at most %d so the Job "+
			"name %q stays within the 63-char Kubernetes limit",
		id,
		len(id),
		maxRestoreIDLen,
		cfg.jobName(),
	)
}

func errRestoreIDInvalid(cfg *RestoreConfig) error {
	return fmt.Errorf(
		"restoreId %q must be a valid DNS-1123 label: lowercase "+
			"alphanumeric and hyphens, not starting or ending with a hyphen",
		cfgRestoreIDLens.Get(cfg),
	)
}

func errStorageClassEmpty(*RestoreConfig) error {
	return fmt.Errorf("storage.class cannot be empty")
}

func errStorageSizeEmpty(*RestoreConfig) error {
	return fmt.Errorf("storage.size cannot be empty")
}

func errResticSecretInvalid(*RestoreConfig) error {
	return fmt.Errorf("resticSecret name and key are required")
}

func errBucketSecretInvalid(*RestoreConfig) error {
	return fmt.Errorf("bucketSecret name and key are required")
}

func errProjectSecretInvalid(*RestoreConfig) error {
	return fmt.Errorf("projectSecret name and key are required")
}

func errConfirmTargetMismatch(cfg *RestoreConfig) error {
	return fmt.Errorf(
		"confirmTarget must exactly equal %q (namespace/server/restoreId) "+
			"to prevent an accidental restore into the wrong target; got %q",
		cfg.targetIdentity(),
		cfgConfirmTargetLens.Get(cfg),
	)
}

// Validate checks that the configuration is complete and internally consistent.
func (cfg *RestoreConfig) Validate() error {
	return F.Pipe12(
		E.Of[error](cfg),
		E.Chain(E.FromPredicate(hasNamespace, errNamespaceEmpty)),
		E.Chain(E.FromPredicate(hasBucket, errBucketEmpty)),
		E.Chain(E.FromPredicate(hasRestoreID, errRestoreIDEmpty)),
		E.Chain(E.FromPredicate(restoreIDWithinLimit, errRestoreIDTooLong)),
		E.Chain(E.FromPredicate(restoreIDMatchesDNS, errRestoreIDInvalid)),
		E.Chain(E.FromPredicate(hasStorageClass, errStorageClassEmpty)),
		E.Chain(E.FromPredicate(hasStorageSize, errStorageSizeEmpty)),
		E.Chain(E.FromPredicate(hasResticSecret, errResticSecretInvalid)),
		E.Chain(E.FromPredicate(hasBucketSecret, errBucketSecretInvalid)),
		E.Chain(E.FromPredicate(hasProjectSecret, errProjectSecretInvalid)),
		E.Chain(E.FromPredicate(confirmTargetMatches, errConfirmTargetMismatch)),
		E.ToError[*RestoreConfig],
	)
}
