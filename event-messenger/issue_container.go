package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (emi *EventMessengerIssue) ContainerEnvArgsArray() corev1.EnvVarArray {
	secrets := emi.Config.Deployment.Secrets
	var envVarArray corev1.EnvVarArray

	secretEnvVars := []struct {
		name string
		key  string
	}{
		{"GITHUB_OWNER", secrets.Keys.Owner},
		{"GITHUB_REPOSITORY", secrets.Keys.Repository},
		{"GITHUB_TOKEN", secrets.Keys.Token},
	}

	for _, envVar := range secretEnvVars {
		envVarArray = append(envVarArray, &corev1.EnvVarArgs{
			Name: pulumi.String(envVar.name),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(secrets.Name),
					Key:  pulumi.String(envVar.key),
				},
			},
		})
	}
	return envVarArray
}

func (emi *EventMessengerIssue) ContainerArgs() pulumi.StringArray {
	args := []string{
		"gh-issue",
		"--log-level",
		emi.Config.LogLevel,
		"--subject",
		emi.Config.Nats.Subject,
		"--token",
		"$(GITHUB_TOKEN)",
		"--repository",
		"$(GITHUB_REPOSITORY)",
		"--owner",
		"$(GITHUB_OWNER)",
	}
	return pulumi.ToStringArray(args)
}

func (emi *EventMessengerIssue) ContainerArray() corev1.ContainerArray {
	config := emi.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name: pulumi.String(config.Deployment.Name),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag),
			),
			ImagePullPolicy: pulumi.String(config.Image.PullPolicy),
			Args:            emi.ContainerArgs(),
			Env:             emi.ContainerEnvArgsArray(),
		},
	}
}
