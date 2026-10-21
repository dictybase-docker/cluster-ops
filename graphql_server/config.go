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
	Enabled bool
	Hosts   []string
	Path    string
	Label   struct {
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
	graphqlConfig.applyDefaults()
	if err := graphqlConfig.validate(); err != nil {
		return nil, err
	}
	return graphqlConfig, nil
}

func (c *GraphqlServerConfig) applyDefaults() {
	if c.Name == "" {
		c.Name = "graphql"
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	if c.LogLevel == "" {
		c.LogLevel = "error"
	}
	if c.Image.Name == "" {
		c.Image.Name = "dictybase/graphql-server"
	}
	if c.S3Bucket.Name == "" {
		c.S3Bucket.Name = "editor"
	}
	if c.S3Bucket.Path == "" {
		c.S3Bucket.Path = "assets"
	}
	if c.Endpoints.Organism == "" {
		c.Endpoints.Organism = "https://raw.githubusercontent.com/dictyBase/" +
			"migration-data/master/downloads/organisms-with-citations.staging.json"
	}
	if c.Endpoints.Publication == "" {
		c.Endpoints.Publication = "https://www.ebi.ac.uk/europepmc/webservices/rest/search"
	}
}

// validate fails closed when required base, auth, or ingress fields are
// missing; a half-configured stack would otherwise send invalid empty values to
// the Kubernetes API server.
func (c *GraphqlServerConfig) validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if err := c.validateAuth(); err != nil {
		return err
	}
	return c.validateIngress()
}

func (c *GraphqlServerConfig) validateBase() error {
	missing := []string{}
	for _, field := range []struct {
		name string
		val  string
	}{
		{"namespace", c.Namespace},
		{"image.tag", c.Image.Tag},
		{"endpoints.store", c.Endpoints.Store},
		{"secrets.minio.name", c.Secrets.Minio.Name},
		{"secrets.minio.userKey", c.Secrets.Minio.UserKey},
		{"secrets.minio.passKey", c.Secrets.Minio.PassKey},
	} {
		if field.val == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"graphql_server:properties is missing %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

func (c *GraphqlServerConfig) validateAuth() error {
	if !c.AuthEnabled {
		return nil
	}
	missing := []string{}
	for _, field := range []struct {
		name string
		val  string
	}{
		{"appId", c.Secrets.Auth.AppId},
		{"appSecret", c.Secrets.Auth.AppSecret},
		{"jwksURI", c.Secrets.Auth.JwksURI},
		{"jwtIssuer", c.Secrets.Auth.JwtIssuer},
		{"jwtAudience", c.Secrets.Auth.JwtAudience},
	} {
		if field.val == "" {
			missing = append(missing, field.name)
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

func (c *GraphqlServerConfig) validateIngress() error {
	if !c.Ingress.Enabled {
		return nil
	}
	missing := []string{}
	if len(c.Ingress.Hosts) == 0 {
		missing = append(missing, "hosts")
	}
	if c.Ingress.Path == "" {
		missing = append(missing, "path")
	}
	if c.Ingress.TLSSecret == "" {
		missing = append(missing, "tlsSecret")
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"ingress.enabled=true but ingress is missing %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}
