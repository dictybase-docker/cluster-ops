package main

import (
	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
)

func serviceSpecs(args *specProperties) *corev1.ServiceArgs {
	return &corev1.ServiceArgs{
		Metadata: k8s.Metadata(args.namespace, args.serviceName),
		Spec: k8s.ServiceSpecArgs(
			args.deploymentName,
			args.serviceName,
			args.app.Port,
		),
	}
}
