package main

import (
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (eme *EventMessengerEmail) CreateDeployment(ctx *pulumi.Context) error {
	_, err := appsv1.NewDeployment(ctx, "event-messenger-email", &appsv1.DeploymentArgs{
		Metadata: eme.CreateDeploymentMetadata(),
		Spec:     eme.CreateDeploymentSpec(),
	})
	return err
}

func (eme *EventMessengerEmail) CreateDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Namespace: pulumi.String(eme.Config.Namespace),
		Name:      pulumi.String("event-messenger-email"),
	}
}

func (eme *EventMessengerEmail) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Replicas: pulumi.Int(eme.Config.Replicas),
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String("event-messenger-email"),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String("event-messenger-email"),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: corev1.ContainerArray{
					&corev1.ContainerArgs{
						Name:  pulumi.String("event-messenger-email"),
						Image: pulumi.String(eme.Config.Image),
						Env: eme.ContainerEnvArgsArray(),
					},
				},
			},
		},
	}
}
