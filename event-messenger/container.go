package main

import (
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
					Name: pulumi.String(envVar.configMap.name),
					Key:  pulumi.String(envVar.configMap.key),
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
					Name: pulumi.String(eme.Config.MailgunApiKey.name),
					Key:  pulumi.String(eme.Config.MailgunApiKey.key),
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

