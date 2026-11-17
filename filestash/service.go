package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CreateService creates the ClusterIP Service on the Filestash port.
func (f *Filestash) CreateService(ctx *pulumi.Context, deployment pulumi.Resource) error {
	cfg := f.Config
	_, err := corev1.NewService(ctx, f.resourceName("svc"), &corev1.ServiceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(cfg.Name),
			Namespace: pulumi.String(cfg.Namespace),
			Labels:    f.labels(),
		},
		Spec: &corev1.ServiceSpecArgs{
			Type:     pulumi.String("ClusterIP"),
			Selector: f.labels(),
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Name:       pulumi.String("http"),
					Port:       pulumi.Int(cfg.Port),
					TargetPort: pulumi.Int(cfg.Port),
				},
			},
		},
	}, pulumi.DependsOn([]pulumi.Resource{deployment}))
	if err != nil {
		return fmt.Errorf("error creating filestash service: %w", err)
	}
	return nil
}
