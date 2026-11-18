package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CreateStatePVC creates the ReadWriteOnce PVC for /app/data/state.
// The size comes from storage.size in the stack file.
func (f *Filestash) CreateStatePVC(ctx *pulumi.Context) (*corev1.PersistentVolumeClaim, error) {
	cfg := f.Config
	name := f.resourceName("state")
	pvc, err := corev1.NewPersistentVolumeClaim(ctx, name, &corev1.PersistentVolumeClaimArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(cfg.Namespace),
			// The filestash-backup stack backs up this state with restic, so
			// Velero skips it like the other datastores.
			Labels: pulumi.StringMap{
				nameLabel:                       pulumi.String(cfg.Name),
				"velero.io/exclude-from-backup": pulumi.String("true"),
			},
		},
		Spec: &corev1.PersistentVolumeClaimSpecArgs{
			AccessModes:      pulumi.StringArray{pulumi.String("ReadWriteOnce")},
			StorageClassName: pulumi.String(cfg.Storage.StorageClass),
			Resources: &corev1.VolumeResourceRequirementsArgs{
				Requests: pulumi.StringMap{"storage": pulumi.String(cfg.Storage.Size)},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating filestash state pvc: %w", err)
	}
	return pvc, nil
}
