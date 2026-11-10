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

type minioBackupMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *minioBackupMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *minioBackupMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func newSampleMinioBackupConfig() *MinioBackupConfig {
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
	cfg := F.Pipe7(
		&MinioBackupConfig{},
		cfgBucketLens.Set("restic-minio-backup-dcr-kube1"),
		cfgNamespaceLens.Set("prod"),
		cfgStorageLens.Set(storage),
		cfgMinioSecretLens.Set(minioSec),
		cfgResticSecretLens.Set(resticSec),
		cfgBucketSecretLens.Set(bucketSec),
		cfgProjectSecretLens.Set(projectSec),
	)
	cfg.applyDefaults()
	return cfg
}

func TestMinioBackupDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	cfg := newSampleMinioBackupConfig()
	require.NoError(t, cfg.Validate())
	require.Equal(t, "minio", cfgServerLens.Get(cfg))
	require.Equal(t, 9000, cfgPortLens.Get(cfg))
	require.Equal(t, "30 1 * * *", cfgScheduleLens.Get(cfg))
	require.Equal(t, "gs:restic-minio-backup-dcr-kube1:/", resticRepository(cfg))
}

func TestMinioBackupInstallCreatesBucketCronJobAndImmediateJob(t *testing.T) {
	t.Parallel()
	mocks := &minioBackupMocks{}
	mocks.resources = make(map[string]pulumi.MockResourceArgs)
	cfg := newSampleMinioBackupConfig()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewMinioBackup(cfg).Install(ctx)
	}, pulumi.WithMocks("minio-backup", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	_, hasBucket := mocks.resources["restic-minio-backup-dcr-kube1"]
	require.True(t, hasBucket)

	cron, hasCron := mocks.resources["minio-backup-cronjob"]
	require.True(t, hasCron)
	require.Contains(t, fmt.Sprint(cron.Inputs), "30 1 * * *")

	job, hasJob := mocks.resources["minio-immediate-backup-job"]
	require.True(t, hasJob)
	require.Contains(t, fmt.Sprint(job.Inputs), "mc mirror")
	require.Contains(t, fmt.Sprint(job.Inputs), "restic/restic:0.17.0")
}
