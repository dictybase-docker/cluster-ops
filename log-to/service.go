package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) CreateService(
	ctx *pulumi.Context,
	deploymentName pulumi.StringInput,
	serviceName string,
	port int,
) (*corev1.Service, error) {
	service, err := corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: lt.CreateServiceMetaData(serviceName),
		Spec:     lt.CreateServiceSpec(deploymentName, serviceName, port),
	})

	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s service: %w",
			serviceName,
			err,
		)
	}

	return service, nil
}

func (lt *Logto) CreateServiceMetaData(
	serviceName string,
) *metav1.ObjectMetaArgs {
	config := lt.Config
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(serviceName),
		Namespace: pulumi.String(config.Namespace),
	}
}

func (lt *Logto) CreateServiceSpec(
	deploymentName pulumi.StringInput,
	serviceName string,
	port int,
) *corev1.ServiceSpecArgs {
	return &corev1.ServiceSpecArgs{
		Selector: pulumi.StringMap{
			"app": deploymentName,
		},
		Ports: corev1.ServicePortArray{
			&corev1.ServicePortArgs{
				Name:       pulumi.String(serviceName),
				Port:       pulumi.Int(port),
				TargetPort: pulumi.Int(port),
			},
		},
		Type: pulumi.String("NodePort"),
	}
}
