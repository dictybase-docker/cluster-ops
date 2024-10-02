package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
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
  Name string
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
	pulumi.Run(execute)
}

func execute(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	frontendConfig := &FrontendConfig{}
	if err := cfg.TryObject("properties", frontendConfig); err != nil {
		return fmt.Errorf("failed to read frontend config: %w", err)
	}

	for _, app := range frontendConfig.Apps {
		appConfig := app

		deploymentName := fmt.Sprintf("%s-api-server", app.Name)
		serviceName := fmt.Sprintf("%s-api", app.Name)
		
		deployment, err := appsv1.NewDeployment(
			ctx, deploymentName, deploymentSpec(&specProperties{
				deploymentName: deploymentName,
				serviceName:    serviceName,
				port:           frontendConfig.Port,
				app:            &appConfig,
				namespace:      frontendConfig.Namespace,
			}))
		if err != nil {
			return fmt.Errorf("error in running deployment for %s: %w", app, err)
		}

		_, err = corev1.NewService(
			ctx,
			serviceName,
			serviceSpecs(
				&specProperties{
					deploymentName: deploymentName,
					serviceName:    serviceName,
					app:            &appConfig,
					port:           frontendConfig.Port,
					namespace:      frontendConfig.Namespace,
				},
			),
			pulumi.DependsOn([]pulumi.Resource{deployment}),
		)
		if err != nil {
			return fmt.Errorf("error in running service for %s: %w", app, err)
		}
	}

	return nil
}
