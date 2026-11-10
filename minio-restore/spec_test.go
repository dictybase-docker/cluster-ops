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

type minioRestoreMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *minioRestoreMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *minioRestoreMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func newSampleMinioRestoreConfig() *RestoreConfig {
	storage := F.Pipe2(
		StorageConfig{},
		storageClassLens.Set("dictycr-balanced"),
		storageSizeLens.Set("150Gi"),
	)
	minioSec := F.Pipe3(
		MinioSecretPair{},
		minioSecretNameLens.Set("minio-root"),
		minioSecretUserKeyLens.Set("rootUser"),
		minioSecretPassKeyLens.Set("rootPassword"),
	)
	resticSec := F.Pipe2(
		SecretKeyPair{},
		secretKeyPairNameLens.Set("minio-backup-auth"),
		secretKeyPairKeyLens.Set("resticPass"),
	)
	bucketSec := F.Pipe2(
		SecretKeyPair{},
		secretKeyPairNameLens.Set("minio-backup-auth"),
		secretKeyPairKeyLens.Set("gcsCredentials"),
	)
	projectSec := F.Pipe2(
		SecretKeyPair{},
		secretKeyPairNameLens.Set("minio-backup-auth"),
		secretKeyPairKeyLens.Set("gcsProject"),
	)
	cfg := F.Pipe9(
		&RestoreConfig{},
		cfgNamespaceLens.Set("prod"),
		cfgBucketLens.Set("restic-minio-backup-dcr-kube1"),
		cfgRestoreIDLens.Set("drill-2026-01"),
		cfgConfirmTargetLens.Set("prod/minio/drill-2026-01"),
		cfgStorageLens.Set(storage),
		cfgMinioSecretLens.Set(minioSec),
		cfgResticSecretLens.Set(resticSec),
		cfgBucketSecretLens.Set(bucketSec),
		cfgProjectSecretLens.Set(projectSec),
	)
	cfg.applyDefaults()
	return cfg
}

func TestMinioRestoreDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	cfg := newSampleMinioRestoreConfig()
	require.NoError(t, cfg.Validate())
	require.Equal(t, "minio", cfgServerLens.Get(cfg))
	require.Equal(t, 9000, cfgPortLens.Get(cfg))
	require.Equal(t, "latest", cfgSnapshotLens.Get(cfg))
	require.Equal(t, "minio-restore-drill-2026-01", cfg.jobName())
}

func TestMinioRestoreBuildResticArgsWithSingleBucketAndNoLock(t *testing.T) {
	t.Parallel()
	cfg := F.Pipe2(
		newSampleMinioRestoreConfig(),
		cfgNoLockLens.Set(true),
		cfgS3BucketLens.Set("editor"),
	)
	require.Equal(t, []string{
		"--no-lock",
		"-r", "gs:restic-minio-backup-dcr-kube1:/",
		"restore", "latest",
		"--target", "/restore",
		"--include", "/backup/buckets/editor",
		"--include", "/backup/policies/editor.json",
	}, buildResticRestoreArgs(cfg))
}

func TestMinioRestoreInstallCreatesJob(t *testing.T) {
	t.Parallel()
	mocks := &minioRestoreMocks{}
	mocks.resources = make(map[string]pulumi.MockResourceArgs)
	cfg := F.Pipe1(newSampleMinioRestoreConfig(), cfgOverwriteLens.Set(true))

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewMinioRestore(cfg).Install(ctx)
	}, pulumi.WithMocks("minio-restore", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	job, ok := mocks.resources["minio-restore-drill-2026-01"]
	require.True(t, ok)
	require.Equal(t, "kubernetes:batch/v1:Job", job.TypeToken)
	require.Contains(t, fmt.Sprint(job.Inputs), "restic/restic:0.17.0")
	require.Contains(t, fmt.Sprint(job.Inputs), "minio/mc:RELEASE.2025-08-13T08-35-41Z")
}
