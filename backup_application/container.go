package main

import (
	"fmt"

	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func createRepoContainerSpec(args *specProperties) corev1.ContainerArray {
	baseArgs := baseContainerSpec(args)
	baseArgs.VolumeMounts = containerVolumeSpec(
		args.app.volumeName,
		"/var/secret",
	)
	baseArgs.Env = containerEnvSpec(args.secretName, args.app.bucket)
	baseArgs.Args = createRepoArgs()
	return []corev1.ContainerInput{baseArgs}
}

func containerVolumeSpec(
	volumeName string,
	mountPath string,
) corev1.VolumeMountArray {
	return corev1.VolumeMountArray{
		k8s.CreateContainerVolumeMount(volumeName, mountPath),
	}
}

func containerEnvSpec(secret, bucket string) corev1.EnvVarArray {
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
		k8s.CreateEnvVar("RESTIC_REPOSITORY", bucket),
	}
}

func baseContainerSpec(args *specProperties) corev1.ContainerArgs {
	return corev1.ContainerArgs{
		Name:    k8s.Container(args.app.jobName),
		Image:   k8s.Image(args.image, args.tag),
		Command: containerCommand(),
	}
}

func containerCommand() pulumi.StringArrayInput {
	return pulumi.ToStringArray([]string{"/bin/sh", "-c"})
}

func createRepoArgs() pulumi.StringArrayInput {
	return pulumi.ToStringArray(
		[]string{"restic snapshots || restic init"},
	)
}

func postgresBackupArgs(database string) pulumi.StringArrayInput {
	return pulumi.ToStringArray(
		[]string{
			fmt.Sprintf(
				"pg_dump -Fc %s | restic --stdin --stdin-filename %s.dump",
				database,
				database,
			),
		},
	)
}
