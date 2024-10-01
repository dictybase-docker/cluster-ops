package main

import (
  "fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) SecretEnvArgsArray() corev1.EnvVarArray {
  envVars := []struct {
    name string
    secret SecretKeyPair
  }{
    {"SECRET_KEY", gs.Config.MinioSecret},
    {"ACCESS_KEY", gs.Config.MinioAccess},
    {"JWT_AUDIENCE", gs.Config.JwtAudience},
    {"JWT_ISSUER", gs.Config.JwtIssuer},
    {"JWKS_PUBLIC_URI", gs.Config.JwksURI},
    {"APPLICATION_SECRET", gs.Config.AuthAppSecret},
    {"APPLICATION_ID", gs.Config.AuthAppId},
  }

  var envVarArray corev1.EnvVarArray
  for _, envVar := range envVars {
    envVarArray = append(envVarArray, &corev1.EnvVarArgs{
      Name: pulumi.String(envVar.name),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(envVar.secret.name),
          Key:  pulumi.String(envVar.secret.key),
        },
      },
    })
  }
  return envVarArray
}

func (gs *GraphqlServer) ConfigMapEnvArgsArray() corev1.EnvVarArray {
	envVars := []struct {
		name string
		configMap ConfigMapPair
	}{
		{"PUBLICATION_API_ENDPOINT", gs.Config.PublicationApiEndpoint},
		{"S3_STORAGE_ENDPOINT", gs.Config.S3StorageEndpoint},
		{"AUTH_ENDPOINT", gs.Config.AuthEndpoint},
		{"ORGANISM_API_ENDPOINT", gs.Config.OrganismEndpoint},
	}

	var envVarArray corev1.EnvVarArray
	for _, envVar := range envVars {
		envVarArray = append(envVarArray, &corev1.EnvVarArgs{
			Name: pulumi.String(envVar.name),
			ValueFrom: &corev1.EnvVarSourceArgs{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelectorArgs{
					Name: pulumi.String(envVar.configMap.name),
					Key:  pulumi.String(envVar.configMap.key),
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
		pulumi.String("start-server"),
		pulumi.String("--log-level"),
		pulumi.String(config.LogLevel),
		pulumi.String("--s3-bucket"),
		pulumi.String(config.S3Bucket),
		pulumi.String("--s3-bucket-path"),
		pulumi.String(config.S3BucketPath),
	}
	return append(args, gs.allowedOriginsFlags()...)
}

func (gs *GraphqlServer) ContainerArray() corev1.ContainerArray {
	config := gs.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name:  pulumi.String(fmt.Sprintf("%s-container", config.Name)),
			Image: pulumi.String(fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag)),
			Args:  gs.ContainerArgs(),
			Env:   gs.ContainerEnvArgsArray(),
			Ports: gs.ContainerPortArray(),
		},
	}
}
