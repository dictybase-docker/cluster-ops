package main

import (
	"fmt"

	"github.com/dictybase-docker/cluster-ops/internal/nsprobe"
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
	Chart ChartConfig
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
	// kube-arangodb 1.4.x is a namespaced operator: the binary watches only
	// its own pod namespace (MY_POD_NAMESPACE) and the chart grants the
	// arangodeployments RBAC there. The release must therefore sit in the
	// app namespace — next to the ArangoDeployment CRs it manages. Take the
	// namespace from the namespace-bootstrap stack's appNamespace export so
	// a missing bootstrap fails at preview, not inside the Helm create.
	probe, namespace, err := nsprobe.Probe(ctx, "appNamespace")
	if err != nil {
		return err
	}

	// Install the Helm chart
	_, err = helm.NewRelease(ctx, "arangodb-operator", &helm.ReleaseArgs{
		Chart:     pulumi.String(aro.Config.Chart.Name),
		Version:   pulumi.String(aro.Config.Chart.Version),
		Namespace: namespace.ToStringPtrOutput(),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(aro.Config.Chart.Repository),
		},
		Values: pulumi.Map{
			"operator": pulumi.Map{
				"architectures": pulumi.Array{
					pulumi.String("amd64"),
					pulumi.String("arm64"),
				},
			},
		},
	}, pulumi.DependsOn([]pulumi.Resource{probe}))
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
