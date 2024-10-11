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
	Ingress        IngressConfig
}

type ConfigMap struct {
	Name           string
	EndpointKeys   EndpointKeysConfig
	EndpointValues EndpointValuesConfig
	GRPCKeys       GRPCKeysConfig
	GRPCValues     GRPCValuesConfig
}

type EndpointKeysConfig struct {
	AuthEndpoint           string
	OrganismEndpoint       string
	PublicationAPIEndpoint string
	S3StorageEndpoint      string
}

type EndpointValuesConfig struct {
	AuthEndpoint           string
	OrganismEndpoint       string
	PublicationAPIEndpoint string
	S3StorageEndpoint      string
}

type GRPCKeysConfig struct {
	StockHost      string
	StockPort      string
	OrderHost      string
	OrderPort      string
	AnnotationHost string
	AnnotationPort string
	ContentHost    string
	ContentPort    string
	RedisHost      string
	RedisPort      string
}

type GRPCValuesConfig struct {
	StockHost      string
	StockPort      string
	OrderHost      string
	OrderPort      string
	AnnotationHost string
	AnnotationPort string
	ContentHost    string
	ContentPort    string
	RedisHost      string
	RedisPort      string
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
	Name        string
	AuthKeys    AuthKeysConfig
	MinioKeys   MinioKeysConfig
	AuthValues  AuthValuesConfig
	MinioValues MinioValuesConfig
}

type AuthKeysConfig struct {
	AuthAppId     string
	AuthAppSecret string
	JwksURI       string
	JwtAudience   string
	JwtIssuer     string
}

type AuthValuesConfig struct {
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

type MinioValuesConfig struct {
	MinioAccess string
	MinioSecret string
}

type IngressConfig struct {
	Issuer    string
	TLSSecret string
	Hosts     []string
  Port int
  Path string
}

func ReadConfig(ctx *pulumi.Context) (*GraphqlServerConfig, error) {
	conf := config.New(ctx, "graphql_server")
	graphqlConfig := &GraphqlServerConfig{}
	if err := conf.TryObject("properties", graphqlConfig); err != nil {
		return nil, fmt.Errorf("failed to read graphql config: %w", err)
	}
	return graphqlConfig, nil
}
