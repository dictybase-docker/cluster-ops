package main

import (
	"fmt"
	"strings"

	"github.com/dictybase-docker/cluster-ops/k8s"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func createRepoContainerSpec(args *specProperties) corev1.ContainerArray {
	baseArgs := baseContainerSpec(args)
	baseArgs.Env = containerEnvSpec(args.secretName, args.app.bucket)
	baseArgs.Args = createRepoArgs()
	return []corev1.ContainerInput{baseArgs}
}

func postgresBackupContainerSpec(args *specProperties) corev1.ContainerArray {
	baseArgs := baseContainerSpec(args)
	baseArgs.Env = postgresBackupEnvSpec(
		args.secretName,
		args.app.secret,
		args.app.bucket,
	)
	baseArgs.Args = postgresBackupArgs(args.app.databases)
	return []corev1.ContainerInput{baseArgs}
}

func postgresBackupEnvSpec(secret, pgsecret, bucket string) corev1.EnvVarArray {
	envArr := make([]corev1.EnvVarInput, 0)
	for _, key := range []string{"host", "port", "user", "password"} {
		envArr = append(
			envArr,
			k8s.CreateEnvVarWithSecret(
				fmt.Sprintf("PG%s", strings.ToUpper(key)),
				key,
				pgsecret,
			),
		)

	}
	return append(envArr, containerEnvSpec(secret, bucket)...)
}

func postgresBackupArgs(databases []string) pulumi.StringArrayInput {
	dumpCmdTmpl := "pg_dump -Fc %s | restic backup --stdin --tag %s --stdin-filename %s.dump"
	commands := make([]string, 0)
	for _, dbname := range databases {
		commands = append(commands,
			fmt.Sprintf(dumpCmdTmpl, dbname, dbname, dbname),
		)

	}
	return pulumi.StringArray{pulumi.String(strings.Join(commands, ";"))}
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
		VolumeMounts: containerVolumeSpec(
			args.app.volumeName,
			"/var/secret",
		),
	}
}

func containerCommand() pulumi.StringArrayInput {
	return pulumi.StringArray{pulumi.String("/bin/sh"), pulumi.String("-c")}
}

func createRepoArgs() pulumi.StringArrayInput {
	return pulumi.StringArray{
		pulumi.String("restic snapshots || restic init"),
	}
}
