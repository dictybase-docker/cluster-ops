package main

import (
  "fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (eme *EventMessengerEmail) ConfigMapEnvArgsArray() corev1.EnvVarArray {
	envVars := []struct {
		name      string
		configMap ConfigMapEntry
	}{
		{"EMAIL_DOMAIN", eme.Config.Domain},
		{"EMAIL_SENDER_NAME", eme.Config.SenderName},
		{"EMAIL_SENDER", eme.Config.Sender},
		{"EMAIL_CC", eme.Config.Cc},
		{"PUBLICATION_API_ENDPOINT", eme.Config.PublicationApiEndpoint},
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

func (eme *EventMessengerEmail) SecretEnvArgsArray() corev1.EnvVarArray {
  return corev1.EnvVarArray{
    &corev1.EnvVarArgs{
			Name: pulumi.String("MAILGUN_API_KEY"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelectorArgs{
					Name: pulumi.String(eme.Config.MailgunApiKey.Name),
					Key:  pulumi.String(eme.Config.MailgunApiKey.Key),
				},
			},
    },
  }
}

func (eme *EventMessengerEmail) ContainerEnvArgsArray() corev1.EnvVarArray {
	var envVarArray corev1.EnvVarArray
	envVarArray = append(envVarArray, eme.ConfigMapEnvArgsArray()...)
	envVarArray = append(envVarArray, eme.SecretEnvArgsArray()...)
	return envVarArray
}

func (eme *EventMessengerEmail) ContainerArgs() pulumi.StringArray {
  args := []string{
    "send-email",
    "--log-level",
    eme.Config.LogLevel,
    "--subject",
    eme.Config.Nats.Subject,
    "--domain",
    "$(EMAIL_DOMAIN)",
    "--apiKey",
    "$(MAILGUN_API_KEY)",
    "--name",
    "$(EMAIL_SENDER_NAME)",
    "--sender",
    "$(EMAIL_SENDER)",
    "--cc",
    "$(EMAIL_CC)",
    "--pub",
    "$(PUBLICATION_API_ENDPOINT)",
  }
  return pulumi.ToStringArray(args)
}

func (eme *EventMessengerEmail) ContainerArray() corev1.ContainerArray {
	config := eme.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name:  pulumi.String(config.Name),
			Image: pulumi.String(fmt.Sprintf("%s:%s", config.Image.Repository, config.Image.Tag)),
      Args: eme.ContainerArgs(),
			Env:   eme.ContainerEnvArgsArray(),
		},
	}
}

