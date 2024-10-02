package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type SecretKeyPair struct {
	name string
	key  string
}

type ConfigMapPair struct {
	name string
	key  string
}

type ImageConfig struct {
	Name string
	Tag  string
}

type GraphqlServerConfig struct {
	Namespace              string
	Name                   string
	Image                  ImageConfig
	LogLevel               string
	Port                   int
	SecretName             string
	ConfigMapName          string
	S3Bucket               string
	S3BucketPath           string
	AllowedOrigins         []string
	AuthAppId              SecretKeyPair
	AuthAppSecret          SecretKeyPair
	AuthEndpoint           ConfigMapPair
	JwksURI                SecretKeyPair
	JwtAudience            SecretKeyPair
	JwtIssuer              SecretKeyPair
	MinioAccess            SecretKeyPair
	MinioSecret            SecretKeyPair
	OrganismEndpoint       ConfigMapPair
	PublicationApiEndpoint ConfigMapPair
	S3StorageEndpoint      ConfigMapPair
}

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

func ReadConfig(ctx *pulumi.Context) (*GraphqlServerConfig, error) {
	conf := config.New(ctx, "graphql_server")
	graphqlConfig := &GraphqlServerConfig{}
	if err := conf.TryObject("properties", graphqlConfig); err != nil {
		return nil, fmt.Errorf("failed to read graphql config: %w", err)
	}
	return graphqlConfig, nil
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
