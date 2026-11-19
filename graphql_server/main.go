package main

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type GraphqlServer struct {
	Config *GraphqlServerConfig
	// NcbiSecret is the optional NCBI Secret. It is nil when the stack does
	// not configure one.
	NcbiSecret *corev1.Secret
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
	if gs.Config.Secrets.Ncbi.Enabled() {
		secret, err := gs.CreateNcbiSecret(ctx)
		if err != nil {
			return err
		}
		gs.NcbiSecret = secret
	}
	deployment, err := gs.CreateDeployment(ctx)
	if err != nil {
		return err
	}

	_, err = gs.CreateService(ctx, deployment)
	if err != nil {
		return err
	}

	if err = gs.CreateIngress(ctx); err != nil {
		return err
	}

	return nil
}
