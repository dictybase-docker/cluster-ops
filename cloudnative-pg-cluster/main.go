package main

import (
	"fmt"
	"os"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Properties struct {
	Namespace string `pulumi:"namespace"`
	Secret    struct {
		Filepath string `pulumi:"filepath"`
		Key      string `pulumi:"key"`
		Name     string `pulumi:"name"`
	} `pulumi:"secret"`
}

func NewProperties(ctx *pulumi.Context) (*Properties, error) {
	var props Properties
	cfg := config.New(ctx, "")
	if err := cfg.TryObject("properties", &props); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return &props, nil
}

func (prop *Properties) CreateSecret(
	ctx *pulumi.Context,
) (*corev1.Secret, error) {
	// Read the file content
	fileContent, err := os.ReadFile(prop.Secret.Filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Create the secret
	secret, err := corev1.NewSecret(ctx, prop.Secret.Name, &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(prop.Secret.Name),
			Namespace: pulumi.String(prop.Namespace),
		},
		StringData: pulumi.StringMap{
			prop.Secret.Key: pulumi.String(string(fileContent)),
		},
	})
	if err != nil {
		return nil, err
	}

	return secret, nil
}

func createResources(ctx *pulumi.Context) error {
	// Initialize Properties using the constructor
	props, err := NewProperties(ctx)
	if err != nil {
		return err
	}

	// Create the secret using the receiver method
	secret, err := props.CreateSecret(ctx)
	if err != nil {
		return err
	}

	// Export the secret name
	ctx.Export("secretName", secret.Metadata.Name())

	return nil
}

func main() {
	pulumi.Run(createResources)
}
