package main

import (
	"fmt"
	"strings"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type LoadContentConfig struct {
	AppName     string
	BucketPath  []string
	Image       ImageConfig
	LogLevel    string
	MinioSecret struct {
		Name    string
		PassKey string
		UserKey string
	}
	Namespace string
	Runner    struct {
		Command    string
		Subcommand string
	}
}

type ImageConfig struct {
	Name string
	Tag  string
}

type LoadContent struct {
	Config *LoadContentConfig
}

func ReadConfig(ctx *pulumi.Context) (*LoadContentConfig, error) {
	conf := config.New(ctx, "")
	loadContentConfig := &LoadContentConfig{}
	if err := conf.TryObject("properties", loadContentConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read load-content-from-s3 config: %w",
			err,
		)
	}
	return loadContentConfig, nil
}

func NewLoadContent(config *LoadContentConfig) *LoadContent {
	return &LoadContent{
		Config: config,
	}
}

func (lc *LoadContent) containerEnvSpec() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		corev1.EnvVarArgs{
			Name: pulumi.String("ACCESS_KEY"),
			ValueFrom: corev1.EnvVarSourceArgs{
				SecretKeyRef: corev1.SecretKeySelectorArgs{
					Name: pulumi.String(lc.Config.MinioSecret.Name),
					Key:  pulumi.String(lc.Config.MinioSecret.UserKey),
				},
			},
		},
		corev1.EnvVarArgs{
			Name: pulumi.String("SECRET_KEY"),
			ValueFrom: corev1.EnvVarSourceArgs{
				SecretKeyRef: corev1.SecretKeySelectorArgs{
					Name: pulumi.String(lc.Config.MinioSecret.Name),
					Key:  pulumi.String(lc.Config.MinioSecret.PassKey),
				},
			},
		},
	}
}

func (lc *LoadContent) containerSpec() corev1.ContainerArray {
	containers := make(corev1.ContainerArray, 0, len(lc.Config.BucketPath))
	for _, bucketPath := range lc.Config.BucketPath {
		containers = append(containers, lc.createContainerForBucket(bucketPath))
	}
	return containers
}

func (lc *LoadContent) createContainerForBucket(
	bucketPath string,
) corev1.ContainerArgs {
	return corev1.ContainerArgs{
		Name:    lc.generateContainerName(bucketPath),
		Image:   lc.generateImageName(),
		Command: pulumi.ToStringArray([]string{lc.Config.Runner.Command}),
		Args:    lc.generateContainerArgs(bucketPath),
		Env:     lc.containerEnvSpec(),
	}
}

func (lc *LoadContent) generateContainerName(
	bucketPath string,
) pulumi.StringInput {
	return pulumi.String(fmt.Sprintf(
		"%s-container-%s",
		lc.Config.AppName,
		strings.ReplaceAll(bucketPath, "/", "-"),
	))
}

func (lc *LoadContent) generateImageName() pulumi.StringInput {
	return pulumi.String(fmt.Sprintf(
		"%s:%s",
		lc.Config.Image.Name,
		lc.Config.Image.Tag,
	))
}

func (lc *LoadContent) generateContainerArgs(
	bucketPath string,
) pulumi.StringArrayInput {
	return pulumi.ToStringArray([]string{
		"--log-level",
		lc.Config.LogLevel,
		lc.Config.Runner.Subcommand,
		"--s3-bucket-path",
		bucketPath,
	})
}

func (lc *LoadContent) createJob(ctx *pulumi.Context) error {
	_, err := batchv1.NewJob(
		ctx,
		fmt.Sprintf("%s-from-minio", lc.Config.AppName),
		&batchv1.JobArgs{
			Metadata: lc.createJobMetadata(),
			Spec:     lc.createJobSpec(),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}
	return nil
}

func (lc *LoadContent) createJobMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(lc.Config.AppName),
		Namespace: pulumi.String(lc.Config.Namespace),
		Labels:    lc.createJobLabels(),
	}
}

func (lc *LoadContent) createJobLabels() pulumi.StringMap {
	return pulumi.StringMap{
		"app": pulumi.String(fmt.Sprintf("%s-pulumi", lc.Config.AppName)),
	}
}

func (lc *LoadContent) createJobSpec() batchv1.JobSpecArgs {
	return batchv1.JobSpecArgs{
		Template:                lc.createPodTemplateSpec(),
		BackoffLimit:            pulumi.Int(0),
		TtlSecondsAfterFinished: pulumi.Int(600), // 10 minutes = 600 seconds
	}
}

func (lc *LoadContent) createPodTemplateSpec() *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Metadata: lc.createPodMetadata(),
		Spec:     lc.createPodSpec(),
	}
}

func (lc *LoadContent) createPodMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:   pulumi.String(fmt.Sprintf("%s-template", lc.Config.AppName)),
		Labels: lc.createPodLabels(),
	}
}

func (lc *LoadContent) createPodLabels() pulumi.StringMap {
	return pulumi.StringMap{
		"app": pulumi.String(
			fmt.Sprintf("%s-pulumi-template", lc.Config.AppName),
		),
	}
}

func (lc *LoadContent) createPodSpec() *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		RestartPolicy: pulumi.String("Never"),
		Containers:    lc.containerSpec(),
	}
}

func Run(ctx *pulumi.Context) error {
	loadContentConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	loadContent := NewLoadContent(loadContentConfig)

	if err := loadContent.createJob(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
