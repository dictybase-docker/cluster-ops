package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (eme *EventMessengerEmail) CreateDeployment(
	ctx *pulumi.Context,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		eme.Config.Deployment.Name,
		&appsv1.DeploymentArgs{
			Metadata: eme.CreateDeploymentMetadata(),
			Spec:     eme.CreateDeploymentSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			eme.Config.Deployment.Name,
			err,
		)
	}
	return deployment, nil
}

func (eme *EventMessengerEmail) CreateDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(eme.Config.Deployment.Name),
		Namespace: pulumi.String(eme.Config.Namespace),
		Labels: pulumi.StringMap{
			"app": pulumi.String(eme.Config.Deployment.Name),
		},
	}
}

func (eme *EventMessengerEmail) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(eme.Config.Deployment.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(eme.Config.Deployment.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: eme.ContainerArray(),
			},
		},
	}
}
