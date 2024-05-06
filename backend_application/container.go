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
		Env:   k8s.ContainerEnvSpec(args.cfg.Require("secret")),
		Ports: k8s.ContainerPortSpec(args.app.Port, args.serviceName),
		Args:  containerArgs(args.cfg.Get("log-level"), args.app.Port),
	}}
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
