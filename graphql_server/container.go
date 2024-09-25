package main

import (
  "fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ContainerArgs struct {
}

type ContainerConfig struct {
  name string
  image string
  tag string
  logLevel string
  secretName string
  port int
}

func ContainerEnvArgsArray(secretName string) corev1.EnvVarArray {
  envVars := []struct {
    name string
    key  string
  }{
    {"SECRET_KEY", "minio.secretkey"},
    {"ACCESS_KEY", "minio.accesskey"},
    {"JWT_AUDIENCE", "auth.JwtAudience"},
    {"JWT_ISSUER", "auth.JwtIssuer"},
    {"JWKS_PUBLIC_URI", "auth.JwksURI"},
    {"APPLICATION_SECRET", "auth.appSecret"},
    {"APPLICATION_ID", "auth.appId"},
  }

  var envVarArray corev1.EnvVarArray
  for _, envVar := range envVars {
    envVarArray = append(envVarArray, &corev1.EnvVarArgs{
      Name: pulumi.String(envVar.name),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String(envVar.key),
        },
      },
    })
  }
  return envVarArray
}

func ContainerPortArray(name string, port int) corev1.ContainerPortArray {
  return corev1.ContainerPortArray{
    &corev1.ContainerPortArgs{
      Name:          pulumi.String(fmt.Sprintf("%s-api", name)),
      ContainerPort: pulumi.Int(port),
      Protocol:      pulumi.String("TCP"),
    },
  }
}

func ContainerArray(config ContainerConfig) corev1.ContainerArray {
  return corev1.ContainerArray{
    &corev1.ContainerArgs{
      Name:  pulumi.String(fmt.Sprintf("%s-container", config.name)),
      Image: pulumi.String(fmt.Sprintf("%s:%s", config.image, config.tag)),
      Args: pulumi.StringArray{
        pulumi.String("--log-level"),
        pulumi.String(config.logLevel),
        pulumi.String("start-server"),
      },
      Env: ContainerEnvArgsArray(config.secretName),
      Ports: ContainerPortArray(config.name, config.port),
    },
  }
}

func ContainerProperties(args *config.Config) corev1.ContainerArgs {
  return corev1.ContainerArgs{}
}
