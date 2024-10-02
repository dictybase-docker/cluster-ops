package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (emn *EventMessenger) IssueContainerEnvArgsArray() corev1.EnvVarArray {
	secrets := emn.Config.IssueDeployment.Secrets
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

func (emn *EventMessenger) IssueContainerArgs() pulumi.StringArray {
	args := []string{
		"gh-issue",
		"--log-level",
		emn.Config.LogLevel,
		"--subject",
		emn.Config.Nats.Subject,
		"--token",
		"$(GITHUB_TOKEN)",
		"--repository",
		"$(GITHUB_REPOSITORY)",
		"--owner",
		"$(GITHUB_OWNER)",
	}
	return pulumi.ToStringArray(args)
}

func (emn *EventMessenger) IssueContainerArray() corev1.ContainerArray {
	config := emn.Config
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name: pulumi.String(config.IssueDeployment.Name),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", config.Image.Name, config.Image.Tag),
			),
			ImagePullPolicy: pulumi.String(config.Image.PullPolicy),
			Args:            emn.IssueContainerArgs(),
			Env:             emn.IssueContainerEnvArgsArray(),
		},
	}
}

func (emn *EventMessenger) CreateIssueDeployment(
	ctx *pulumi.Context,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		emn.Config.IssueDeployment.Name,
		&appsv1.DeploymentArgs{
			Metadata: emn.CreateIssueDeploymentMetadata(),
			Spec:     emn.CreateIssueDeploymentSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			emn.Config.IssueDeployment.Name,
			err,
		)
	}
	return deployment, nil
}

func (emn *EventMessenger) CreateIssueDeploymentMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Namespace: pulumi.String(emn.Config.Namespace),
		Name:      pulumi.String(emn.Config.IssueDeployment.Name),
		Labels: pulumi.StringMap{
			"app": pulumi.String(emn.Config.IssueDeployment.Name),
		},
	}
}

func (emn *EventMessenger) CreateIssueDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: pulumi.StringMap{
				"app": pulumi.String(emn.Config.IssueDeployment.Name),
			},
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: pulumi.StringMap{
					"app": pulumi.String(emn.Config.IssueDeployment.Name),
				},
			},
			Spec: &corev1.PodSpecArgs{
				Containers: emn.IssueContainerArray(),
			},
		},
	}
}
