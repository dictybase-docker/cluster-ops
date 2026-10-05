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

type ResourceValues struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type ResourceSpec struct {
	Requests ResourceValues `json:"requests"`
	Limits   ResourceValues `json:"limits"`
}

type BackendConfig struct {
	AppName        string `json:"appName"`
	Namespace      string `json:"namespace"`
	Port           int    `json:"port"`
	LogLevel       string `json:"logLevel"`
	Command        string `json:"command"`
	ArangodbSecret struct {
		Name    string `json:"name"`
		PassKey string `json:"passkey"`
		UserKey string `json:"userkey"`
	} `json:"arangodbSecret"`
	Image struct {
		Name string `json:"name"`
		Tag  string `json:"tag"`
	} `json:"image"`
	// Production fields — optional; nil keeps the historical lab behavior
	// (1 replica, no resource requests/limits, no probes).
	Replicas        *int          `json:"replicas"`
	Resources       *ResourceSpec `json:"resources"`
	GRPCHealthProbe *bool         `json:"grpcHealthProbe"`
}

// replicasValue returns the configured replica count, 1 when unset — the
// historical behavior every lab stack relies on.
func (bck *Backend) replicasValue() int {
	if bck.Config.Replicas == nil {
		return 1
	}
	return *bck.Config.Replicas
}

// containerResources returns the resource requirements when configured,
// nil otherwise.
func (bck *Backend) containerResources() *corev1.ResourceRequirementsArgs {
	if bck.Config.Resources == nil {
		return nil
	}
	return &corev1.ResourceRequirementsArgs{
		Requests: bck.resourceQuantityMap(bck.Config.Resources.Requests),
		Limits:   bck.resourceQuantityMap(bck.Config.Resources.Limits),
	}
}

func (bck *Backend) resourceQuantityMap(
	vals ResourceValues,
) pulumi.StringMap {
	return pulumi.StringMap{
		"cpu":    pulumi.String(vals.CPU),
		"memory": pulumi.String(vals.Memory),
	}
}

// grpcProbes returns readiness and liveness gRPC health probes when enabled,
// nil otherwise.
func (bck *Backend) grpcProbes() (*corev1.ProbeArgs, *corev1.ProbeArgs) {
	if bck.Config.GRPCHealthProbe == nil || !*bck.Config.GRPCHealthProbe {
		return nil, nil
	}
	probe := &corev1.ProbeArgs{
		Grpc: &corev1.GRPCActionArgs{
			Port: pulumi.Int(bck.Config.Port),
		},
	}
	return probe, probe
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
		Replicas: pulumi.Int(bck.replicasValue()),
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
	readiness, liveness := bck.grpcProbes()
	container := &corev1.ContainerArgs{
		Name:  pulumi.String(bck.Config.AppName),
		Image: bck.createImageName(),
		Env:   bck.containerEnvSpec(),
		Ports: bck.createContainerPorts(),
		Args:  bck.containerArgs(),
	}
	if res := bck.containerResources(); res != nil {
		container.Resources = res
	}
	if readiness != nil {
		container.ReadinessProbe = readiness
	}
	if liveness != nil {
		container.LivenessProbe = liveness
	}
	return corev1.ContainerArray{container}
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
			bck.Config.Command,
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
	serviceName := fmt.Sprintf("%s-api", bck.Config.AppName)
	return corev1.ContainerPortArray{
		&corev1.ContainerPortArgs{
			Name:          pulumi.String(serviceName),
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
		Type:     pulumi.String("ClusterIP"),
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
