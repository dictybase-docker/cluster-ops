package main

import (
	"fmt"

	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ChartConfig struct {
	Name       string
	Repository string
	Version    string
}

type ArangoDBConfig struct {
	Chart                 ChartConfig
	DeploymentReplication bool
	Namespace             string
}

type ArangoDBOperator struct {
	Config *ArangoDBConfig
}

func ReadConfig(ctx *pulumi.Context) (*ArangoDBConfig, error) {
	conf := config.New(ctx, "")
	arangoConfig := &ArangoDBConfig{}
	if err := conf.TryObject("properties", arangoConfig); err != nil {
		return nil, fmt.Errorf("failed to read arangodb config: %w", err)
	}
	return arangoConfig, nil
}

func NewArangoDBOperator(config *ArangoDBConfig) *ArangoDBOperator {
	return &ArangoDBOperator{
		Config: config,
	}
}

func (aro *ArangoDBOperator) Install(ctx *pulumi.Context) error {
	// Install the Helm chart
	_, err := helm.NewRelease(ctx, "arangodb-operator", &helm.ReleaseArgs{
		Chart:     pulumi.String(aro.Config.Chart.Name),
		Version:   pulumi.String(aro.Config.Chart.Version),
		Namespace: pulumi.String(aro.Config.Namespace),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(aro.Config.Chart.Repository),
		},
		Values: pulumi.Map{
			"operator": pulumi.Map{
				"features": pulumi.Map{
					"deploymentReplications": pulumi.Bool(
						aro.Config.DeploymentReplication,
					),
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to install Helm chart: %w", err)
	}

	return nil
}

func Run(ctx *pulumi.Context) error {
	arangoConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	arangoOperator := NewArangoDBOperator(arangoConfig)

	if err := arangoOperator.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
