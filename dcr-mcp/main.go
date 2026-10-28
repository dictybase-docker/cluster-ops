package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type DcrMcp struct {
	Config *DcrMcpConfig
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}
	return NewDcrMcp(cfg).Install(ctx)
}

func NewDcrMcp(cfg *DcrMcpConfig) *DcrMcp {
	cfg.ApplyDefaults()
	return &DcrMcp{
		Config: cfg,
	}
}

func (m *DcrMcp) Install(ctx *pulumi.Context) error {
	deployment, err := m.CreateDeployment(ctx)
	if err != nil {
		return err
	}

	if _, err = m.CreateService(ctx, deployment); err != nil {
		return err
	}

	return m.CreateIngress(ctx)
}
