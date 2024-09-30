package main

import (
  "fmt"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (emi *EventMessengerIssue) CreateDeployment(ctx *pulumi.Context) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(ctx, "event-messenger-issue", &appsv1.DeploymentArgs{
		Metadata: emi.CreateDeploymentMetadata(),
		Spec:     emi.CreateDeploymentSpec(),
	})
  if err != nil {
    return nil, fmt.Errorf("error creating event-messenger-issue deployment: %w", err)
  }
	return deployment, nil
}

func (emi *EventMessengerIssue) CreateDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Namespace: pulumi.String(emi.Config.Namespace),
		Name:      pulumi.String("event-messenger-issue"),
	}
}

func (emi *EventMessengerIssue) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Replicas: pulumi.Int(emi.Config.Replicas),
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String("event-messenger-issue"),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String("event-messenger-issue"),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: emi.ContainerArray(),
			},
		},
	}
}
