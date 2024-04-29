package main

import (
	"github.com/dictybase-docker/cluster-ops/k8s"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
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
		Selector: deploymentLabelSelector(args.deploymentName),
		Template: deploymentPodTemplate(args),
		Replicas: pulumi.Int(1),
	}

}

func deploymentLabelSelector(deploymentName string) *metav1.LabelSelectorArgs {
	return &metav1.LabelSelectorArgs{
		MatchLabels: pulumi.StringMap{
			"app": pulumi.String(deploymentName),
		}}
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
