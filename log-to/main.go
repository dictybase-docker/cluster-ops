package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type LogtoConfig struct {
  Name string
  Namespace string
  StorageClass string
  DiskSize int
  Database DatabaseProperties
  Endpoint string
  ApiPort int
  AdminPort int
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
