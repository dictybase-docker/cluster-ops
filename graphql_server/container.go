package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) ContainerEnvArgsArray() corev1.EnvVarArray {
	secrets := gs.Config.Secrets
	envVars := []struct {
		name   string
		secret string
	}{
		{"SECRET_KEY", secrets.Minio.PassKey},
		{"ACCESS_KEY", secrets.Minio.UserKey},
	}

	var envVarArray corev1.EnvVarArray
	for _, envVar := range envVars {
		envVarArray = append(envVarArray, &corev1.EnvVarArgs{
			Name: pulumi.String(envVar.name),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(secrets.Minio.Name),
					Key:  pulumi.String(envVar.secret),
				},
			},
		})
	}
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
		pulumi.String("--publication-api"),
		pulumi.String(config.Endpoints.Publication),
		pulumi.String("--s3-storage-api"),
		pulumi.String(config.Endpoints.Store),
		pulumi.String("--organism-api"),
		pulumi.String(config.Endpoints.Organism),
	}
	args = append(args, gs.authFlags()...)
	return append(args, gs.allowedOriginsFlags()...)
}

// authFlags emits the logto authentication arguments. The boolean is passed
// as a single --auth-enabled=<value> argument (urfave/cli v1 treats a
// space-separated value as a positional argument). Passing it explicitly in
// both modes means an older image without the flag fails loudly on startup
// instead of silently booting with the wrong auth state.
func (gs *GraphqlServer) authFlags() pulumi.StringArray {
	config := gs.Config
	if !config.AuthEnabled {
		return pulumi.StringArray{
			pulumi.String("--auth-enabled=false"),
		}
	}
	return pulumi.StringArray{
		pulumi.String("--auth-enabled=true"),
		pulumi.String("--auth-api-endpoint"),
		pulumi.String(config.Endpoints.Auth),
		pulumi.String("--app-id"),
		pulumi.String(config.Secrets.Auth.AppId),
		pulumi.String("--app-secret"),
		pulumi.String(config.Secrets.Auth.AppSecret),
		pulumi.String("--jwks-uri"),
		pulumi.String(config.Secrets.Auth.JwksURI),
		pulumi.String("--jwt-issuer"),
		pulumi.String(config.Secrets.Auth.JwtIssuer),
		pulumi.String("--jwt-audience"),
		pulumi.String(config.Secrets.Auth.JwtAudience),
	}
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
