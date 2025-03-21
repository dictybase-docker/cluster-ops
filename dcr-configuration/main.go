package main

import (
	"fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
)

// SecretConfig represents a Kubernetes secret configuration
type SecretConfig struct {
	Name string            `json:"name"`
	Data map[string]string `json:"data"`
}

// Config represents the main configuration for the project
type Config struct {
	Namespace string         `json:"namespace"`
	Secrets   []SecretConfig `json:"secrets"`
}

// ReadConfig reads the configuration from Pulumi
func ReadConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "")
	cfg := &Config{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read configuration: %w", err)
	}
	return cfg, nil
}

// Run is the main function that creates all resources
func Run(ctx *pulumi.Context) error {
	// Read configuration
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	// Create all secrets defined in the configuration
	for _, secretCfg := range cfg.Secrets {
		_, err := CreateSecret(ctx, secretCfg.Name, cfg.Namespace, secretCfg.Data)
		if err != nil {
			return err
		}
		ctx.Export(fmt.Sprintf("secret-%s", secretCfg.Name), pulumi.String(secretCfg.Name))
	}

	return nil
}

// CreateSecret creates a Kubernetes Secret resource
func CreateSecret(
	ctx *pulumi.Context,
	name string,
	namespace string,
	data map[string]string,
) (*corev1.Secret, error) {
	// Convert the map to pulumi.StringMap
	secretData := pulumi.StringMap{}
	for key, value := range data {
		secretData[key] = pulumi.String(value)
	}

	secret, err := corev1.NewSecret(ctx, name, &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(namespace),
		},
		StringData: secretData,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating secret %s: %w", name, err)
	}

	return secret, nil
}

func main() {
	pulumi.Run(Run)
}
