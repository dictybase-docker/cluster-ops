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

  deployment, err := lt.CreateDeployment(ctx, claimName)
	if err != nil {
		return err
	}

  _, err = lt.CreateService(ctx, deployment.Metadata.Name().Elem(), fmt.Sprintf("%s-api", lt.Config.Name), lt.Config.ApiPort)
	if err != nil {
		return err
	}
  _, err = lt.CreateService(ctx, deployment.Metadata.Name().Elem(), fmt.Sprintf("%s-admin", lt.Config.Name), lt.Config.AdminPort)
	if err != nil {
		return err
	}

	return nil
}

