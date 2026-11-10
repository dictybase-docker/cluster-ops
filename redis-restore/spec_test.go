package main

import (
	"fmt"
	"sync"
	"testing"

	F "github.com/IBM/fp-go/v2/function"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type redisRestoreMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *redisRestoreMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *redisRestoreMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func newSampleRedisRestoreConfig() *RestoreConfig {
	storage := F.Pipe2(
		StorageConfig{},
		storageClassLens.Set("dictycr-balanced"),
		storageSizeLens.Set("10Gi"),
	)
	resticSec := F.Pipe2(
		SecretKeyPair{},
		secretKeyPairNameLens.Set("redis-backup-auth"),
		secretKeyPairKeyLens.Set("resticPass"),
	)
	bucketSec := F.Pipe2(
		SecretKeyPair{},
		secretKeyPairNameLens.Set("redis-backup-auth"),
		secretKeyPairKeyLens.Set("gcsCredentials"),
	)
	projectSec := F.Pipe2(
		SecretKeyPair{},
		secretKeyPairNameLens.Set("redis-backup-auth"),
		secretKeyPairKeyLens.Set("gcsProject"),
	)
	cfg := F.Pipe8(
		&RestoreConfig{},
		cfgNamespaceLens.Set("prod"),
		cfgBucketLens.Set("restic-redis-backup-dcr-kube1"),
		cfgRestoreIDLens.Set("drill-2026-01"),
		cfgConfirmTargetLens.Set("prod/redis/drill-2026-01"),
		cfgStorageLens.Set(storage),
		cfgResticSecretLens.Set(resticSec),
		cfgBucketSecretLens.Set(bucketSec),
		cfgProjectSecretLens.Set(projectSec),
	)
	cfg.applyDefaults()
	return cfg
}

func TestRedisRestoreDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	cfg := newSampleRedisRestoreConfig()
	require.NoError(t, cfg.Validate())
	require.Equal(t, "redis", cfgServerLens.Get(cfg))
	require.Equal(t, 6379, cfgPortLens.Get(cfg))
	require.Equal(t, "latest", cfgSnapshotLens.Get(cfg))
	require.Equal(t, "redis-restore-drill-2026-01", cfg.jobName())
	require.Equal(t, "/restore/redis-backup.rdb", scratchDumpPath())
}

func TestRedisRestoreValidationFailures(t *testing.T) {
	t.Parallel()
	cfg := newSampleRedisRestoreConfig()
	cfg = F.Pipe1(cfg, cfgConfirmTargetLens.Set("prod/redis/wrong"))
	require.ErrorContains(t, cfg.Validate(), "confirmTarget must exactly equal")

	cfgNoBucket := newSampleRedisRestoreConfig()
	cfgNoBucket = F.Pipe1(cfgNoBucket, cfgBucketLens.Set(""))
	require.ErrorContains(t, cfgNoBucket.Validate(), "bucket cannot be empty")
}

func TestBuildResticRestoreArgsWithAndWithoutNoLock(t *testing.T) {
	t.Parallel()
	cfg := newSampleRedisRestoreConfig()
	require.Equal(t, []string{
		"-r", "gs:restic-redis-backup-dcr-kube1:/",
		"restore", "latest",
		"--target", "/restore",
	}, buildResticRestoreArgs(cfg))

	cfgNoLock := F.Pipe2(
		newSampleRedisRestoreConfig(),
		cfgNoLockLens.Set(true),
		cfgSnapshotLens.Set("a1b2c3d4"),
	)
	require.Equal(t, []string{
		"--no-lock",
		"-r", "gs:restic-redis-backup-dcr-kube1:/",
		"restore", "a1b2c3d4",
		"--target", "/restore",
	}, buildResticRestoreArgs(cfgNoLock))
}

func TestRedisRestoreInstallCreatesJob(t *testing.T) {
	t.Parallel()
	mocks := &redisRestoreMocks{}
	mocks.resources = make(map[string]pulumi.MockResourceArgs)
	cfg := F.Pipe1(newSampleRedisRestoreConfig(), cfgOverwriteLens.Set(true))

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewRedisRestore(cfg).Install(ctx)
	}, pulumi.WithMocks("redis-restore", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	job, ok := mocks.resources["redis-restore-drill-2026-01"]
	require.True(t, ok)
	require.Equal(t, "kubernetes:batch/v1:Job", job.TypeToken)
	require.Contains(t, fmt.Sprint(job.Inputs), "restic/restic:0.17.0")
	require.Contains(t, fmt.Sprint(job.Inputs), "redis:8.4.6")
	require.Contains(t, fmt.Sprint(job.Inputs), "MIGRATE")
}
