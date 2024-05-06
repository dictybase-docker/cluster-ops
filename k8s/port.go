package k8s

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func ContainerPortSpec(port int, service string) corev1.ContainerPortArray {
	return corev1.ContainerPortArray{corev1.ContainerPortArgs{
		Name:          pulumi.String(service),
		Protocol:      pulumi.String("TCP"),
		ContainerPort: pulumi.Int(port),
	}}
}
