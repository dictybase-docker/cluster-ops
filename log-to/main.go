package main

import (
  "fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type LogtoConfig struct {
  Name string
  Namespace string
  Image ImageConfig
  StorageClass string
  DiskSize int
  Database DatabaseProperties
  Endpoint string
  ApiPort int
  AdminPort int
}

type ImageConfig struct {
  Name string
  Tag string
}

type DatabaseProperties struct {
  Name string
  User string
  Host string
  Port int
  Password string
}

type Logto struct {
  Config *LogtoConfig
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		return nil
	})
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

func ReadConfig(ctx *pulumi.Context) (*LogtoConfig, error) {
	conf := config.New(ctx, "log-to")
	logtoConfig := &LogtoConfig{}
	if err := conf.TryObject("properties", logtoConfig); err != nil {
		return nil, fmt.Errorf("failed to read log-to config: %w", err)
	}
	return logtoConfig, nil
}
