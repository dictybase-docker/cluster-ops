package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
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

type PlacementConfig struct {
	Pool string
}

type NatsConfig struct {
	Chart     ChartConfig
	Image     ImageConfig
	Namespace string
	Placement PlacementConfig
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
	// Core pub/sub, unauthenticated: no JetStream, no resolver, no PVC, and
	// no authorization block — the server is stateless and accepts any
	// in-cluster client. JetStream persistence and token auth are reserved
	// for a future revision.
	values := pulumi.Map{
		"container": pulumi.Map{
			"image": pulumi.Map{
				"tag": pulumi.String(n.Config.Image.Tag),
			},
		},
	}

	// Pool placement: pin the pods to the stateful pool with the matching
	// dedicated=<pool>:NoSchedule toleration, mirroring the other stateful
	// services on this cluster.
	if n.Config.Placement.Pool != "" {
		values["podTemplate"] = pulumi.Map{
			"merge": pulumi.Map{
				"spec": pulumi.Map{
					"nodeSelector": pulumi.StringMap{
						"pool": pulumi.String(n.Config.Placement.Pool),
					},
					"tolerations": corev1.TolerationArray{
						&corev1.TolerationArgs{
							Key:      pulumi.String("dedicated"),
							Operator: pulumi.String("Equal"),
							Value:    pulumi.String(n.Config.Placement.Pool),
							Effect:   pulumi.String("NoSchedule"),
						},
					},
				},
			},
		}
	}

	return values
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
