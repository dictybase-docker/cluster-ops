package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type LoadUniprotMappingConfig struct {
	AppName string
	Image   struct {
		Name string
		Tag  string
	}
	LogLevel  string
	Namespace string
	Runner    struct {
		Command    string
		Subcommand string
	}
}

func ReadConfig(ctx *pulumi.Context) (*LoadUniprotMappingConfig, error) {
	conf := config.New(ctx, "")
	loadConfig := &LoadUniprotMappingConfig{}
	if err := conf.TryObject("properties", loadConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read load-uniprot-mapping config: %w",
			err,
		)
	}
	return loadConfig, nil
}

type LoadUniprotMapping struct {
	Config *LoadUniprotMappingConfig
}

func NewLoadUniprotMapping(
	config *LoadUniprotMappingConfig,
) *LoadUniprotMapping {
	return &LoadUniprotMapping{
		Config: config,
	}
}

func (lum *LoadUniprotMapping) CreateJob(ctx *pulumi.Context) error {
	_, err := batchv1.NewJob(ctx, lum.Config.AppName, &batchv1.JobArgs{
		Metadata: lum.createMetadata(),
		Spec:     lum.createJobSpec(),
	})
	if err != nil {
		return fmt.Errorf("error creating Job: %w", err)
	}
	return nil
}

func (lum *LoadUniprotMapping) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(lum.Config.AppName),
		Namespace: pulumi.String(lum.Config.Namespace),
	}
}

func (lum *LoadUniprotMapping) createJobSpec() *batchv1.JobSpecArgs {
	return &batchv1.JobSpecArgs{
		Template:                lum.createPodTemplateSpec(),
		BackoffLimit:            pulumi.Int(0),
		TtlSecondsAfterFinished: pulumi.Int(600), // 10 minutes = 600 seconds
	}
}

func (lum *LoadUniprotMapping) createPodTemplateSpec() *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Spec: lum.createPodSpec(),
	}
}

func (lum *LoadUniprotMapping) createPodSpec() *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		RestartPolicy: pulumi.String("Never"),
		Containers:    lum.createContainers(),
	}
}

func (lum *LoadUniprotMapping) createContainers() corev1.ContainerArray {
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name:    pulumi.String(lum.Config.AppName),
			Image:   lum.createImageName(),
			Command: lum.createCommand(),
			Args:    lum.createArgs(),
		},
	}
}

func (lum *LoadUniprotMapping) createImageName() pulumi.StringInput {
	return pulumi.String(
		fmt.Sprintf("%s:%s", lum.Config.Image.Name, lum.Config.Image.Tag),
	)
}

func (lum *LoadUniprotMapping) createCommand() pulumi.StringArrayInput {
	return pulumi.StringArray{
		pulumi.String(lum.Config.Runner.Command),
	}
}

func (lum *LoadUniprotMapping) createArgs() pulumi.StringArray {
	return pulumi.ToStringArray([]string{
		"--log-level",
		lum.Config.LogLevel,
		lum.Config.Runner.Subcommand,
	})
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		config, err := ReadConfig(ctx)
		if err != nil {
			return err
		}

		loadUniprotMapping := NewLoadUniprotMapping(config)

		if err := loadUniprotMapping.CreateJob(ctx); err != nil {
			return err
		}

		return nil
	})
}
