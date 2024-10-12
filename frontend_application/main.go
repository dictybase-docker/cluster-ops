package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type FrontendConfig struct {
	Namespace string
	Port      int
	LogLevel  string
	Apps      []AppConfig
}

type AppConfig struct {
	Name  string
	Image string
	Tag   string
}

type Frontend struct {
	Config *FrontendConfig
}

func ReadConfig(ctx *pulumi.Context) (*FrontendConfig, error) {
	conf := config.New(ctx, "")
	frontendConfig := &FrontendConfig{}
	if err := conf.TryObject("properties", frontendConfig); err != nil {
		return nil, fmt.Errorf("failed to read frontend config: %w", err)
	}
	return frontendConfig, nil
}

func NewFrontend(config *FrontendConfig) *Frontend {
	return &Frontend{
		Config: config,
	}
}

func (fe *Frontend) Install(ctx *pulumi.Context) error {
	for _, app := range fe.Config.Apps {
		// Create a new variable for each iteration
		appCopy := app
		deploymentName := fmt.Sprintf("%s-api-server", app.Name)
		serviceName := fmt.Sprintf("%s-api", app.Name)

		deployment, err := fe.createDeployment(
			ctx,
			&appCopy, // Use the address of the copy instead of &app
			deploymentName,
			serviceName,
		)
		if err != nil {
			return fmt.Errorf(
				"error creating Deployment for %s: %w",
				app.Name,
				err,
			)
		}

		_, err = fe.createService(
			ctx,
			deploymentName,
			serviceName,
			deployment,
		)
		if err != nil {
			return fmt.Errorf(
				"error creating Service for %s: %w",
				app.Name,
				err,
			)
		}
	}

	return nil
}

func (fe *Frontend) createDeployment(
	ctx *pulumi.Context,
	app *AppConfig,
	deploymentName, serviceName string,
) (*appsv1.Deployment, error) {
	return appsv1.NewDeployment(ctx, deploymentName, &appsv1.DeploymentArgs{
		Metadata: fe.createMetadata(deploymentName),
		Spec:     fe.createDeploymentSpec(app, deploymentName, serviceName),
	})
}

func (fe *Frontend) createService(
	ctx *pulumi.Context,
	deploymentName, serviceName string,
	deployment *appsv1.Deployment,
) (*corev1.Service, error) {
	return corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: fe.createMetadata(serviceName),
		Spec:     fe.createServiceSpec(deploymentName, serviceName),
	}, pulumi.DependsOn([]pulumi.Resource{deployment}))
}

func (fe *Frontend) createMetadata(name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(fe.Config.Namespace),
		Labels:    fe.createLabels(name),
	}
}

func (fe *Frontend) createLabels(name string) pulumi.StringMap {
	return pulumi.StringMap{
		"app": pulumi.String(name),
	}
}

func (fe *Frontend) createDeploymentSpec(
	app *AppConfig,
	deploymentName, serviceName string,
) *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: fe.createLabels(deploymentName),
		},
		Template: fe.createPodTemplateSpec(app, deploymentName, serviceName),
		Replicas: pulumi.Int(1),
	}
}

func (fe *Frontend) createPodTemplateSpec(
	app *AppConfig,
	deploymentName, serviceName string,
) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Labels: fe.createLabels(deploymentName),
		},
		Spec: fe.createPodSpec(app, serviceName),
	}
}

func (fe *Frontend) createPodSpec(
	app *AppConfig,
	serviceName string,
) *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		Containers: fe.createContainers(app, serviceName),
	}
}

func (fe *Frontend) createContainers(
	app *AppConfig,
	serviceName string,
) corev1.ContainerArray {
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name:  pulumi.String(app.Name),
			Image: pulumi.String(fmt.Sprintf("%s:%s", app.Image, app.Tag)),
			Ports: fe.createContainerPorts(serviceName),
		},
	}
}

func (fe *Frontend) createContainerPorts(
	serviceName string,
) corev1.ContainerPortArray {
	return corev1.ContainerPortArray{
		&corev1.ContainerPortArgs{
			Name:          pulumi.String(serviceName),
			ContainerPort: pulumi.Int(fe.Config.Port),
			Protocol:      pulumi.String("TCP"),
		},
	}
}

func (fe *Frontend) createServiceSpec(
	deploymentName string,
	serviceName string,
) *corev1.ServiceSpecArgs {
	return &corev1.ServiceSpecArgs{
		Selector: fe.createLabels(deploymentName),
		Ports: corev1.ServicePortArray{
			&corev1.ServicePortArgs{
				Port:       pulumi.Int(fe.Config.Port),
				TargetPort: pulumi.String(serviceName),
			},
		},
	}
}

func Run(ctx *pulumi.Context) error {
	frontendConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	frontend := NewFrontend(frontendConfig)

	if err := frontend.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
