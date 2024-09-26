package main

import (
	"fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) CreateService(ctx *pulumi.Context, deployment pulumi.Resource) error {
	config := gs.Config
	serviceName := fmt.Sprintf("%s-api", config.Name)
	deploymentName := fmt.Sprintf("%s-api-server", config.Name)

	_, err := corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(serviceName),
			Namespace: pulumi.String(config.Namespace),
		},
		Spec: &corev1.ServiceSpecArgs{
			Selector: pulumi.StringMap{
				"app": pulumi.String(deploymentName),
			},
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Port:       pulumi.Int(config.Port),
					TargetPort: pulumi.Int(config.Port),
				},
			},
			Type: pulumi.String("NodePort"),
		},
	}, pulumi.DependsOn([]pulumi.Resource{deployment}))

	if err != nil {
		return err
	}

	return nil
}

