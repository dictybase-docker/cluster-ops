package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
)

func (lt *Logto) CreatePersistentVolumeClaim(ctx *pulumi.Context) (*corev1.PersistentVolumeClaim, error) {
  pvc, err := corev1.NewPersistentVolumeClaim(ctx, "", &corev1.PersistentVolumeClaimArgs{
    Metadata: &metav1.ObjectMetaArgs{
      Name: pulumi.String("logto-claim"),
      Namespace: pulumi.String(lt.Config.Namespace),
    },
  })

  return pvc, nil
}


