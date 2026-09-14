package main

import (
	"fmt"

	"github.com/dictybase-docker/cluster-ops/internal/nsprobe"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type PluginConfig struct {
	Chart struct {
		Name       string
		Repository string
		Version    string
	}
}

type Plugin struct {
	config PluginConfig
}

func main() {
	pulumi.Run(run)
}

func run(ctx *pulumi.Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	return NewPlugin(cfg).Install(ctx)
}

func loadConfig(ctx *pulumi.Context) (PluginConfig, error) {
	conf := config.New(ctx, "")
	var cfg PluginConfig
	if err := conf.TryObject("properties", &cfg); err != nil {
		return PluginConfig{}, fmt.Errorf(
			"failed to read cnpg-backup-plugin properties: %w",
			err,
		)
	}
	return cfg, nil
}

func NewPlugin(config PluginConfig) *Plugin {
	return &Plugin{config: config}
}

// Install deploys the Barman Cloud CNPG-I plugin chart into the operator
// namespace. Requires cert-manager (pre-configured by the kops bootstrap)
// and namespace-bootstrap to have run. The CloudNativePG Cluster resources
// created by cloudnative-pg-cluster probe this stack via
// nsprobe.ProbePlugin, so the plugin must be applied BEFORE any cluster
// stack that enables plugin backups.
func (p *Plugin) Install(ctx *pulumi.Context) error {
	// Guard: refuse to run when the namespace-bootstrap stack has not been
	// applied — the operator namespace does not exist yet. The Helm release
	// namespace comes from its operatorsNamespace export (single source of
	// truth).
	probe, namespace, err := nsprobe.Probe(ctx, "operatorsNamespace")
	if err != nil {
		return err
	}

	release, err := helm.NewRelease(ctx, nsprobe.PluginDeploymentName, &helm.ReleaseArgs{
		Chart:     pulumi.String(p.config.Chart.Name),
		Version:   pulumi.String(p.config.Chart.Version),
		Namespace: namespace.ToStringPtrOutput(),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(p.config.Chart.Repository),
		},
	}, pulumi.DependsOn([]pulumi.Resource{probe}))
	if err != nil {
		return fmt.Errorf("failed to install Helm chart: %w", err)
	}

	ctx.Export("releaseName", release.Status.Name())
	ctx.Export("namespace", namespace.ToStringPtrOutput())
	return nil
}
