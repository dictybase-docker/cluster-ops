package main

import (
	"fmt"

	nginxingress "github.com/pulumi/pulumi-kubernetes-ingress-nginx/sdk/go/kubernetes-ingress-nginx"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Config struct {
	// Add fields here as needed
}

func ReadConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "")
	cfg := &Config{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf(
			"failed to read ingress-controller config: %w",
			err,
		)
	}
	return cfg, nil
}

func Run(ctx *pulumi.Context) error {
	// Create an NGINX Ingress Controller
	_, err := nginxingress.NewIngressController(
		ctx,
		"nginx-ingress",
		&nginxingress.IngressControllerArgs{
			Controller: &nginxingress.ControllerArgs{
				PublishService: &nginxingress.ControllerPublishServiceArgs{
					Enabled: pulumi.Bool(true),
				},
			},
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
