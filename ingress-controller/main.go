package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type IngressController struct {
	Config *IngressControllerConfig
}

type IngressControllerConfig struct {
	Namespace string
	Chart     ChartConfig
}

type ChartConfig struct {
	Name       string
	Repository string
	Version    string
}

func ReadConfig(ctx *pulumi.Context) (*IngressControllerConfig, error) {
	conf := config.New(ctx, "")
	cfg := &IngressControllerConfig{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf(
			"failed to read ingress-controller config: %w",
			err,
		)
	}
	return cfg, nil
}

func (ic *IngressController) Install(ctx *pulumi.Context) error {
	config := ic.Config

	// Create the namespace
	namespace, err := corev1.NewNamespace(
		ctx,
		config.Namespace,
		&corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(config.Namespace),
			},
		},
	)
	if err != nil {
		return fmt.Errorf(
			"failed to create namespace %s: %w",
			config.Namespace,
			err,
		)
	}

	// Install the Helm chart
	_, err = helm.NewRelease(ctx, config.Chart.Name, &helm.ReleaseArgs{
		Chart:     pulumi.String(config.Chart.Name),
		Version:   pulumi.String(config.Chart.Version),
		Namespace: namespace.Metadata.Name().Elem(),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(config.Chart.Repository),
		},
		Values: pulumi.Map{
			"controller": pulumi.Map{
				"service": pulumi.Map{
					"externalTrafficPolicy": pulumi.String("Local"),
				},
			},
		},
	}, pulumi.DependsOn([]pulumi.Resource{namespace}))
	if err != nil {
		return fmt.Errorf(
			"failed to install %s Helm chart: %w",
			config.Chart.Name,
			err,
		)
	}
	return nil
}

func Run(ctx *pulumi.Context) error {
	config, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	ic := &IngressController{
		Config: config,
	}

	if err = ic.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
