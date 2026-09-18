package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
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

type AuthConfig struct {
	SecretName string
	Key        string
	Token      string
}

type NatsConfig struct {
	Chart     ChartConfig
	Image     ImageConfig
	Namespace string
	Placement PlacementConfig
	Auth      AuthConfig
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
	// Core pub/sub: no JetStream, no resolver, no PVC — the server is
	// stateless and in-flight messages are lost on a pod restart. JetStream
	// persistence is reserved for a future revision.
	values := pulumi.Map{
		"container": pulumi.Map{
			"image": pulumi.Map{
				"tag": pulumi.String(n.Config.Image.Tag),
			},
		},
	}

	// Token auth: the chart merges an authorization block into nats.conf
	// whose token value is the $TOKEN env variable (<< $TOKEN >> is the
	// chart's unquoted-variable syntax; the NATS server itself expands
	// $VARIABLE references from its process env at startup), and the TOKEN
	// env var comes from the auth Secret via secretKeyRef.
	// Unauthenticated NATS accepts any command from any pod in the cluster.
	container, ok := values["container"].(pulumi.Map)
	if !ok {
		return values
	}
	if n.Config.Auth.SecretName != "" {
		container["env"] = pulumi.Map{
			"TOKEN": pulumi.Map{
				"valueFrom": pulumi.Map{
					"secretKeyRef": pulumi.Map{
						"name": pulumi.String(n.Config.Auth.SecretName),
						"key":  pulumi.String(n.Config.Auth.Key),
					},
				},
			},
		}
		values["config"] = pulumi.Map{
			"merge": pulumi.Map{
				"authorization": pulumi.Map{
					"token": pulumi.String("<< $TOKEN >>"),
				},
			},
		}
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

// createAuthSecret stores the NATS auth token consumed through the $TOKEN
// env variable in the nats.conf authorization block.
func (n *Nats) createAuthSecret(
	ctx *pulumi.Context,
) (*corev1.Secret, error) {
	secret, err := corev1.NewSecret(
		ctx,
		n.Config.Auth.SecretName,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(n.Config.Auth.SecretName),
				Namespace: pulumi.String(n.Config.Namespace),
			},
			StringData: pulumi.StringMap{
				n.Config.Auth.Key: pulumi.String(n.Config.Auth.Token),
			},
		})
	if err != nil {
		return nil, fmt.Errorf("error creating auth Secret: %w", err)
	}
	return secret, nil
}

func (n *Nats) Install(ctx *pulumi.Context) error {
	var depends []pulumi.Resource
	if n.Config.Auth.SecretName != "" {
		authSecret, err := n.createAuthSecret(ctx)
		if err != nil {
			return err
		}
		depends = append(depends, authSecret)
	}

	_, err := helm.NewRelease(ctx, "nats", &helm.ReleaseArgs{
		Name:      pulumi.String(n.Config.Chart.Name),
		Chart:     pulumi.String(n.Config.Chart.Name),
		Version:   pulumi.String(n.Config.Chart.Version),
		Namespace: pulumi.String(n.Config.Namespace),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(n.Config.Chart.Repository),
		},
		Values: n.getHelmValues(),
	}, pulumi.DependsOn(depends))
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
