package main

import (
	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func containerVolumeSpec(
	volumeName string,
	mountPath string,
) corev1.VolumeMountArray {
	return corev1.VolumeMountArray{
		k8s.CreateContainerVolumeMount(volumeName, mountPath),
	}
}

func containerEnvSpec(secret string) corev1.EnvVarArray {
	return corev1.EnvVarArray{
		k8s.CreateEnvVarWithSecret(
			"RESTIC_PASSWORD",
			"restic.password",
			secret,
		),
		k8s.CreateEnvVarWithSecret("GOOGLE_PROJECT_ID", "gcs.project", secret),
		k8s.CreateEnvVar(
			"GOOGLE_APPLICATION_CREDENTIALS",
			"/var/secret/credentials.json",
		),
	}
}

func containerSpec(args *specProperties) corev1.ContainerArray {
	return []corev1.ContainerInput{corev1.ContainerArgs{
		Name:         k8s.Container(args.jobName),
		Image:        k8s.Image(args.app.Image, args.app.Tag),
		VolumeMounts: containerVolumeSpec(args.volumeName, "/var/secret"),
		Env:          containerEnvSpec(args.secretName),
		Args:         containerCommand(),
	}}
}

func containerCommand() pulumi.StringArrayInput {
	return pulumi.ToStringArray(
		[]string{"restic", "snapshots", "||", "restic", "init"},
	)
}
