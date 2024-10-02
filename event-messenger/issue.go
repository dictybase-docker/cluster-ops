package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

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

func (emi *EventMessenger) IssueContainerEnvArgsArray() corev1.EnvVarArray {
	secrets := emi.Config.IssueDeployment.Secrets
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

func (emi *EventMessenger) IssueContainerArgs() pulumi.StringArray {
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

func (emi *EventMessenger) IssueContainerArray() corev1.ContainerArray {
	config := emi.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name: pulumi.String(config.IssueDeployment.Name),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag),
			),
			ImagePullPolicy: pulumi.String(config.Image.PullPolicy),
			Args:            emi.IssueContainerArgs(),
			Env:             emi.IssueContainerEnvArgsArray(),
		},
	}
}

func (emi *EventMessenger) CreateIssueDeployment(
	ctx *pulumi.Context,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		emi.Config.IssueDeployment.Name,
		&appsv1.DeploymentArgs{
			Metadata: emi.CreateIssueDeploymentMetadata(),
			Spec:     emi.CreateIssueDeploymentSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			emi.Config.IssueDeployment.Name,
			err,
		)
	}
	return deployment, nil
}

func (emi *EventMessenger) CreateIssueDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Namespace: pulumi.String(emi.Config.Namespace),
		Name:      pulumi.String(emi.Config.IssueDeployment.Name),
		Labels: pulumi.StringMap{
			"app": pulumi.String(emi.Config.IssueDeployment.Name),
		},
	}
}

func (emi *EventMessenger) CreateIssueDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(emi.Config.IssueDeployment.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(emi.Config.IssueDeployment.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: emi.IssueContainerArray(),
			},
		},
	}
}
