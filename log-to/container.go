package main

import (
  "fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) ContainerArray() corev1.ContainerArray {
	config := lt.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name:         pulumi.String(fmt.Sprintf("%s-container", config.Name)),
			Image:        pulumi.String(fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag)),
			Command:      pulumi.StringArray{pulumi.String("/bin/sh")},
			Args:         lt.ContainerArgs(),
			Env:          lt.ContainerEnvArgsArray(),
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

func (lt *Logto) ContainerEnvArgsArray() corev1.EnvVarArray {
	config := lt.Config
	dbURL := fmt.Sprintf("postgresql://%s@%s:%d/%s?sslmode=no-verify",
		config.Database.User,
		config.Database.Host,
		config.Database.Port,
		config.Database.Name,
  )

	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name:  pulumi.String("DB_URL"),
			Value: pulumi.String(dbURL),
		},
		&corev1.EnvVarArgs{
			Name:  pulumi.String("PGPASSWORD"),
			Value: pulumi.String(config.Database.Password),
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
}

func (lt *Logto) ContainerPortArray() corev1.ContainerPortArray {
	config := lt.Config
	return corev1.ContainerPortArray{
		&corev1.ContainerPortArgs{
			Name:          pulumi.String(fmt.Sprintf("%s-api", config.Name)),
			ContainerPort: pulumi.Int(config.ApiPort),
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

