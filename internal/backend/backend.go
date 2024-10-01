package backend

import (
	"fmt"
	"strconv"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type BackendConfig struct {
	AppName        string
	Namespace      string
	Port           int
	LogLevel       string
	ArangodbSecret struct {
		Name    string
		PassKey string
		UserKey string
	}
	Image struct {
		Name string
		Tag  string
	}
}

type Backend struct {
	Config *BackendConfig
}

func ReadConfig(ctx *pulumi.Context) (*BackendConfig, error) {
	conf := config.New(ctx, "")
	backendConfig := &BackendConfig{}
	if err := conf.TryObject("properties", backendConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read backend config: %w",
			err,
		)
	}
	return backendConfig, nil
}

func NewBackend(config *BackendConfig) *Backend {
	return &Backend{
		Config: config,
	}
}

func (bck *Backend) Install(ctx *pulumi.Context) error {
	deployment, err := bck.createDeployment(ctx)
	if err != nil {
		return err
	}

	if err := bck.createService(ctx, deployment); err != nil {
		return err
	}

	return nil
}

func (bck *Backend) createDeployment(
	ctx *pulumi.Context,
) (*appsv1.Deployment, error) {
	deploymentName := fmt.Sprintf("%s-api-server", bck.Config.AppName)
	labels := bck.createLabels(deploymentName)

	deployment, err := appsv1.NewDeployment(
		ctx,
		deploymentName,
		&appsv1.DeploymentArgs{
			Metadata: bck.createMetadata(deploymentName),
			Spec:     bck.createDeploymentSpec(labels),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating Kubernetes Deployment: %w", err)
	}

	return deployment, nil
}

func (bck *Backend) createLabels(
	deploymentName string,
) pulumi.StringMap {
	return pulumi.StringMap{
		"app": pulumi.String(deploymentName),
	}
}

func (bck *Backend) createMetadata(
	name string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(bck.Config.Namespace),
	}
}

func (bck *Backend) createDeploymentSpec(
	labels pulumi.StringMap,
) *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: labels,
		},
		Replicas: pulumi.Int(1),
		Template: bck.createPodTemplateSpec(labels),
	}
}

func (bck *Backend) createPodTemplateSpec(
	labels pulumi.StringMap,
) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Labels: labels,
		},
		Spec: bck.createPodSpec(),
	}
}

func (bck *Backend) createPodSpec() *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		Containers: bck.createContainers(),
	}
}

func (bck *Backend) createContainers() corev1.ContainerArray {
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name:  pulumi.String(bck.Config.AppName),
			Image: bck.createImageName(),
			Env:   bck.containerEnvSpec(),
			Ports: bck.createContainerPorts(),
			Args:  bck.containerArgs(),
		},
	}
}

func (bck *Backend) containerEnvSpec() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name: pulumi.String("ARANGODB_PASSWORD"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(bck.Config.ArangodbSecret.Name),
					Key:  pulumi.String(bck.Config.ArangodbSecret.PassKey),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("ARANGODB_USER"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(bck.Config.ArangodbSecret.Name),
					Key:  pulumi.String(bck.Config.ArangodbSecret.UserKey),
				},
			},
		},
	}
}

func (bck *Backend) containerArgs() pulumi.StringArrayInput {
	return pulumi.ToStringArray(
		[]string{
			"--log-level",
			bck.Config.LogLevel,
			"start-server",
			"--user",
			"$(ARANGODB_USER)",
			"--pass",
			"$(ARANGODB_PASSWORD)",
			"--port",
			strconv.Itoa(bck.Config.Port),
		})
}

func (bck *Backend) createImageName() pulumi.StringInput {
	return pulumi.Sprintf("%s:%s",
		bck.Config.Image.Name,
		bck.Config.Image.Tag,
	)
}

func (bck *Backend) createContainerPorts() corev1.ContainerPortArray {
	return corev1.ContainerPortArray{
		&corev1.ContainerPortArgs{
			ContainerPort: pulumi.Int(bck.Config.Port),
		},
	}
}

func (bck *Backend) createService(
	ctx *pulumi.Context,
	deployment *appsv1.Deployment,
) error {
	serviceName := fmt.Sprintf("%s-api", bck.Config.AppName)
	deploymentName := fmt.Sprintf("%s-api-server", bck.Config.AppName)

	_, err := corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: bck.createServiceMetadata(serviceName),
		Spec:     bck.createServiceSpec(deploymentName, serviceName),
	}, pulumi.DependsOn([]pulumi.Resource{deployment}))
	if err != nil {
		return fmt.Errorf("error creating Kubernetes Service: %w", err)
	}

	return nil
}

func (bck *Backend) createServiceMetadata(
	name string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(bck.Config.Namespace),
	}
}

func (bck *Backend) createServiceSpec(
	deploymentName, serviceName string,
) *corev1.ServiceSpecArgs {
	return &corev1.ServiceSpecArgs{
		Selector: bck.createLabels(deploymentName),
		Ports:    bck.createServicePorts(serviceName),
		Type:     pulumi.String("NodePort"),
	}
}

func (bck *Backend) createServicePorts(
	serviceName string,
) corev1.ServicePortArray {
	return corev1.ServicePortArray{
		&corev1.ServicePortArgs{
			Name:       pulumi.String(serviceName),
			Port:       pulumi.Int(bck.Config.Port),
			TargetPort: pulumi.String(serviceName),
		},
	}
}
