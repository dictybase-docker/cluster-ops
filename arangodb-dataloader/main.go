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
	}
}

func (adl *ArangodbDataloader) createDataloaderArgs(
	imp ImportConfig,
) pulumi.StringArray {
	return pulumi.StringArray{
		pulumi.String("--log-level"), pulumi.String(adl.Config.LogLevel),
		pulumi.String("load-arangodb"),
		pulumi.String("--arangodb-database"), pulumi.String(imp.Database),
		pulumi.String("--s3-bucket-path"), pulumi.String(imp.BucketPath),
		pulumi.String("--output-dir"), pulumi.String(imp.OutputDir),
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
			Name: pulumi.String("MINIO_USER"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(adl.Config.MinioSecret.Name),
					Key:  pulumi.String(adl.Config.MinioSecret.UserKey),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("MINIO_PASS"),
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
