package main

import (
	"fmt"

	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ChartConfig struct {
	Name       string
	Repository string
	Version    string
}

type ImageConfig struct {
	Tag string
}

type StorageConfig struct {
	Class string
	Size  string
}

type NatsConfig struct {
	Chart     ChartConfig
	Image     ImageConfig
	Namespace string
	Storage   StorageConfig
}

type Nats struct {
	Config *NatsConfig
}

func ReadConfig(ctx *pulumi.Context) (*NatsConfig, error) {
	conf := config.New(ctx, "")
	natsConfig := &NatsConfig{}
	if err := conf.TryObject("properties", natsConfig); err != nil {
		return nil, fmt.Errorf("failed to read nats config: %w", err)
	}
	return natsConfig, nil
}

func NewNats(config *NatsConfig) *Nats {
	return &Nats{
		Config: config,
	}
}

func (n *Nats) getHelmValues() pulumi.Map {
	return pulumi.Map{
		"container": pulumi.Map{
			"image": pulumi.Map{
				"tag": pulumi.String(n.Config.Image.Tag),
			},
		},
		"config": pulumi.Map{
			"resolver": pulumi.Map{
				"pvc": pulumi.Map{
					"size":             pulumi.String(n.Config.Storage.Size),
					"storageClassName": pulumi.String(n.Config.Storage.Class),
				},
			},
		},
	}
}

func (n *Nats) Install(ctx *pulumi.Context) error {
	_, err := helm.NewRelease(ctx, "nats", &helm.ReleaseArgs{
		Name:      pulumi.String(n.Config.Chart.Name),
		Chart:     pulumi.String(n.Config.Chart.Name),
		Version:   pulumi.String(n.Config.Chart.Version),
		Namespace: pulumi.String(n.Config.Namespace),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(n.Config.Chart.Repository),
		},
		Values: n.getHelmValues(),
	})
	if err != nil {
		return fmt.Errorf("failed to install Helm chart: %w", err)
	}
	return nil
}

func Run(ctx *pulumi.Context) error {
	natsConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	nats := NewNats(natsConfig)

	if err := nats.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
