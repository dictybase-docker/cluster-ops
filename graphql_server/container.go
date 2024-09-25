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

func ContainerEnvArgsArray(configMapName string, secretName string) corev1.EnvVarArray {
  return corev1.EnvVarArray{
    &corev1.EnvVarArgs{
      Name: pulumi.String("SECRET_KEY"),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String("minio.secretkey"),
        },
      },
    },
    &corev1.EnvVarArgs{
      Name: pulumi.String("ACCESS_KEY"),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String("minio.accesskey"),
        },
      },
    },
    &corev1.EnvVarArgs{
      Name: pulumi.String("JWT_AUDIENCE"),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String("auth.JwtAudience"),
        },
      },
    },
    &corev1.EnvVarArgs{
      Name: pulumi.String("JWT_ISSUER"),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String("auth.JwtIssuer"),
        },
      },
    },
    &corev1.EnvVarArgs{
      Name: pulumi.String("JWKS_PUBLIC_URI"),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String("auth.JwksURI"),
        },
      },
    },
    &corev1.EnvVarArgs{
      Name: pulumi.String("APPLICATION_SECRET"),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String("auth.appSecret"),
        },
      },
    },
    &corev1.EnvVarArgs{
      Name: pulumi.String("APPLICATION_ID"),
      ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
          Name: pulumi.String(secretName),
          Key:  pulumi.String("auth.appId"),
        },
      },
    },
  }
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
