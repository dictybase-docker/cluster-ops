package main

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func ReadConfig(ctx *pulumi.Context) (*MinioBackupConfig, error) {
	conf := config.New(ctx, "")
	backupConfig := &MinioBackupConfig{}
	if err := conf.TryObject("properties", backupConfig); err != nil {
		return nil, fmt.Errorf("failed to read minio-backup config: %w", err)
	}
	backupConfig.applyDefaults()
	if err := backupConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid minio-backup config: %w", err)
	}
	return backupConfig, nil
}

type MinioBackup struct {
	Config *MinioBackupConfig
}

func NewMinioBackup(cfg *MinioBackupConfig) *MinioBackup {
	return &MinioBackup{Config: cfg}
}

func (mb *MinioBackup) Install(ctx *pulumi.Context) error {
	bucket, err := mb.createGCSBucket(ctx)
	if err != nil {
		return err
	}

	if err := mb.createBackupCronJob(ctx, bucket); err != nil {
		return err
	}

	return mb.createImmediateBackupJob(ctx, bucket)
}

func (mb *MinioBackup) createGCSBucket(
	ctx *pulumi.Context,
) (*storage.Bucket, error) {
	bucketName := cfgBucketLens.Get(mb.Config)
	bucket, err := storage.NewBucket(ctx, bucketName, &storage.BucketArgs{
		Name:           pulumi.String(bucketName),
		Location:       pulumi.String("US"),
		LifecycleRules: mb.createLifecycleRules(),
		Versioning: &storage.BucketVersioningArgs{
			Enabled: pulumi.Bool(true),
		},
		SoftDeletePolicy: &storage.BucketSoftDeletePolicyArgs{
			RetentionDurationSeconds: pulumi.Int(5011200), // 58 days
		},
		ForceDestroy: pulumi.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("error creating GCS bucket: %w", err)
	}

	ctx.Export("bucketName", bucket.Name)
	ctx.Export("bucketUrl", bucket.Url)
	return bucket, nil
}

func (mb *MinioBackup) createLifecycleRules() storage.BucketLifecycleRuleArray {
	return storage.BucketLifecycleRuleArray{
		&storage.BucketLifecycleRuleArgs{
			Action: &storage.BucketLifecycleRuleActionArgs{
				Type: pulumi.String("Delete"),
			},
			Condition: &storage.BucketLifecycleRuleConditionArgs{
				WithState:        pulumi.String("ARCHIVED"),
				NumNewerVersions: pulumi.Int(3),
			},
		},
	}
}

func (mb *MinioBackup) createBackupCronJob(
	ctx *pulumi.Context,
	bucket *storage.Bucket,
) error {
	_, err := batchv1.NewCronJob(
		ctx,
		cronJobName,
		&batchv1.CronJobArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(cronJobName),
				Namespace: pulumi.String(cfgNamespaceLens.Get(mb.Config)),
			},
			Spec: &batchv1.CronJobSpecArgs{
				Schedule: pulumi.String(cfgScheduleLens.Get(mb.Config)),
				JobTemplate: &batchv1.JobTemplateSpecArgs{
					Spec: mb.createJobSpec(false),
				},
			},
		},
		pulumi.DependsOn([]pulumi.Resource{bucket}),
	)
	if err != nil {
		return fmt.Errorf("error creating MinIO backup CronJob: %w", err)
	}
	return nil
}

func (mb *MinioBackup) createImmediateBackupJob(
	ctx *pulumi.Context,
	bucket *storage.Bucket,
) error {
	_, err := batchv1.NewJob(
		ctx,
		immediateJobName,
		&batchv1.JobArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(immediateJobName),
				Namespace: pulumi.String(cfgNamespaceLens.Get(mb.Config)),
			},
			Spec: mb.createJobSpec(true),
		},
		pulumi.DependsOn([]pulumi.Resource{bucket}),
	)
	if err != nil {
		return fmt.Errorf("error creating MinIO immediate backup Job: %w", err)
	}
	return nil
}

func (mb *MinioBackup) createJobSpec(isImmediate bool) *batchv1.JobSpecArgs {
	jobSpec := &batchv1.JobSpecArgs{
		Template:     mb.createPodTemplateSpec(),
		BackoffLimit: pulumi.Int(0),
	}
	if isImmediate {
		jobSpec.TtlSecondsAfterFinished = pulumi.Int(900)
	}
	return jobSpec
}

func (mb *MinioBackup) createPodTemplateSpec() *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Spec: &corev1.PodSpecArgs{
			RestartPolicy: pulumi.String("Never"),
			InitContainers: corev1.ContainerArray{
				mb.createMCDumpContainer(),
			},
			Containers: corev1.ContainerArray{
				mb.createResticBackupContainer(),
			},
			Volumes: corev1.VolumeArray{
				mb.createScratchVolume(),
				mb.createGCSCredentialsVolume(),
			},
		},
	}
}

