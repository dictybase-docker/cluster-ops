package main

import (
	"github.com/dictybase-docker/cluster-ops/internal/backend"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func Run(ctx *pulumi.Context) error {
	backendConfig, err := backend.ReadConfig(ctx)
	if err != nil {
		return err
	}

	bck := backend.NewBackend(backendConfig)

	if err := bck.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
