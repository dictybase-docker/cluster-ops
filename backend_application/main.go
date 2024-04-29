package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type appProperties struct {
	Image string
	Tag   string
	Port  int
}

type specProperties struct {
	deploymentName string
	serviceName    string
	appName        string
	namespace      string
	cfg            *config.Config
	ctx            *pulumi.Context
	app            *appProperties
}

func main() {
	pulumi.Run(execute)
}

func execute(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	appNames := make([]string, 0)
	if err := cfg.TryObject("apps", &appNames); err != nil {
		return fmt.Errorf(
			"apps attribute is required in the configuration %s",
			err,
		)
	}
	namespace, err := cfg.Try("namespace")
	if err != nil {
		return fmt.Errorf("attribute namespace is missing %s", err)
	}
	for _, key := range appNames {
		app := &appProperties{}
		if err := cfg.TryObject(key, app); err != nil {
			return fmt.Errorf("app name %s is required %s", key, err)
		}
		deploymentName := fmt.Sprintf("%s-api-server", key)
		serviceName := fmt.Sprintf("%s-api", key)
		deployment, err := appsv1.NewDeployment(
			ctx, deploymentName, deploymentSpec(&specProperties{
				cfg:            cfg,
				ctx:            ctx,
				deploymentName: deploymentName,
				serviceName:    serviceName,
				app:            app,
				appName:        key,
				namespace:      namespace,
			}))
		if err != nil {
			return fmt.Errorf("error in running deployment %s", err)
		}
		_, err = corev1.NewService(
			ctx,
			serviceName,
			serviceSpecs(
				&specProperties{
					deploymentName: deploymentName,
					serviceName:    serviceName,
					app:            app,
					cfg:            cfg,
					appName:        key,
					namespace:      namespace,
				},
			),
			pulumi.DependsOn([]pulumi.Resource{deployment}),
		)
		if err != nil {
			return fmt.Errorf("error in running service %s", err)
		}
	}

	return nil
}
