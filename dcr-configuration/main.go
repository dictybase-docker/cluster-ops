package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func Run(ctx *pulumi.Context) error {
  return nil
}

func main() {
	pulumi.Run(Run)
}
