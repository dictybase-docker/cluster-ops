package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type GatewayStack struct {
	Config *GatewayConfig
}

func NewGatewayStack(cfg *GatewayConfig) *GatewayStack {
	cfg.ApplyDefaults()
	return &GatewayStack{
		Config: cfg,
	}
}

func (g *GatewayStack) Install(ctx *pulumi.Context) error {
	issuer, err := g.createIssuer(ctx)
	if err != nil {
		return err
	}

	gw, err := g.createGateway(ctx, issuer)
	if err != nil {
		return err
	}

	if err := g.createHTTPRoutes(ctx, gw); err != nil {
		return err
	}

	ctx.Export("gatewayName", gw.Metadata.Name())
	ctx.Export("issuerName", issuer.Metadata.Name())
	return nil
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}
	return NewGatewayStack(cfg).Install(ctx)
}

func main() {
	pulumi.Run(Run)
}
