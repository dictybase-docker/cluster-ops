package main

import (
	"strconv"

	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func serviceSpecs(args *specProperties) *corev1.ServiceArgs {
	return &corev1.ServiceArgs{
		Metadata: k8s.Metadata(args.cfg, args.serviceName),
		Spec:     serviceSpecArgs(args.deploymentName, args.serviceName),
	}
}

func serviceSpecArgs(
	deploymentName, serviceName string,
) *corev1.ServiceSpecArgs {
	return &corev1.ServiceSpecArgs{
		Selector: pulumi.StringMap{
			"app": pulumi.String(deploymentName),
		},
		Ports: servicePortSpec(serviceName),
		Type:  pulumi.String("NodePort"),
	}
}

func servicePortSpec(serviceName string) corev1.ServicePortArray {
	targetPort, _ := strconv.Atoi(serviceName)
	return corev1.ServicePortArray{
		&corev1.ServicePortArgs{
			Name:       pulumi.String(serviceName),
			Port:       pulumi.Int(80),
			TargetPort: pulumi.Int(targetPort),
		},
	}

}
