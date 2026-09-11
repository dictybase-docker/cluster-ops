package main

import (
	"fmt"

	"github.com/dictybase-docker/cluster-ops/internal/nsprobe"
	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
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
}

type PgOperator struct {
	config PgOperatorConfig
}

func main() {
	pulumi.Run(run)
}

func run(ctx *pulumi.Context) error {
	pgConfig, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	pgOperator := NewPgOperator(pgConfig)
	return pgOperator.Install(ctx)
}

func loadConfig(ctx *pulumi.Context) (PgOperatorConfig, error) {
	conf := config.New(ctx, "")
	var pgConfig PgOperatorConfig
	if err := conf.TryObject("properties", &pgConfig); err != nil {
		return PgOperatorConfig{}, fmt.Errorf(
			"failed to read pg-operator properties: %w",
			err,
		)
	}
	return pgConfig, nil
}

func NewPgOperator(config PgOperatorConfig) *PgOperator {
	return &PgOperator{config: config}
}

func (pg *PgOperator) Install(ctx *pulumi.Context) error {
	// The namespace comes from the namespace-bootstrap stack's export — probe
	// it so a missing bootstrap fails at preview, not inside the Helm create.
	probe, namespace, err := nsprobe.Probe(ctx, "operatorsNamespace")
	if err != nil {
		return err
	}

	// Install the Helm chart
	_, err = helm.NewRelease(ctx, "cloudnative-pg-operator", &helm.ReleaseArgs{
		Chart:     pulumi.String(pg.config.Chart.Name),
		Version:   pulumi.String(pg.config.Chart.Version),
		Namespace: namespace.ToStringPtrOutput(),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(pg.config.Chart.Repository),
		},
		Values: pulumi.Map{
			"image": pulumi.Map{
				"tag": pulumi.String(pg.config.Image.Tag),
			},
		},
	}, pulumi.DependsOn([]pulumi.Resource{probe}))
	if err != nil {
		return fmt.Errorf("failed to install Helm chart: %w", err)
	}
	return nil
}
