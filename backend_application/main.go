package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type specProperties struct {
	deploymentName string
	serviceName    string
	cfg            *config.Config
	ctx            *pulumi.Context
}

func main() {
	pulumi.Run(execute)
}

func execute(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	deploymentName := fmt.Sprintf("%s-api-server", cfg.Require("name"))
	serviceName := fmt.Sprintf("%s-api", cfg.Require("name"))
	deployment, err := appsv1.NewDeployment(
		ctx, deploymentName, deploymentSpec(&specProperties{
			cfg:            cfg,
			ctx:            ctx,
			deploymentName: deploymentName,
			serviceName:    serviceName,
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
				cfg:            cfg,
			},
		),
		pulumi.DependsOn([]pulumi.Resource{deployment}),
	)
	if err != nil {
		return fmt.Errorf("error in running service %s", err)
	}

	return nil
}
