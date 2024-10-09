package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) CreatePersistentVolumeClaim(
	ctx *pulumi.Context,
) (*corev1.PersistentVolumeClaim, error) {
	pvcName := fmt.Sprintf("%s-claim", lt.Config.Name)
	pvc, err := corev1.NewPersistentVolumeClaim(
		ctx,
		pvcName,
		&corev1.PersistentVolumeClaimArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(pvcName),
				Namespace: pulumi.String(lt.Config.Namespace),
			},
			Spec: &corev1.PersistentVolumeClaimSpecArgs{
				StorageClassName: pulumi.String(lt.Config.StorageClass),
				AccessModes: pulumi.StringArray{
					pulumi.String("ReadWriteOnce"),
				},
				Resources: corev1.ResourceRequirementsArgs{
					Requests: &pulumi.StringMap{
						"storage": pulumi.String(lt.Config.DiskSize),
					},
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s PersistentVolumeClaim: %w",
			lt.Config.Name,
			err,
		)
	}

	return pvc, nil
}
