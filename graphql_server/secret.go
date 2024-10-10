package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) CreateSecret (ctx *pulumi.Context) (*corev1.Secret, error) {
  config := gs.Config
  secretsData := pulumi.StringMap{
    config.Secrets.AuthKeys.AuthAppId: pulumi.String(config.Secrets.AuthValues.AuthAppId),
    config.Secrets.AuthKeys.AuthAppSecret: pulumi.String(config.Secrets.AuthValues.AuthAppSecret),
    config.Secrets.AuthKeys.JwksURI: pulumi.String(config.Secrets.AuthValues.JwksURI),
    config.Secrets.AuthKeys.JwtAudience: pulumi.String(config.Secrets.AuthValues.JwtAudience),
    config.Secrets.AuthKeys.JwtIssuer: pulumi.String(config.Secrets.AuthValues.JwtIssuer),
    config.Secrets.MinioKeys.MinioAccess: pulumi.String(config.Secrets.MinioValues.MinioAccess),
    config.Secrets.MinioKeys.MinioSecret: pulumi.String(config.Secrets.MinioValues.MinioSecret),
  }

  configMap, err := corev1.NewSecret(ctx, config.Secrets.Name, &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(config.Secrets.Name),
			Namespace: pulumi.String(config.Namespace),
		},
    StringData: secretsData,
  })

  if err != nil {
		return nil, fmt.Errorf("error creating secret: %w", err)
  }

  return configMap, nil
}

