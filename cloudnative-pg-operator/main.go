package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type PgOperatorConfig struct {
	Chart struct {
		Name       string
		Repository string
		Version    string
	}
	Image struct {
		Tag string
	}
	Namespace string
}

func main() {
	pulumi.Run(run)
}

func run(ctx *pulumi.Context) error {
	pgConfig, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	if err := logNamespace(ctx, pgConfig.Namespace); err != nil {
		return err
	}

	// Add more operations using pgConfig here

	return nil
}

func loadConfig(ctx *pulumi.Context) (PgOperatorConfig, error) {
	conf := config.New(ctx, "pg-operator")
	var pgConfig PgOperatorConfig
	if err := conf.TryObject("properties", &pgConfig); err != nil {
		return PgOperatorConfig{}, fmt.Errorf("failed to read pg-operator properties: %w", err)
	}
	return pgConfig, nil
}

func logNamespace(ctx *pulumi.Context, namespace string) error {
	return ctx.Log.Info(fmt.Sprintf("Namespace: %s", namespace), nil)
}
