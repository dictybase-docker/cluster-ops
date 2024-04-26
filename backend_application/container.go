package main

import (
	"strconv"

	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func containerSpec(
	cfg *config.Config,
	ctx *pulumi.Context,
	service string,
) corev1.ContainerArray {
	return []corev1.ContainerInput{corev1.ContainerArgs{
		Name:  k8s.Container(cfg),
		Image: k8s.Image(cfg),
		Args:  containerArgs(cfg),
		Env:   containerEnvSpec(cfg, ctx),
		Ports: containerPortSpec(cfg, service),
	}}
}

func containerEnvSpec(
	cfg *config.Config,
	ctx *pulumi.Context,
) corev1.EnvVarArray {
	return corev1.EnvVarArray{
		createEnvVar("ARANGODB_PASSWORD", "arangodb.password", cfg, ctx),
		createEnvVar("ARANGODB_USER", "arangodb.user", cfg, ctx),
	}
}

func containerArgs(cfg *config.Config) pulumi.StringArrayInput {
	return pulumi.ToStringArray(
		[]string{
			"--log-level",
			cfg.Get("log-level"),
			"start-server",
			"--user",
			"$(ARGANGODB_USER)",
			"--pass",
			"$(ARANGODB_PASSWORD)",
			"--port",
			strconv.Itoa(cfg.RequireInt("port")),
		})
}

func createEnvVar(
	name, key string,
	cfg *config.Config,
	ctx *pulumi.Context,
) corev1.EnvVarArgs {
	return corev1.EnvVarArgs{
		Name: pulumi.String(name),
		ValueFrom: corev1.EnvVarSourceArgs{
			SecretKeyRef: createSecretKeySelector(key, cfg, ctx),
		},
	}
}

func createSecretKeySelector(
	key string,
	cfg *config.Config,
	ctx *pulumi.Context,
) corev1.SecretKeySelectorArgs {
	return corev1.SecretKeySelectorArgs{
		Name: pulumi.StringPtr(config.Require(ctx, "secret")),
		Key:  pulumi.String(key),
	}
}

func containerPortSpec(cfg *config.Config, service string) corev1.ContainerPortArray {
	return corev1.ContainerPortArray{corev1.ContainerPortArgs{
		Name:          pulumi.String(service),
		Protocol:      pulumi.String("TCP"),
		ContainerPort: pulumi.Int(cfg.RequireInt("port")),
	}}
}
