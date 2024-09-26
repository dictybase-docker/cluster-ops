package main

import (
	"fmt"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

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

