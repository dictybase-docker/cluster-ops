package main

import (
  "fmt"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (eme *EventMessengerEmail) ContainerEnvArgsArray() corev1.EnvVarArray {
	secrets := eme.Config.Deployment.Secrets
	var envVarArray corev1.EnvVarArray
	
	secretEnvVars := []struct {
		name string
		key  string
	}{
		{"EMAIL_DOMAIN", secrets.Keys.Domain},
		{"EMAIL_SENDER_NAME", secrets.Keys.SenderName},
		{"EMAIL_SENDER", secrets.Keys.Sender},
		{"EMAIL_CC", secrets.Keys.Cc},
		{"PUBLICATION_API_ENDPOINT", secrets.Keys.PublicationApiEndpoint},
		{"MAILGUN_API_KEY", secrets.Keys.MailgunApiKey},
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
			Name:            pulumi.String(config.Deployment.Name),
			Image:           pulumi.String(fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag)),
			ImagePullPolicy: pulumi.String(config.Image.PullPolicy),
			Args:            eme.ContainerArgs(),
			Env:             eme.ContainerEnvArgsArray(),
		},
	}
}

