package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type Logto struct {
	Config *LogtoConfig
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	config, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	lt := NewLogto(config)

	if err := lt.Install(ctx); err != nil {
		return err
	}

	return nil
}

func NewLogto(config *LogtoConfig) *Logto {
	return &Logto{
		Config: config,
	}
}

func (lt *Logto) Install(ctx *pulumi.Context) error {
	pvc, err := lt.CreatePersistentVolumeClaim(ctx)
	if err != nil {
		return err
	}

	claimName := pvc.Metadata.Name().Elem()

	deployment, err := lt.CreateDeployment(
		ctx,
		claimName,
		lt.Config.DatabaseSecret,
		pvc,
	)
	if err != nil {
		return err
	}

	apiService, err := lt.CreateService(
		ctx,
		deployment.Metadata.Name().Elem(),
		fmt.Sprintf("%s-api", lt.Config.Name),
		lt.Config.APIPort,
		deployment,
	)
	if err != nil {
		return err
	}

	_, err = lt.CreateService(
		ctx,
		deployment.Metadata.Name().Elem(),
		fmt.Sprintf("%s-admin", lt.Config.Name),
		lt.Config.AdminPort,
		deployment,
	)
	if err != nil {
		return err
	}

	// TODO: Implement CreateIngress function
	_, err = lt.CreateIngress(ctx, apiService)
	if err != nil {
		return err
	}

	return nil
}
