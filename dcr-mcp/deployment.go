package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const appLabel = "app"

func (m *DcrMcp) CreateDeploymentMetaData() *metav1.ObjectMetaArgs {
	cfg := m.Config
	deploymentName := fmt.Sprintf("%s-api-server", cfg.Name)
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(deploymentName),
		Namespace: pulumi.String(cfg.Namespace),
	}
}

func (m *DcrMcp) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	cfg := m.Config
	deploymentName := fmt.Sprintf("%s-api-server", cfg.Name)
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				appLabel: pulumi.String(deploymentName),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					appLabel: pulumi.String(deploymentName),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: m.ContainerArray(),
			},
		},
	}
}

func (m *DcrMcp) CreateDeployment(
	ctx *pulumi.Context,
) (*appsv1.Deployment, error) {
	cfg := m.Config
	deployment, err := appsv1.NewDeployment(
		ctx,
		fmt.Sprintf("%s-api-server", cfg.Name),
		&appsv1.DeploymentArgs{
			Metadata: m.CreateDeploymentMetaData(),
			Spec:     m.CreateDeploymentSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating dcr-mcp deployment: %w", err)
	}
	return deployment, nil
}
