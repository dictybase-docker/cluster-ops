package k8s

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func servicePortSpec(serviceName string, port int) corev1.ServicePortArray {
	return corev1.ServicePortArray{
		&corev1.ServicePortArgs{
			Name:       pulumi.String(serviceName),
			Port:       pulumi.Int(port),
			TargetPort: pulumi.String(serviceName),
		},
	}
}

func ServiceSpecArgs(
	deploymentName, serviceName string,
	port int,
) *corev1.ServiceSpecArgs {
	return &corev1.ServiceSpecArgs{
		Selector: pulumi.StringMap{
			"app": pulumi.String(deploymentName),
		},
		Ports: servicePortSpec(serviceName, port),
		Type:  pulumi.String("NodePort"),
	}
}
