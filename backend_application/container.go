package main

import (
	"strconv"

	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func containerSpec(args *specProperties) corev1.ContainerArray {
	return []corev1.ContainerInput{corev1.ContainerArgs{
		Name:  k8s.Container(args.appName),
		Image: k8s.Image(args.app.Image, args.app.Tag),
		Args:  containerArgs(args.cfg.Get("log-level"), args.app.Port),
		Env:   containerEnvSpec(args.cfg.Require("secret")),
		Ports: containerPortSpec(args.app.Port, args.serviceName),
	}}
}

func containerEnvSpec(secret string) corev1.EnvVarArray {
	return corev1.EnvVarArray{
		createEnvVar("ARANGODB_PASSWORD", "arangodb.password", secret),
		createEnvVar("ARANGODB_USER", "arangodb.user", secret),
	}
}

func createEnvVar(name, key, secret string) corev1.EnvVarArgs {
	return corev1.EnvVarArgs{
		Name: pulumi.String(name),
		ValueFrom: corev1.EnvVarSourceArgs{
			SecretKeyRef: createSecretKeySelector(key, secret),
		},
	}
}

func containerArgs(log string, port int) pulumi.StringArrayInput {
	return pulumi.ToStringArray(
		[]string{
			"--log-level",
			log,
			"start-server",
			"--user",
			"$(ARANGODB_USER)",
			"--pass",
			"$(ARANGODB_PASSWORD)",
			"--port",
			strconv.Itoa(port),
		})
}

func createSecretKeySelector(key, secret string) corev1.SecretKeySelectorArgs {
	return corev1.SecretKeySelectorArgs{
		Name: pulumi.StringPtr(secret),
		Key:  pulumi.String(key),
	}
}

func containerPortSpec(port int, service string) corev1.ContainerPortArray {
	return corev1.ContainerPortArray{corev1.ContainerPortArgs{
		Name:          pulumi.String(service),
		Protocol:      pulumi.String("TCP"),
		ContainerPort: pulumi.Int(port),
	}}
}
