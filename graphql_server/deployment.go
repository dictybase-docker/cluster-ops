package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) CreateDeploymentMetaData() *metav1.ObjectMetaArgs {
	config := gs.Config
	deploymentName := fmt.Sprintf("%s-api-server", config.Name)
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(deploymentName),
		Namespace: pulumi.String(config.Namespace),
	}
}

func (gs *GraphqlServer) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	config := gs.Config
	deploymentName := fmt.Sprintf("%s-api-server", config.Name)
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(deploymentName),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(deploymentName),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: gs.ContainerArray(),
			},
		},
	}
}

func (gs *GraphqlServer) CreateDeployment(ctx *pulumi.Context) (*appsv1.Deployment, error) {
	config := gs.Config
	deployment, err := appsv1.NewDeployment(
		ctx,
		fmt.Sprintf("%s-api-server", config.Name),
		&appsv1.DeploymentArgs{
			Metadata: gs.CreateDeploymentMetaData(),
			Spec:     gs.CreateDeploymentSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating graphql-server deployment: %w", err)
	}

	return deployment, nil
}
