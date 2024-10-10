package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) SecretEnvArgsArray() corev1.EnvVarArray {
	secrets := gs.Config.Secrets
	envVars := []struct {
		name   string
		secret string
	}{
		{"SECRET_KEY", secrets.MinioKeys.MinioSecret},
		{"ACCESS_KEY", secrets.MinioKeys.MinioAccess},
		{"JWT_AUDIENCE", secrets.AuthKeys.JwtAudience},
		{"JWT_ISSUER", secrets.AuthKeys.JwtIssuer},
		{"JWKS_PUBLIC_URI", secrets.AuthKeys.JwksURI},
		{"APPLICATION_SECRET", secrets.AuthKeys.AuthAppSecret},
		{"APPLICATION_ID", secrets.AuthKeys.AuthAppId},
	}

	var envVarArray corev1.EnvVarArray
	for _, envVar := range envVars {
		envVarArray = append(envVarArray, &corev1.EnvVarArgs{
			Name: pulumi.String(envVar.name),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(secrets.Name),
					Key:  pulumi.String(envVar.secret),
				},
			},
		})
	}
	return envVarArray
}

func (gs *GraphqlServer) ConfigMapEnvArgsArray() corev1.EnvVarArray {
	configMap := gs.Config.ConfigMap
	envVars := []struct {
		name string
		key  string
	}{
		{
			"PUBLICATION_API_ENDPOINT",
			configMap.EndpointKeys.PublicationAPIEndpoint,
		},
		{"S3_STORAGE_ENDPOINT", configMap.EndpointKeys.S3StorageEndpoint},
		{"AUTH_ENDPOINT", configMap.EndpointKeys.AuthEndpoint},
		{"ORGANISM_API_ENDPOINT", configMap.EndpointKeys.OrganismEndpoint},
		{"STOCK_API_SERVICE_HOST", configMap.GRPCKeys.StockHost},
		{"STOCK_API_SERVICE_PORT", configMap.GRPCKeys.StockPort},
		{"ORDER_API_SERVICE_HOST", configMap.GRPCKeys.OrderHost},
		{"ORDER_API_SERVICE_PORT", configMap.GRPCKeys.OrderPort},
		{"ANNOTATION_API_SERVICE_HOST", configMap.GRPCKeys.AnnotationHost},
		{"ANNOTATION_API_SERVICE_PORT", configMap.GRPCKeys.AnnotationPort},
		{"CONTENT_API_SERVICE_HOST", configMap.GRPCKeys.ContentHost},
		{"CONTENT_API_SERVICE_PORT", configMap.GRPCKeys.ContentPort},
	}

	var envVarArray corev1.EnvVarArray
	for _, envVar := range envVars {
		envVarArray = append(envVarArray, &corev1.EnvVarArgs{
			Name: pulumi.String(envVar.name),
			ValueFrom: &corev1.EnvVarSourceArgs{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelectorArgs{
					Name: pulumi.String(configMap.Name),
					Key:  pulumi.String(envVar.key),
				},
			},
		})
	}
	return envVarArray
}

func (gs *GraphqlServer) ContainerEnvArgsArray() corev1.EnvVarArray {
	var envVarArray corev1.EnvVarArray
	envVarArray = append(envVarArray, gs.ConfigMapEnvArgsArray()...)
	envVarArray = append(envVarArray, gs.SecretEnvArgsArray()...)
	return envVarArray
}

func (gs *GraphqlServer) ContainerPortArray() corev1.ContainerPortArray {
	config := gs.Config
	return corev1.ContainerPortArray{
		&corev1.ContainerPortArgs{
			Name:          pulumi.String(fmt.Sprintf("%s-api", config.Name)),
			ContainerPort: pulumi.Int(config.Port),
			Protocol:      pulumi.String("TCP"),
		},
	}
}

func (gs *GraphqlServer) allowedOriginsFlags() pulumi.StringArray {
	origins := gs.Config.AllowedOrigins
	var originFlagArray []string
	for _, origin := range origins {
		originFlagArray = append(originFlagArray, "--allowed-origin", origin)
	}
	return pulumi.ToStringArray(originFlagArray)
}

func (gs *GraphqlServer) ContainerArgs() pulumi.StringArray {
	config := gs.Config
	args := pulumi.StringArray{
		pulumi.String("--log-level"),
		pulumi.String(config.LogLevel),
    pulumi.String("start-server"),
		pulumi.String("--s3-bucket"),
		pulumi.String(config.S3Bucket.Name),
		pulumi.String("--s3-bucket-path"),
		pulumi.String(config.S3Bucket.Path),
	}
	return append(args, gs.allowedOriginsFlags()...)
}

func (gs *GraphqlServer) ContainerArray() corev1.ContainerArray {
	config := gs.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name: pulumi.String(fmt.Sprintf("%s-container", config.Name)),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag),
			),
			Args:  gs.ContainerArgs(),
      Env:   gs.ContainerEnvArgsArray(),
			Ports: gs.ContainerPortArray(),
		},
	}
}
