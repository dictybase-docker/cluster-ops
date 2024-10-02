package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type GraphqlServerConfig struct {
	AllowedOrigins []string
	ConfigMap      ConfigMap
	Image          ImageConfig
	LogLevel       string
	Name           string
	Namespace      string
	Port           int
	S3Bucket       S3BucketConfig
	Secrets        SecretsConfig
}

type ConfigMap struct {
	Name         string
	EndpointKeys EndpointKeysConfig
}

type EndpointKeysConfig struct {
	AuthEndpoint           string
	OrganismEndpoint       string
	PublicationAPIEndpoint string
	S3StorageEndpoint      string
}

type ImageConfig struct {
	Name string
	Tag  string
}

type S3BucketConfig struct {
	Name string
	Path string
}

type SecretsConfig struct {
	Name      string
	AuthKeys  AuthKeysConfig
	MinioKeys MinioKeysConfig
}

type AuthKeysConfig struct {
	AuthAppId     string
	AuthAppSecret string
	JwksURI       string
	JwtAudience   string
	JwtIssuer     string
}

type MinioKeysConfig struct {
	MinioAccess string
	MinioSecret string
}

func ReadConfig(ctx *pulumi.Context) (*GraphqlServerConfig, error) {
	conf := config.New(ctx, "graphql_server")
	graphqlConfig := &GraphqlServerConfig{}
	if err := conf.TryObject("properties", graphqlConfig); err != nil {
		return nil, fmt.Errorf("failed to read graphql config: %w", err)
	}
	return graphqlConfig, nil
}
