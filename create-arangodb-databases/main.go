package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ArangoDBConfig struct {
	Namespace      string
	ArangodbSecret struct {
		Name    string
		User    string
		Pass    string
		UserKey string
		PassKey string
	}
	Databases []string
	Grant     string
	Image     struct {
		Name string
		Tag  string
	}
}

func ReadConfig(ctx *pulumi.Context) (*ArangoDBConfig, error) {
	conf := config.New(ctx, "")
	arangoConfig := &ArangoDBConfig{}
	if err := conf.TryObject("properties", arangoConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read create-arangodb-databases config: %w",
			err,
		)
	}
	return arangoConfig, nil
}

func createArangoDBSecret(ctx *pulumi.Context, config *ArangoDBConfig) error {
	secretName := config.ArangodbSecret.Name
	secretArgs := &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(secretName),
			Namespace: pulumi.String(config.Namespace),
		},
		StringData: pulumi.StringMap{
			config.ArangodbSecret.UserKey: pulumi.String(config.ArangodbSecret.User),
			config.ArangodbSecret.PassKey: pulumi.String(config.ArangodbSecret.Pass),
		},
		Type: pulumi.String("Opaque"),
	}

	_, err := corev1.NewSecret(ctx, secretName, secretArgs)
	if err != nil {
		return fmt.Errorf("error creating ArangoDB secret: %w", err)
	}

	return nil
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		config, err := ReadConfig(ctx)
		if err != nil {
			return err
		}

		if err := createArangoDBSecret(ctx, config); err != nil {
			return err
		}

		return nil
	})
}
