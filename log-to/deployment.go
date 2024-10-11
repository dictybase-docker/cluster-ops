package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) CreateDeployment(
	ctx *pulumi.Context,
	claimName pulumi.StringInput,
	dbSecretName string,
	pvc *corev1.PersistentVolumeClaim,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		lt.Config.Name,
		&appsv1.DeploymentArgs{
			Metadata: lt.CreateDeploymentMetadata(),
			Spec:     lt.CreateDeploymentSpec(claimName, dbSecretName),
		},
		pulumi.DependsOn([]pulumi.Resource{pvc}),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			lt.Config.Name,
			err,
		)
	}

	return deployment, nil
}

func (lt *Logto) CreateDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(lt.Config.Name),
		Namespace: pulumi.String(lt.Config.Namespace),
	}
}

func (lt *Logto) CreateDeploymentSpec(
	claimName pulumi.StringInput,
	dbSecretName string,
) *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(lt.Config.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(lt.Config.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: lt.ContainerArray(dbSecretName),
				Volumes: corev1.VolumeArray{
					&corev1.VolumeArgs{
						Name: pulumi.String(
							fmt.Sprintf("%s-volume", lt.Config.Name),
						),
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSourceArgs{
							ClaimName: claimName,
						},
					},
					&corev1.VolumeArgs{
						Name: pulumi.String("db-secret"),
						Secret: &corev1.SecretVolumeSourceArgs{
							SecretName: pulumi.String(dbSecretName),
						},
					},
				},
			},
		},
	}
}
