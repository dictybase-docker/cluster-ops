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
	Namespace     string
	Name          string
	Image         string
	Tag           string
	LogLevel      string
	Port          int
	SecretName    string
	ConfigMapName string
	S3Bucket      string
	S3BucketPath  string
  AllowedOrigins []string
}

type GraphqlServer struct {
  Config *GraphqlServerConfig
}

func main() {
	pulumi.Run(Run)
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

func ReadConfig(ctx *pulumi.Context) (*GraphqlServerConfig, error) {
	conf := config.New(ctx, "graphql_server")
	graphqlConfig := &GraphqlServerConfig{}
	if err := conf.TryObject("properties", graphqlConfig); err != nil {
		return nil, fmt.Errorf("failed to read graphql config: %w", err)
	}
	return graphqlConfig, nil
}

func NewGraphqlServer(config *GraphqlServerConfig) *GraphqlServer {
  return &GraphqlServer{
    Config: config,
  }
}

func (gs *GraphqlServer) CreateDeployment(ctx *pulumi.Context) (*appsv1.Deployment, error) {
	config := gs.Config
	deploymentName := fmt.Sprintf("%s-api-server", config.Name)

	deployment, err := appsv1.NewDeployment(ctx, fmt.Sprintf("%s-api-server", config.Name), &appsv1.DeploymentArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(deploymentName),
			Namespace: pulumi.String(config.Namespace),
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
					Containers: gs.ContainerArray(),
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return deployment, nil
}

func (gs *GraphqlServer) Install(ctx *pulumi.Context) error {
	deployment, err := gs.CreateDeployment(ctx)
	if err != nil {
		return err
	}

	config := gs.Config
	deploymentName := fmt.Sprintf("%s-api-server", config.Name)
	serviceName := fmt.Sprintf("%s-api", config.Name)

	// Create service
	_, err = corev1.NewService(ctx, serviceName, &corev1.ServiceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(serviceName),
			Namespace: pulumi.String(config.Namespace),
		},
		Spec: &corev1.ServiceSpecArgs{
			Selector: pulumi.StringMap{
				"app": pulumi.String(deploymentName),
			},
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Port:       pulumi.Int(config.Port),
					TargetPort: pulumi.Int(config.Port),
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

