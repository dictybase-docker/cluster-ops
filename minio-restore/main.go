package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func ReadConfig(ctx *pulumi.Context) (*RestoreConfig, error) {
	conf := config.New(ctx, "")
	cfg := &RestoreConfig{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read minio-restore config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid minio-restore config: %w", err)
	}
	return cfg, nil
}

type MinioRestore struct {
	Config *RestoreConfig
}

func NewMinioRestore(cfg *RestoreConfig) *MinioRestore {
	return &MinioRestore{Config: cfg}
}

func (mr *MinioRestore) Install(ctx *pulumi.Context) error {
	job, err := mr.createRestoreJob(ctx)
	if err != nil {
		return err
	}

	ctx.Export("jobName", job.Metadata.Name())
	ctx.Export("targetIdentity", pulumi.String(mr.Config.targetIdentity()))
	return nil
}

func (mr *MinioRestore) createRestoreJob(
	ctx *pulumi.Context,
) (*batchv1.Job, error) {
	cfg := mr.Config
	name := cfg.jobName()

	job, err := batchv1.NewJob(ctx, name, &batchv1.JobArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(cfgNamespaceLens.Get(cfg)),
			Labels: pulumi.StringMap{
				"app":        pulumi.String(jobNamePrefix),
				"restore-id": pulumi.String(cfgRestoreIDLens.Get(cfg)),
			},
		},
		Spec: &batchv1.JobSpecArgs{
			BackoffLimit:            pulumi.Int(0),
			TtlSecondsAfterFinished: pulumi.Int(3600),
			Template:                mr.createPodTemplateSpec(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating minio-restore job: %w", err)
	}
	return job, nil
}

func (mr *MinioRestore) createPodTemplateSpec() *corev1.PodTemplateSpecArgs {
	cfg := mr.Config
	return &corev1.PodTemplateSpecArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Labels: pulumi.StringMap{
				"app":        pulumi.String(jobNamePrefix),
				"restore-id": pulumi.String(cfgRestoreIDLens.Get(cfg)),
			},
		},
		Spec: &corev1.PodSpecArgs{
			RestartPolicy: pulumi.String("Never"),
			InitContainers: corev1.ContainerArray{
				mr.createResticContainer(),
			},
			Containers: corev1.ContainerArray{
				mr.createMCRestoreContainer(),
			},
			Volumes: corev1.VolumeArray{
				mr.createScratchVolume(),
				mr.createGCSCredentialsVolume(),
			},
		},
	}
}

func (mr *MinioRestore) createScratchVolume() *corev1.VolumeArgs {
	storageCfg := cfgStorageLens.Get(mr.Config)
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

func (mr *MinioRestore) createGCSCredentialsVolume() *corev1.VolumeArgs {
	bucketSecret := cfgBucketSecretLens.Get(mr.Config)
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

func (mr *MinioRestore) createResticContainer() *corev1.ContainerArgs {
	cfg := mr.Config
	resticSecret := cfgResticSecretLens.Get(cfg)
	projectSecret := cfgProjectSecretLens.Get(cfg)
	storageCfg := cfgStorageLens.Get(cfg)
	return &corev1.ContainerArgs{
		Name:  pulumi.String(initContainerName),
		Image: pulumi.String(resticImageRef(cfg)),
		Args:  pulumi.ToStringArray(buildResticRestoreArgs(cfg)),
		Env: corev1.EnvVarArray{
			&corev1.EnvVarArgs{
				Name: pulumi.String("RESTIC_PASSWORD"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(secretKeyPairNameLens.Get(resticSecret)),
						Key:  pulumi.String(secretKeyPairKeyLens.Get(resticSecret)),
					},
				},
			},
			&corev1.EnvVarArgs{
				Name: pulumi.String("GOOGLE_PROJECT_ID"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(secretKeyPairNameLens.Get(projectSecret)),
						Key:  pulumi.String(secretKeyPairKeyLens.Get(projectSecret)),
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

func (mr *MinioRestore) createMCRestoreContainer() *corev1.ContainerArgs {
	cfg := mr.Config
	minioSec := cfgMinioSecretLens.Get(cfg)
	storageCfg := cfgStorageLens.Get(cfg)
	return &corev1.ContainerArgs{
		Name:    pulumi.String(mainContainerName),
		Image:   pulumi.String(mcImageRef(cfg)),
		Command: pulumi.StringArray{pulumi.String("sh")},
		Args:    pulumi.ToStringArray(buildMCRestoreArgs()),
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
				Name:  pulumi.String("MINIO_TARGET_BUCKET"),
				Value: pulumi.String(cfgS3BucketLens.Get(cfg)),
			},
			&corev1.EnvVarArgs{
				Name:  pulumi.String("MINIO_OVERWRITE"),
				Value: pulumi.String(overwriteEnvValue(cfgOverwriteLens.Get(cfg))),
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
				ReadOnly:  pulumi.Bool(false),
			},
		},
	}
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	mr := NewMinioRestore(cfg)
	return mr.Install(ctx)
}

func main() {
	pulumi.Run(Run)
}
