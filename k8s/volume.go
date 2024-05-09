package k8s

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type VolumeItemsProperties struct {
	Key   string
	Value string
}

func CreateContainerVolumeMount(
	volume, mountPath string,
) corev1.VolumeMountArgs {
	return corev1.VolumeMountArgs{
		Name:      pulumi.String(volume),
		MountPath: pulumi.String(mountPath),
	}
}

func CreateVolumeSpec(
	secretName, volumeName string,
	items []*VolumeItemsProperties,
) corev1.VolumeArray {
	return corev1.VolumeArray{
		corev1.VolumeArgs{
			Name: pulumi.String(volumeName),
			Secret: corev1.SecretVolumeSourceArgs{
				SecretName: pulumi.String(secretName),
				Items:      volumeItems(items),
			},
		},
	}
}

func volumeItems(items []*VolumeItemsProperties) corev1.KeyToPathArray {
	keyPathArr := make([]corev1.KeyToPathInput, 0)
	for _, itm := range items {
		keyPathArr = append(
			keyPathArr,
			corev1.KeyToPathArgs{
				Key:  pulumi.String(itm.Key),
				Path: pulumi.String(itm.Value),
			},
		)
	}
	return keyPathArr
}
