package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (m *DcrMcp) CreateService(
	ctx *pulumi.Context,
	deployment pulumi.Resource,
) (*corev1.Service, error) {
	cfg := m.Config
	serviceName := fmt.Sprintf("%s-api", cfg.Name)
	deploymentName := fmt.Sprintf("%s-api-server", cfg.Name)

	service, err := corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: m.CreateServiceMetaData(),
		Spec:     m.CreateServiceSpec(deploymentName),
	}, pulumi.DependsOn([]pulumi.Resource{deployment}))
	if err != nil {
		return nil, fmt.Errorf("error creating dcr-mcp service: %w", err)
	}
	return service, nil
}

func (m *DcrMcp) CreateServiceMetaData() *metav1.ObjectMetaArgs {
	cfg := m.Config
	serviceName := fmt.Sprintf("%s-api", cfg.Name)
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(serviceName),
		Namespace: pulumi.String(cfg.Namespace),
	}
}

func (m *DcrMcp) CreateServiceSpec(
	deploymentName string,
) *corev1.ServiceSpecArgs {
	cfg := m.Config
	return &corev1.ServiceSpecArgs{
		Selector: pulumi.StringMap{
			appLabel: pulumi.String(deploymentName),
		},
		Ports: corev1.ServicePortArray{
			&corev1.ServicePortArgs{
				Port:       pulumi.Int(cfg.Port),
				TargetPort: pulumi.Int(cfg.Port),
			},
		},
		Type: pulumi.String("NodePort"),
	}
}
