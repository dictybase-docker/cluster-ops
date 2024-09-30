package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type LogtoConfig struct {
  Name string
  Namespace string
}

type Logto struct {
  Config *LogtoConfig
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		return nil
	})
}
