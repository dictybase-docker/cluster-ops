package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) ContainerArray(
	dbSecretName string,
) corev1.ContainerArray {
	config := lt.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name: pulumi.String(
				fmt.Sprintf("%s-container", config.Name),
			),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag),
			),
			Command:      pulumi.StringArray{pulumi.String("/bin/sh")},
			Args:         lt.ContainerArgs(),
			Env:          lt.ContainerEnvArgsArray(dbSecretName),
			Ports:        lt.ContainerPortArray(),
			VolumeMounts: lt.ContainerVolumeMountArray(),
		},
	}
}

func (lt *Logto) ContainerArgs() pulumi.StringArray {
	config := lt.Config
	script := fmt.Sprintf("npm run cli db seed -- --swe && "+
		"npm run cli db alteration deploy %s && "+
		"npm run cli connector link && "+
		"npm start", config.Image.Tag)

	return pulumi.StringArray{
		pulumi.String("-c"),
		pulumi.String(script),
	}
}

func (lt *Logto) ContainerEnvArgsArray(
	dbSecretName string,
) corev1.EnvVarArray {
	config := lt.Config
	envArr := corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name: pulumi.String("PGPASSWORD"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(dbSecretName),
					Key:  pulumi.String("password"),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("DBUSER"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(dbSecretName),
					Key:  pulumi.String("username"),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name:  pulumi.String("ENDPOINT"),
			Value: pulumi.String(config.Endpoint),
		},
		&corev1.EnvVarArgs{
			Name:  pulumi.String("TRUST_PROXY_HEADER"),
			Value: pulumi.String("1"),
		},
	}

	return append(envArr, &corev1.EnvVarArgs{
		Name: pulumi.String("DB_URL"),
		Value: pulumi.String(
			"postgresql://$(DBUSER)@$(LOGTO_RW_SERVICE_HOST):$(LOGTO_RW_SERVICE_PORT)/logto?sslmode=no-verify",
		),
	})
}

func (lt *Logto) ContainerPortArray() corev1.ContainerPortArray {
	config := lt.Config
	return corev1.ContainerPortArray{
		&corev1.ContainerPortArgs{
			Name:          pulumi.String(fmt.Sprintf("%s-api", config.Name)),
			ContainerPort: pulumi.Int(config.APIPort),
			Protocol:      pulumi.String("TCP"),
		},
		&corev1.ContainerPortArgs{
			Name:          pulumi.String(fmt.Sprintf("%s-admin", config.Name)),
			ContainerPort: pulumi.Int(config.AdminPort),
			Protocol:      pulumi.String("TCP"),
		},
	}
}

func (lt *Logto) ContainerVolumeMountArray() corev1.VolumeMountArray {
	return corev1.VolumeMountArray{
		&corev1.VolumeMountArgs{
			Name:      pulumi.String(fmt.Sprintf("%s-volume", lt.Config.Name)),
			MountPath: pulumi.String("/etc/logto/packages/core/connectors"),
		},
	}
}
