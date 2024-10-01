package backend

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func Run(ctx *pulumi.Context) error {
	backendConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	bck := NewBackend(backendConfig)

	if err := bck.Install(ctx); err != nil {
		return err
	}

	return nil
}