func (mb *MinioBackup) createScratchVolume() *corev1.VolumeArgs {
	storageCfg := cfgStorageLens.Get(mb.Config)
	return &corev1.VolumeArgs{
		Name: pulumi.String(storageNameLens.Get(storageCfg)),
		Ephemeral: &corev1.EphemeralVolumeSourceArgs{
			VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplateArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Labels: pulumi.StringMap{
						"type": pulumi.String(defaultStorageName),
					},
				},
				Spec: &corev1.PersistentVolumeClaimSpecArgs{
					AccessModes: pulumi.StringArray{
						pulumi.String("ReadWriteOnce"),
					},
					StorageClassName: pulumi.String(storageClassLens.Get(storageCfg)),
					Resources: &corev1.VolumeResourceRequirementsArgs{
						Requests: pulumi.StringMap{
							"storage": pulumi.String(storageSizeLens.Get(storageCfg)),
						},
					},
				},
			},
		},
	}
}

func (mb *MinioBackup) createGCSCredentialsVolume() *corev1.VolumeArgs {
	bucketSecret := cfgBucketSecretLens.Get(mb.Config)
	return &corev1.VolumeArgs{
		Name: pulumi.String("gcs-credentials"),
		Secret: &corev1.SecretVolumeSourceArgs{
			SecretName: pulumi.String(secretKeyPairNameLens.Get(bucketSecret)),
			Items: corev1.KeyToPathArray{
				&corev1.KeyToPathArgs{
					Key:  pulumi.String(secretKeyPairKeyLens.Get(bucketSecret)),
					Path: pulumi.String("gcs-credentials"),
				},
			},
		},
	}
}

func (mb *MinioBackup) createMCDumpContainer() *corev1.ContainerArgs {
	cfg := mb.Config
	minioSec := cfgMinioSecretLens.Get(cfg)
	storageCfg := cfgStorageLens.Get(cfg)
	return &corev1.ContainerArgs{
		Name:    pulumi.String(initContainerName),
		Image:   pulumi.String(mcImageRef(cfg)),
		Command: pulumi.StringArray{pulumi.String("sh")},
		Args:    pulumi.ToStringArray(buildMCDumpArgs()),
		Env: corev1.EnvVarArray{
			&corev1.EnvVarArgs{
				Name:  pulumi.String("MINIO_HOST"),
				Value: pulumi.String(cfgServerLens.Get(cfg)),
			},
			&corev1.EnvVarArgs{
				Name:  pulumi.String("MINIO_PORT"),
				Value: pulumi.String(portString(cfg)),
			},
			&corev1.EnvVarArgs{
				Name: pulumi.String("MINIO_ROOT_USER"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(minioSecretNameLens.Get(minioSec)),
						Key:  pulumi.String(minioSecretUserKeyLens.Get(minioSec)),
					},
				},
			},
			&corev1.EnvVarArgs{
				Name: pulumi.String("MINIO_ROOT_PASSWORD"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(minioSecretNameLens.Get(minioSec)),
						Key:  pulumi.String(minioSecretPassKeyLens.Get(minioSec)),
					},
				},
			},
		},
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name:      pulumi.String(storageNameLens.Get(storageCfg)),
				MountPath: pulumi.String(scratchMountPath),
			},
		},
	}
}

func (mb *MinioBackup) createResticBackupContainer() *corev1.ContainerArgs {
	cfg := mb.Config
	resticSec := cfgResticSecretLens.Get(cfg)
	projectSec := cfgProjectSecretLens.Get(cfg)
	storageCfg := cfgStorageLens.Get(cfg)
	return &corev1.ContainerArgs{
		Name:    pulumi.String(mainContainerName),
		Image:   pulumi.String(resticImageRef(cfg)),
		Command: pulumi.StringArray{pulumi.String("sh")},
		Args:    pulumi.ToStringArray(buildResticBackupArgs()),
		Env: corev1.EnvVarArray{
			&corev1.EnvVarArgs{
				Name:  pulumi.String("RESTIC_REPOSITORY"),
				Value: pulumi.String(resticRepository(cfg)),
			},
			&corev1.EnvVarArgs{
				Name: pulumi.String("RESTIC_PASSWORD"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(secretKeyPairNameLens.Get(resticSec)),
						Key:  pulumi.String(secretKeyPairKeyLens.Get(resticSec)),
					},
				},
			},
			&corev1.EnvVarArgs{
				Name: pulumi.String("GOOGLE_PROJECT_ID"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(secretKeyPairNameLens.Get(projectSec)),
						Key:  pulumi.String(secretKeyPairKeyLens.Get(projectSec)),
					},
				},
			},
			&corev1.EnvVarArgs{
				Name:  pulumi.String("GOOGLE_APPLICATION_CREDENTIALS"),
				Value: pulumi.String("/var/secret/gcs-credentials"),
			},
		},
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name:      pulumi.String(storageNameLens.Get(storageCfg)),
				MountPath: pulumi.String(scratchMountPath),
			},
			&corev1.VolumeMountArgs{
				Name:      pulumi.String("gcs-credentials"),
				MountPath: pulumi.String("/var/secret"),
				ReadOnly:  pulumi.Bool(true),
			},
		},
	}
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	mb := NewMinioBackup(cfg)
	return mb.Install(ctx)
}

func main() {
	pulumi.Run(Run)
}
