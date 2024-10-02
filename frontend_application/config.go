package main

import (
	"fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func ReadConfig(ctx *pulumi.Context) (*FrontendConfig, error) {
	cfg := config.New(ctx, "")
	frontendConfig := &FrontendConfig{}
	if err := cfg.TryObject("properties", frontendConfig); err != nil {
		return nil, fmt.Errorf("failed to read frontend config: %w", err)
	}
	return frontendConfig, nil
}
