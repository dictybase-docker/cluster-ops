package main

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type GraphqlServerConfig struct {
	AllowedOrigins []string
	AuthEnabled    bool
	Endpoints      EndpointsConfig
	Image          ImageConfig
	Ingress        IngressConfig
	LogLevel       string
	Name           string
	Namespace      string
	Port           int
	S3Bucket       S3BucketConfig
	Secrets        SecretsConfig
}

type EndpointsConfig struct {
	Auth        string
	Organism    string
	Publication string
	Store       string
}

type ImageConfig struct {
	Name string
	Tag  string
}

type IngressConfig struct {
	Hosts []string
	Path  string
	Label struct {
		Name  string
		Value string
	}
	TLSSecret string
}

type S3BucketConfig struct {
	Name string
	Path string
}

type SecretsConfig struct {
	Auth  AuthConfig
	Minio MinioConfig
}

type AuthConfig struct {
	AppId       string
	AppSecret   string
	JwksURI     string
	JwtAudience string
	JwtIssuer   string
}

type MinioConfig struct {
	Name    string
	PassKey string
	UserKey string
}

type SecureString struct {
	Secure string
}

func ReadConfig(ctx *pulumi.Context) (*GraphqlServerConfig, error) {
	conf := config.New(ctx, "graphql_server")
	graphqlConfig := &GraphqlServerConfig{}
	if err := conf.TryObject("properties", graphqlConfig); err != nil {
		return nil, fmt.Errorf("failed to read graphql config: %w", err)
	}
	if err := graphqlConfig.validate(); err != nil {
		return nil, err
	}
	return graphqlConfig, nil
}

// validate fails closed when auth is enabled without its logto secrets; a
// half-configured stack would otherwise deploy a container that cannot boot.
func (c *GraphqlServerConfig) validate() error {
	if !c.AuthEnabled {
		return nil
	}
	missing := []string{}
	for name, val := range map[string]string{
		"appId":       c.Secrets.Auth.AppId,
		"appSecret":   c.Secrets.Auth.AppSecret,
		"jwksURI":     c.Secrets.Auth.JwksURI,
		"jwtIssuer":   c.Secrets.Auth.JwtIssuer,
		"jwtAudience": c.Secrets.Auth.JwtAudience,
	} {
		if val == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"authEnabled=true but secrets.auth is missing %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}
