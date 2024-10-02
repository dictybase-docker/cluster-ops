package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type GraphqlServer struct {
	Config *GraphqlServerConfig
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	config, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	graphqlServer := NewGraphqlServer(config)

	if err := graphqlServer.Install(ctx); err != nil {
		return err
	}

	return nil
}

func NewGraphqlServer(config *GraphqlServerConfig) *GraphqlServer {
	return &GraphqlServer{
		Config: config,
	}
}

func (gs *GraphqlServer) Install(ctx *pulumi.Context) error {
	deployment, err := gs.CreateDeployment(ctx)
	if err != nil {
		return err
	}

	if err := gs.CreateService(ctx, deployment); err != nil {
		return err
	}

	return nil
}
