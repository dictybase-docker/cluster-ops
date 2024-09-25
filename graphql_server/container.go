package main

import (
  "fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type ContainerConfig struct {
  name string
  image string
  tag string
  logLevel string
  configMapName string
  secretName string
  port int
  allowedOrigins []string
}

func SecretEnvArgsArray(secretName string) corev1.EnvVarArray {
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

func ConfigMapEnvArgsArray(configMapName string) corev1.EnvVarArray {
  envVars := []struct {
    name string
    key  string
  }{
    {"PUBLICATION_API_ENDPOINT", "endpoint.publication"},
    {"S3_STORAGE_ENDPOINT", "endpoint.storage"},
    {"AUTH_ENDPOINT", "auth.endpoint"},
    {"ORGANISM_API_ENDPOINT", "endpoint.organism"},
  }

  var envVarArray corev1.EnvVarArray
  for _, envVar := range envVars {
    envVarArray = append(envVarArray, &corev1.EnvVarArgs{
      Name: pulumi.String(envVar.name),
      ValueFrom: &corev1.EnvVarSourceArgs{
        ConfigMapKeyRef: &corev1.ConfigMapKeySelectorArgs{
          Name: pulumi.String(configMapName),
          Key:  pulumi.String(envVar.key),
        },
      },
    })
  }
  return envVarArray
}


func ContainerEnvArgsArray(configMapName string, secretName string) corev1.EnvVarArray {
  var envVarArray corev1.EnvVarArray
  envVarArray = append(envVarArray, ConfigMapEnvArgsArray(configMapName)...)
  envVarArray = append(envVarArray, SecretEnvArgsArray(secretName)...)
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

func allowedOriginsFlags(origins []string) pulumi.StringArray {
  var originFlagArray []string
  for _, origin := range origins {
    originFlagArray = append(originFlagArray, "--allowed-origin", origin)
  }
  return pulumi.ToStringArray(originFlagArray)
}

func containerArgs(logLevel string, origins []string) pulumi.StringArray {
  args := pulumi.StringArray{
    pulumi.String("start-server"),
    pulumi.String("--log-level"),
    pulumi.String(logLevel),
  }
  return append(args, allowedOriginsFlags(origins)...)
}

func containerArray(config *ContainerConfig) corev1.ContainerArray {
  return corev1.ContainerArray{
    &corev1.ContainerArgs{
      Name:  pulumi.String(fmt.Sprintf("%s-container", config.name)),
      Image: pulumi.String(fmt.Sprintf("%s:%s", config.image, config.tag)),
      Args: containerArgs(config.logLevel, config.allowedOrigins),
      Env: ContainerEnvArgsArray(config.configMapName, config.secretName),
      Ports: ContainerPortArray(config.name, config.port),
    },
  }
}
