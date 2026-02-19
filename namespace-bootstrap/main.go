package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Config struct {
	Namespace string
}

func readConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "")
	cfg := &Config{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read namespace-bootstrap config: %w", err)
	}
	return cfg, nil
}

func run(ctx *pulumi.Context) error {
	cfg, err := readConfig(ctx)
	if err != nil {
		return err
	}

	_, err = corev1.NewNamespace(ctx, cfg.Namespace, &corev1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String(cfg.Namespace),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create namespace %s: %w", cfg.Namespace, err)
	}

	return nil
}

func main() {
	pulumi.Run(run)
}
