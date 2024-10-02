package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type EventMessengerEmail struct {
	Config *EventMessengerEmailConfig
}

// EventMessengerEmailConfig holds the configuration for the event messenger email service
type EventMessengerEmailConfig struct {
	LogLevel   string
	Namespace  string
	Nats       NatsProperties
	Image      ImageConfig
	Deployment EmailDeployment
}

type EmailDeployment struct {
	Name    string
	Secrets EmailSecrets
}

type EmailSecrets struct {
	Name string
	Keys EmailSecretKeys
}

type EmailSecretKeys struct {
	Cc                     string
	Domain                 string
	MailgunAPIKey          string
	PublicationAPIEndpoint string
	Sender                 string
	SenderName             string
}

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
		{"PUBLICATION_API_ENDPOINT", secrets.Keys.PublicationAPIEndpoint},
		{"MAILGUN_API_KEY", secrets.Keys.MailgunAPIKey},
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
			Name: pulumi.String(config.Deployment.Name),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag),
			),
			Args: eme.ContainerArgs(),
			Env:  eme.ContainerEnvArgsArray(),
		},
	}
}

func (eme *EventMessengerEmail) CreateDeployment(
	ctx *pulumi.Context,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		eme.Config.Deployment.Name,
		&appsv1.DeploymentArgs{
			Metadata: eme.CreateDeploymentMetadata(),
			Spec:     eme.CreateDeploymentSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			eme.Config.Deployment.Name,
			err,
		)
	}
	return deployment, nil
}

func (eme *EventMessengerEmail) CreateDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(eme.Config.Deployment.Name),
		Namespace: pulumi.String(eme.Config.Namespace),
		Labels: pulumi.StringMap{
			"app": pulumi.String(eme.Config.Deployment.Name),
		},
	}
}

func (eme *EventMessengerEmail) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(eme.Config.Deployment.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(eme.Config.Deployment.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: eme.ContainerArray(),
			},
		},
	}
}
