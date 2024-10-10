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
	configMap, err := gs.CreateConfigMap(ctx)
	if err != nil {
		return err
	}

	secret, err := gs.CreateSecret(ctx)
	if err != nil {
		return err
	}

	deployment, err := gs.CreateDeployment(ctx, configMap, secret)
	if err != nil {
		return err
	}

	_, err = gs.CreateService(ctx, deployment)

	if err != nil {
		return err
	}

	return nil
}
