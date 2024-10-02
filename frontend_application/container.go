package main

import (
	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func containerSpec(args *specProperties) corev1.ContainerArray {
	return []corev1.ContainerInput{corev1.ContainerArgs{
		Name:  k8s.Container(args.app.Name),
		Image: k8s.Image(args.app.Image, args.app.Tag),
		Ports: containerPortSpec(args.port, args.serviceName),
	}}
}

func containerPortSpec(port int, service string) corev1.ContainerPortArray {
	return corev1.ContainerPortArray{corev1.ContainerPortArgs{
		Name:          pulumi.String(service),
		Protocol:      pulumi.String("TCP"),
		ContainerPort: pulumi.Int(port),
	}}
}
