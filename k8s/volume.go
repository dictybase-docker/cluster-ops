package k8s

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func CreateContainerVolumeMount(
	volume, mountPath string,
) corev1.VolumeMountArgs {
	return corev1.VolumeMountArgs{
		Name:      pulumi.String(volume),
		MountPath: pulumi.String(mountPath),
	}
}
