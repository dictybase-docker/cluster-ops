package main

import (
	"fmt"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

var allowedOrigins = []string{
	"http://localhost:*",
	"https://dictybase.org",
	"https://*.dictybase.org",
	"https://dictycr.org",
	"https://*.dictycr.org",
	"https://dictybase.dev",
	"https://*.dictybase.dev",
	"https://dictybase.dev*",
}

type GraphqlServerConfig struct {
	namespace     string
	name          string
	image         string
	tag           string
	logLevel      string
	port          int
	secretName    string
	configMapName string
	s3Bucket      string
	s3BucketPath  string
  allowedOrigins []string
}

type GraphqlServer struct {
  Config *GraphqlServerConfig
}

func Run(ctx *pulumi.Context) error {
  // Load configuration
  config, err := ReadConfig(ctx)
  if err != nil {
    return err
  }

  graphqlServer := NewGraphqlServer(config)

	if err := graphqlServer.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}

func ReadConfig(ctx *pulumi.Context) (*GraphqlServerConfig, error) {
	conf := config.New(ctx, "graphql_server")
	arangoConfig := &GraphqlServerConfig{}
	if err := conf.TryObject("properties", arangoConfig); err != nil {
		return nil, fmt.Errorf("failed to read arangodb config: %w", err)
	}
	return arangoConfig, nil
}

func NewGraphqlServer(config *GraphqlServerConfig) *GraphqlServer {
  return &GraphqlServer{
    Config: config,
  }
}

func (gs *GraphqlServer) Install(ctx *pulumi.Context) error {
	config := gs.Config

	deploymentName := fmt.Sprintf("%s-api-server", config.name)
	serviceName := fmt.Sprintf("%s-api", config.name)

	// Create deployment
	deployment, err := appsv1.NewDeployment(ctx, fmt.Sprintf("%s-api-server", config.name), &appsv1.DeploymentArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(deploymentName),
			Namespace: pulumi.String(config.namespace),
		},
		Spec: &appsv1.DeploymentSpecArgs{
			Selector: &metav1.LabelSelectorArgs{
				MatchLabels: pulumi.StringMap{
					"app": pulumi.String(deploymentName),
				},
			},
			Template: &corev1.PodTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Labels: pulumi.StringMap{
						"app": pulumi.String(deploymentName),
					},
				},
				Spec: &corev1.PodSpecArgs{
					Containers: ContainerArray(&ContainerConfig{
						name:           config.name,
						image:          config.image,
						tag:            config.tag,
						logLevel:       config.logLevel,
						configMapName:  config.logLevel,
						secretName:     config.secretName,
						port:           config.port,
						allowedOrigins: config.allowedOrigins,
					}),
				},
			},
		},
	})
	if err != nil {
		return err
	}

	// Create service
	_, err = corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(serviceName),
			Namespace: pulumi.String(config.namespace),
		},
		Spec: &corev1.ServiceSpecArgs{
			Selector: pulumi.StringMap{
				"app": pulumi.String(deploymentName),
			},
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Port:       pulumi.Int(config.port),
					TargetPort: pulumi.Int(config.port),
				},
			},
			Type: pulumi.String("NodePort"),
		},
	},
		pulumi.DependsOn([]pulumi.Resource{deployment}),
	)

	if err != nil {
		return err
	}

	return nil
}

