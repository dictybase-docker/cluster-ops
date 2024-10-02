package main

import (
	"fmt"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type FrontendConfig struct {
	Namespace string
	Port      int
	LogLevel  string
	Apps      []AppConfig
}

type Frontend struct {
	Config *FrontendConfig
}

type AppConfig struct {
	Name  string
	Image string
	Tag   string
}

type specProperties struct {
	deploymentName string
	serviceName    string
	namespace      string
	port           int
	app            *AppConfig
}

func main() {
	pulumi.Run(Run)
}

func NewFrontend(config *FrontendConfig) *Frontend {
	return &Frontend{
		Config: config,
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

func (fe *Frontend) Install(ctx *pulumi.Context) error {
	for _, app := range fe.Config.Apps {
		appConfig := app

		deploymentName := fmt.Sprintf("%s-api-server", app.Name)
		serviceName := fmt.Sprintf("%s-api", app.Name)
		
		deployment, err := appsv1.NewDeployment(
			ctx, deploymentName, deploymentSpec(&specProperties{
				deploymentName: deploymentName,
				serviceName:    serviceName,
				port:           fe.Config.Port,
				app:            &appConfig,
				namespace:      fe.Config.Namespace,
			}))

		if err != nil {
			return fmt.Errorf("error in running deployment for %s: %w", app.Name, err)
		}

		_, err = corev1.NewService(
			ctx,
			serviceName,
			serviceSpecs(
				&specProperties{
					deploymentName: deploymentName,
					serviceName:    serviceName,
					app:            &appConfig,
					port:           fe.Config.Port,
					namespace:      fe.Config.Namespace,
				},
			),
			pulumi.DependsOn([]pulumi.Resource{deployment}),
		)
		if err != nil {
			return fmt.Errorf("error in running service for %s: %w", app.Name, err)
		}
	}

	return nil
}
