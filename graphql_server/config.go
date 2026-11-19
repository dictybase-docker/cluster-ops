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
	Ncbi  NcbiConfig
}

// NcbiConfig describes the optional Secret that carries the NCBI identity
// values. Name and Keys are Kubernetes pointers with fixed defaults, so a
// stack sets only the three Values, as Pulumi secrets. A stack without any
// value deploys without the Secret and without the environment variables.
type NcbiConfig struct {
	Name   string
	Keys   NcbiFields
	Values NcbiFields
}

// NcbiFields holds the three NCBI identity items. Keys use it for the Secret
// key names and Values use it for the item values.
type NcbiFields struct {
	APIKey string
	Email  string
	Tool   string
}

// Enabled reports whether the stack sets any NCBI value.
func (n NcbiConfig) Enabled() bool {
	return n.Values != (NcbiFields{})
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
	c.Secrets.Ncbi.applyDefaults()
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
	if err := c.validateNcbi(); err != nil {
		return err
	}
	return c.validateIngress()
}

// Fixed names of the NCBI Secret and its keys. They are Kubernetes pointers,
// not credentials.
//
//nolint:gosec // G101 false positive: these are Secret and key names.
const (
	ncbiSecretName = "graphql-ncbi"
	ncbiKeyAPIKey  = "graphql.ncbi.apiKey"
	ncbiKeyEmail   = "graphql.ncbi.email"
	ncbiKeyTool    = "graphql.ncbi.tool"
)

// applyDefaults fills the fixed Secret name and key names.
func (n *NcbiConfig) applyDefaults() {
	if n.Name == "" {
		n.Name = ncbiSecretName
	}
	if n.Keys.APIKey == "" {
		n.Keys.APIKey = ncbiKeyAPIKey
	}
	if n.Keys.Email == "" {
		n.Keys.Email = ncbiKeyEmail
	}
	if n.Keys.Tool == "" {
		n.Keys.Tool = ncbiKeyTool
	}
}

// validateNcbi fails closed on a partial NCBI configuration. A stack without
// any NCBI value stays valid. NCBI asks every client to send its tool name and
// contact email, so a stack that sets one value must set all three.
func (c *GraphqlServerConfig) validateNcbi() error {
	ncbi := c.Secrets.Ncbi
	if !ncbi.Enabled() {
		return nil
	}
	missing := []string{}
	for _, field := range []struct {
		name string
		val  string
	}{
		{"secrets.ncbi.values.apiKey", ncbi.Values.APIKey},
		{"secrets.ncbi.values.email", ncbi.Values.Email},
		{"secrets.ncbi.values.tool", ncbi.Values.Tool},
	} {
		if field.val == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"secrets.ncbi is partly set; properties is missing %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
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
