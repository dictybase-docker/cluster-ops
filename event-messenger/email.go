package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (emn *EventMessenger) CreateEmailSecret(
	ctx *pulumi.Context,
) (*corev1.Secret, error) {
	config := emn.Config.EmailDeployment.Secrets
	secretData := pulumi.StringMap{
		config.Keys.Cc:     pulumi.String(config.Values.Cc),
		config.Keys.Domain: pulumi.String(config.Values.Domain),
		config.Keys.MailgunAPIKey: pulumi.String(
			config.Values.MailgunAPIKey,
		),
		config.Keys.PublicationAPIEndpoint: pulumi.String(
			config.Values.PublicationAPIEndpoint,
		),
		config.Keys.Sender: pulumi.String(config.Values.Sender),
		config.Keys.SenderName: pulumi.String(
			config.Values.SenderName,
		),
	}

	secret, err := corev1.NewSecret(ctx, config.Name, &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(config.Name),
			Namespace: pulumi.String(emn.Config.Namespace),
		},
		StringData: secretData,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating email secret: %w", err)
	}

	return secret, nil
}

func (emn *EventMessenger) EmailContainerEnvArgsArray() corev1.EnvVarArray {
	secrets := emn.Config.EmailDeployment.Secrets
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

func (emn *EventMessenger) EmailContainerArgs() pulumi.StringArray {
	args := []string{
		"--log-level",
		emn.Config.LogLevel,
		"send-email",
		"--subject",
		emn.Config.Nats.Subject,
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

func (emn *EventMessenger) EmailContainerArray() corev1.ContainerArray {
	config := emn.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name: pulumi.String(config.EmailDeployment.Name),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag),
			),
			Args: emn.EmailContainerArgs(),
			Env:  emn.EmailContainerEnvArgsArray(),
		},
	}
}

func (emn *EventMessenger) CreateEmailDeployment(
	ctx *pulumi.Context,
	emailSecret *corev1.Secret,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		emn.Config.EmailDeployment.Name,
		&appsv1.DeploymentArgs{
			Metadata: emn.CreateEmailDeploymentMetadata(),
			Spec:     emn.CreateEmailDeploymentSpec(),
		},
		pulumi.DependsOn([]pulumi.Resource{emailSecret}),
		pulumi.Timeouts(&pulumi.CustomTimeouts{Create: "3m"}),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			emn.Config.EmailDeployment.Name,
			err,
		)
	}
	return deployment, nil
}

func (emn *EventMessenger) CreateEmailDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(emn.Config.EmailDeployment.Name),
		Namespace: pulumi.String(emn.Config.Namespace),
		Labels: pulumi.StringMap{
			"app": pulumi.String(emn.Config.EmailDeployment.Name),
		},
	}
}

func (emn *EventMessenger) CreateEmailDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(emn.Config.EmailDeployment.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(emn.Config.EmailDeployment.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: emn.EmailContainerArray(),
			},
		},
	}
}
