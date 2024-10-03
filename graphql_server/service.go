package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (gs *GraphqlServer) CreateService(
	ctx *pulumi.Context,
	deployment pulumi.Resource,
) (*corev1.Service, error) {
	config := gs.Config
	serviceName := fmt.Sprintf("%s-api", config.Name)
	deploymentName := fmt.Sprintf("%s-api-server", config.Name)

	service, err := corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: gs.CreateServiceMetaData(),
		Spec:     gs.CreateServiceSpec(deploymentName),
	}, pulumi.DependsOn([]pulumi.Resource{deployment}))

	if err != nil {
		return nil, fmt.Errorf(
			"error creating graphql-server service: %w",
			err,
		)
	}

	return service, nil
}

func (gs *GraphqlServer) CreateServiceMetaData() *metav1.ObjectMetaArgs {
	config := gs.Config
	serviceName := fmt.Sprintf("%s-api", config.Name)
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(serviceName),
		Namespace: pulumi.String(config.Namespace),
	}
}

func (gs *GraphqlServer) CreateServiceSpec(
	deploymentName string,
) *corev1.ServiceSpecArgs {
	config := gs.Config
	return &corev1.ServiceSpecArgs{
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
	}
}
