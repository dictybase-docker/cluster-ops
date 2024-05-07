package main

import (
	"github.com/dictybase-docker/cluster-ops/k8s"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func deploymentSpec(args *specProperties) *appsv1.DeploymentArgs {
	return &appsv1.DeploymentArgs{
		Metadata: k8s.Metadata(args.namespace, args.deploymentName),
		Spec:     deploymentSpecArgs(args),
	}
}

func deploymentSpecArgs(
	args *specProperties,
) *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: k8s.SpecLabelSelector(args.deploymentName),
		Template: deploymentPodTemplate(args),
		Replicas: pulumi.Int(1),
	}
}

func deploymentPodTemplate(
	args *specProperties,
) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Metadata: k8s.TemplateMetadata(args.deploymentName),
		Spec: &corev1.PodSpecArgs{
			Containers: containerSpec(args),
		},
	}
}
