package main

import (
  "fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (emi *EventMessengerIssue) ConfigMapEnvArgsArray() corev1.EnvVarArray {
	envVars := []struct {
		name      string
		configMap ConfigMapPair
	}{
		{"GITHUB_REPOSITORY", emi.Config.GithubRepo},
		{"GITHUB_OWNER", emi.Config.GithubOwner},
	}

	var envVarArray corev1.EnvVarArray
	for _, envVar := range envVars {
		envVarArray = append(envVarArray, &corev1.EnvVarArgs{
			Name: pulumi.String(envVar.name),
			ValueFrom: &corev1.EnvVarSourceArgs{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelectorArgs{
					Name: pulumi.String(envVar.configMap.Name),
					Key:  pulumi.String(envVar.configMap.Key),
				},
			},
		})
	}
	return envVarArray
}

func (emi *EventMessengerIssue) SecretEnvArgsArray() corev1.EnvVarArray {
  return corev1.EnvVarArray{
    &corev1.EnvVarArgs{
			Name: pulumi.String("GITHUB_TOKEN"),
			ValueFrom: &corev1.EnvVarSourceArgs{
        SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(emi.Config.GithubToken.Name),
					Key:  pulumi.String(emi.Config.GithubToken.Key),
				},
			},
    },
  }
}

func (emi *EventMessengerIssue) ContainerEnvArgsArray() corev1.EnvVarArray {
	var envVarArray corev1.EnvVarArray
	envVarArray = append(envVarArray, emi.ConfigMapEnvArgsArray()...)
	envVarArray = append(envVarArray, emi.SecretEnvArgsArray()...)
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
			Name:  pulumi.String(config.Name),
			Image: pulumi.String(fmt.Sprintf("%s:%s", config.Image.Repository, config.Image.Tag)),
      Args: emi.ContainerArgs(),
			Env:   emi.ContainerEnvArgsArray(),
		},
	}
}

