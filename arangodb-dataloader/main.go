package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type DataloaderConfig struct {
	Namespace      string
	LogLevel       string
	Import         []ImportConfig
	Folder         string
	ArangodbSecret struct {
		Name    string
		UserKey string
		PassKey string
	}
	MinioSecret struct {
		Name    string
		UserKey string
		PassKey string
	}
	Storage struct {
		Class string
		Name  string
		Size  string
	}
	Image ImageConfig
}

type ImportConfig struct {
	Database   string
	BucketPath string
	OutputDir  string
}

type ImageConfig struct {
	Name string
	Tag  string
}

type ArangodbDataloader struct {
	Config *DataloaderConfig
}

func ReadConfig(ctx *pulumi.Context) (*DataloaderConfig, error) {
	conf := config.New(ctx, "")
	loaderConfig := &DataloaderConfig{}
	if err := conf.TryObject("properties", loaderConfig); err != nil {
		return nil, fmt.Errorf("failed to read dataloader config: %w", err)
	}
	return loaderConfig, nil
}

func NewDataloader(config *DataloaderConfig) *ArangodbDataloader {
	return &ArangodbDataloader{
		Config: config,
	}
}

func (adl *ArangodbDataloader) Install(ctx *pulumi.Context) error {
	for _, imp := range adl.Config.Import {
		if err := adl.createDataloaderJob(ctx, imp); err != nil {
			return err
		}
	}
	return nil
}

func (adl *ArangodbDataloader) createDataloaderJob(
	ctx *pulumi.Context,
	imp ImportConfig,
) error {
	jobName := fmt.Sprintf("arangodb-dataloader-%s", imp.Database)
	jobArgs := &batchv1.JobArgs{
		Metadata: adl.createJobMetadata(jobName),
		Spec:     adl.createJobSpec(imp),
	}

	_, err := batchv1.NewJob(ctx, jobName, jobArgs)
	if err != nil {
		return fmt.Errorf(
			"error creating Kubernetes Job for database %s: %w",
			imp.Database,
			err,
		)
	}
	return nil
}

func (adl *ArangodbDataloader) createJobMetadata(
	name string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(adl.Config.Namespace),
	}
}

func (adl *ArangodbDataloader) createJobSpec(
	imp ImportConfig,
) *batchv1.JobSpecArgs {
	return &batchv1.JobSpecArgs{
		Template:                adl.createPodTemplateSpec(imp),
		BackoffLimit:            pulumi.Int(0),
		TtlSecondsAfterFinished: pulumi.Int(900), // 15 minutes
	}
}

func (adl *ArangodbDataloader) createPodTemplateSpec(
	imp ImportConfig,
) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Spec: &corev1.PodSpecArgs{
			Containers: corev1.ContainerArray{
				adl.createDataloaderContainer(imp),
			},
			RestartPolicy: pulumi.String("Never"),
			Volumes: corev1.VolumeArray{
				adl.createBackupVolume(),
			},
		},
	}
}

func (adl *ArangodbDataloader) createDataloaderContainer(
	imp ImportConfig,
) *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name: pulumi.String("dataloader"),
		Image: pulumi.Sprintf(
			"%s:%s",
			adl.Config.Image.Name,
			adl.Config.Image.Tag,
		),
		Command: pulumi.StringArray{
			pulumi.String("dataloader"),
		},
		Args: adl.createDataloaderArgs(imp),
		Env:  adl.createDataloaderEnv(),
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name:      pulumi.String(adl.Config.Storage.Name),
				MountPath: pulumi.String(adl.Config.Folder),
			},
		},
	}
}

func (adl *ArangodbDataloader) createDataloaderArgs(
	imp ImportConfig,
) pulumi.StringArray {
	// Construct full output path by joining folder and output dir
	outputPath := fmt.Sprintf("%s/%s", adl.Config.Folder, imp.OutputDir)

	return pulumi.StringArray{
		pulumi.String("--log-level"), pulumi.String(adl.Config.LogLevel),
		pulumi.String("load-arangodb"),
		pulumi.String("--arangodb-database"), pulumi.String(imp.Database),
		pulumi.String("--arangodb-user"), pulumi.String("$(ARANGODB_USER)"),
		pulumi.String("--arangodb-pass"), pulumi.String("$(ARANGODB_PASS)"),
		pulumi.String("--s3-bucket-path"), pulumi.String(imp.BucketPath),
		pulumi.String("--output-dir"), pulumi.String(outputPath),
	}
}

func (adl *ArangodbDataloader) createBackupVolume() *corev1.VolumeArgs {
	return &corev1.VolumeArgs{
		Name: pulumi.String(adl.Config.Storage.Name),
		Ephemeral: &corev1.EphemeralVolumeSourceArgs{
			VolumeClaimTemplate: adl.createVolumeClaimTemplate(),
		},
	}
}

func (adl *ArangodbDataloader) createVolumeClaimTemplate() *corev1.PersistentVolumeClaimTemplateArgs {
	return &corev1.PersistentVolumeClaimTemplateArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Labels: pulumi.StringMap{
				"type": pulumi.String("arangodb-dataloader-ephemeral"),
			},
		},
		Spec: adl.createPersistentVolumeClaimSpec(),
	}
}

func (adl *ArangodbDataloader) createPersistentVolumeClaimSpec() *corev1.PersistentVolumeClaimSpecArgs {
	return &corev1.PersistentVolumeClaimSpecArgs{
		AccessModes:      pulumi.StringArray{pulumi.String("ReadWriteOnce")},
		StorageClassName: pulumi.String(adl.Config.Storage.Class),
		Resources: &corev1.VolumeResourceRequirementsArgs{
			Requests: pulumi.StringMap{
				"storage": pulumi.String(adl.Config.Storage.Size),
			},
		},
	}
}

func (adl *ArangodbDataloader) createDataloaderEnv() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name: pulumi.String("ARANGODB_USER"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(adl.Config.ArangodbSecret.Name),
					Key:  pulumi.String(adl.Config.ArangodbSecret.UserKey),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("ARANGODB_PASS"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(adl.Config.ArangodbSecret.Name),
					Key:  pulumi.String(adl.Config.ArangodbSecret.PassKey),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("ACCESS_KEY"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(adl.Config.MinioSecret.Name),
					Key:  pulumi.String(adl.Config.MinioSecret.UserKey),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("SECRET_KEY"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(adl.Config.MinioSecret.Name),
					Key:  pulumi.String(adl.Config.MinioSecret.PassKey),
				},
			},
		},
	}
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		config, err := ReadConfig(ctx)
		if err != nil {
			return err
		}

		dataloader := NewDataloader(config)
		if err := dataloader.Install(ctx); err != nil {
			return err
		}

		return nil
	})
}
