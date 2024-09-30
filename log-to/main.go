package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type LogtoConfig struct {
  Name string
  Namespace string
  StorageClass string
  DiskSize int
}

type Logto struct {
  Config *LogtoConfig
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		return nil
	})
}
