package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type EventMessengerIssueConfig struct {
	LogLevel   string
	Namespace  string
	Nats       NatsProperties
	Image      ImageConfig
	Deployment IssueDeployment
}

type IssueDeployment struct {
	Name    string
	Secrets IssueSecrets
}

type IssueSecrets struct {
	Name string
	Keys IssueSecretKeys
}

type IssueSecretKeys struct {
	Owner      string
	Repository string
	Token      string
}

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

func (emi *EventMessengerIssue) CreateDeployment(
	ctx *pulumi.Context,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		emi.Config.Deployment.Name,
		&appsv1.DeploymentArgs{
			Metadata: emi.CreateDeploymentMetadata(),
			Spec:     emi.CreateDeploymentSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			emi.Config.Deployment.Name,
			err,
		)
	}
	return deployment, nil
}

func (emi *EventMessengerIssue) CreateDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Namespace: pulumi.String(emi.Config.Namespace),
		Name:      pulumi.String(emi.Config.Deployment.Name),
		Labels: pulumi.StringMap{
			"app": pulumi.String(emi.Config.Deployment.Name),
		},
	}
}

func (emi *EventMessengerIssue) CreateDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(emi.Config.Deployment.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(emi.Config.Deployment.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: emi.ContainerArray(),
			},
		},
	}
}
