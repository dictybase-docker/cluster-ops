package main

import (
	"fmt"

	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func ReadConfig(ctx *pulumi.Context) (*ArangoClusterConfig, error) {
	conf := config.New(ctx, "")
	arangoConfig := &ArangoClusterConfig{}
	if err := conf.TryObject("properties", arangoConfig); err != nil {
		return nil, fmt.Errorf("failed to read arangodb-cluster config: %w", err)
	}
	if arangoConfig.Name == "" {
		arangoConfig.Name = defaultName
	}
	if err := arangoConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid arangodb-cluster config: %w", err)
	}
	return arangoConfig, nil
}

func CreateResources(ctx *pulumi.Context, cfg *ArangoClusterConfig) error {
	secret, err := corev1.NewSecret(ctx, "arangodb-secret", &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(cfg.Secret.Name),
			Namespace: pulumi.String(cfg.Namespace),
			Labels: pulumi.StringMap{
				"velero.io/exclude-from-backup": pulumi.String("true"),
			},
		},
		StringData: pulumi.StringMap{
			"password": pulumi.String(cfg.Secret.Password),
		},
	})
	if err != nil {
		return fmt.Errorf("error creating secret: %w", err)
	}

	specMap := buildClusterSpec(cfg)

	arangoCR, err := apiextensions.NewCustomResource(
		ctx,
		cfg.Name,
		&apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("database.arangodb.com/v1"),
			Kind:       pulumi.String("ArangoDeployment"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(cfg.Name),
				Namespace: pulumi.String(cfg.Namespace),
				Labels: pulumi.StringMap{
					"velero.io/exclude-from-backup": pulumi.String("true"),
				},
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": specMap,
			},
		},
		pulumi.DependsOn([]pulumi.Resource{secret}),
	)
	if err != nil {
		return fmt.Errorf("error creating ArangoDeployment custom resource: %w", err)
	}

	ctx.Export("arangoName", arangoCR.Metadata.Name())
	ctx.Export("secretName", secret.Metadata.Name())
	return nil
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	return CreateResources(ctx, cfg)
}

func main() {
	pulumi.Run(Run)
}
