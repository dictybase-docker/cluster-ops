package main

import (
  "fmt"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) CreateDeployment(ctx *pulumi.Context) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(ctx, lt.Config.Name, &appsv1.DeploymentArgs{
		Metadata: lt.CreateDeploymentMetadata(),
	})

  if err != nil {
    return nil, fmt.Errorf("error creating %s deployment: %w", lt.Config.Name, err)
  }

	return deployment, nil
}

func (lt *Logto) CreateDeploymentMetadata() (*metav1.ObjectMetaArgs) {
  return &metav1.ObjectMetaArgs{
			Name:      pulumi.String(lt.Config.Name),
			Namespace: pulumi.String(lt.Config.Namespace),
  }
}

func (lt *Logto) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(lt.Config.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(lt.Config.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				// Containers: lt.ContainerArray(),
			},
		},
	}
}

