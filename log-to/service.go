package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

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

func (lt *Logto) CreateService(
	ctx *pulumi.Context,
	appName pulumi.StringInput,
	serviceName string,
	port int,
	deployment *appsv1.Deployment, // Add this parameter
) (*corev1.Service, error) {
	service, err := corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(serviceName),
			Namespace: pulumi.String(lt.Config.Namespace),
		},
		Spec: &corev1.ServiceSpecArgs{
			Selector: pulumi.StringMap{
				"app": appName,
			},
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Port:       pulumi.Int(port),
					TargetPort: pulumi.Int(port),
				},
			},
		},
	}, pulumi.DependsOn([]pulumi.Resource{deployment})) // Add this line
	if err != nil {
		return nil, fmt.Errorf(
			"error creating service %s: %w",
			serviceName,
			err,
		)
	}
	return service, nil
}
