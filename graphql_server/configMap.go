package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) CreateConfigMap (ctx *pulumi.Context) (*corev1.ConfigMap, error) {
  config := gs.Config
  configMapData := pulumi.StringMap{
    config.ConfigMap.EndpointKeys.AuthEndpoint: pulumi.String(config.ConfigMap.EndpointValues.AuthEndpoint),
    config.ConfigMap.EndpointKeys.OrganismEndpoint: pulumi.String(config.ConfigMap.EndpointValues.OrganismEndpoint),
    config.ConfigMap.EndpointKeys.PublicationAPIEndpoint: pulumi.String(config.ConfigMap.EndpointValues.PublicationAPIEndpoint),
    config.ConfigMap.EndpointKeys.S3StorageEndpoint: pulumi.String(config.ConfigMap.EndpointValues.S3StorageEndpoint),
    config.ConfigMap.GRPCKeys.StockHost: pulumi.String(config.ConfigMap.GRPCValues.StockHost),
    config.ConfigMap.GRPCKeys.StockPort: pulumi.String(config.ConfigMap.GRPCValues.StockPort),
    config.ConfigMap.GRPCKeys.OrderHost: pulumi.String(config.ConfigMap.GRPCValues.OrderHost),
    config.ConfigMap.GRPCKeys.OrderPort: pulumi.String(config.ConfigMap.GRPCValues.OrderPort),
    config.ConfigMap.GRPCKeys.AnnotationHost: pulumi.String(config.ConfigMap.GRPCValues.AnnotationHost),
    config.ConfigMap.GRPCKeys.AnnotationPort: pulumi.String(config.ConfigMap.GRPCValues.AnnotationPort),
    config.ConfigMap.GRPCKeys.ContentHost: pulumi.String(config.ConfigMap.GRPCValues.ContentHost),
    config.ConfigMap.GRPCKeys.ContentPort: pulumi.String(config.ConfigMap.GRPCValues.ContentPort),
    config.ConfigMap.GRPCKeys.RedisHost: pulumi.String(config.ConfigMap.GRPCValues.RedisHost),
    config.ConfigMap.GRPCKeys.RedisPort: pulumi.String(config.ConfigMap.GRPCValues.RedisPort),
  }

  configMap, err := corev1.NewConfigMap(ctx, config.ConfigMap.Name, &corev1.ConfigMapArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(config.ConfigMap.Name),
			Namespace: pulumi.String(config.Namespace),
		},
    Data: configMapData,
  })

  if err != nil {
		return nil, fmt.Errorf("error creating configMap: %w", err)
  }

  return configMap, nil
}

