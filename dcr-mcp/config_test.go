package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type mcpResourceMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *mcpResourceMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *mcpResourceMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func validBaseConfig() *DcrMcpConfig {
	cfg := &DcrMcpConfig{
		Namespace:       "prod",
		GraphQLEndpoint: "https://graphql.dictycr.org/graphql",
		Image: ImageConfig{
			Tag: "v1.1.0",
		},
	}
	cfg.ApplyDefaults()
	return cfg
}

func TestValidateDefaultsDisabled(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	require.NoError(t, cfg.Validate())
	require.Equal(t, "dcr-mcp", cfg.Name)
	require.Equal(t, "start-server", cfg.Command)
	require.Equal(t, "0.0.0.0", cfg.Host)
	require.Equal(t, 8080, cfg.Port)
	require.Equal(t, 120, cfg.RateLimit)
	require.True(t, cfg.StatelessValue())
	require.Equal(t, "dictybase/dcr-mcp", cfg.Image.Name)
	require.Equal(t, []string{"*"}, cfg.AllowedOrigins)
}

func TestValidateBaseMissingFields(t *testing.T) {
	t.Parallel()
	cfg := &DcrMcpConfig{}
	cfg.ApplyDefaults()
	err := cfg.Validate()
	require.EqualError(t, err, "dcr-mcp:properties is missing namespace, image.tag, graphqlEndpoint")
}

func TestValidateBasePlaceholderFields(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	cfg.GraphQLEndpoint = "https://<graphql-host>/graphql"
	cfg.Image.Tag = "<immutable-tag>"
	err := cfg.Validate()
	require.EqualError(t, err, "dcr-mcp:properties contains placeholder in image.tag, graphqlEndpoint")
}

func TestValidateIngressEnabledMissingFields(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	cfg.Ingress = IngressConfig{
		Enabled: true,
	}
	err := cfg.Validate()
	require.EqualError(t, err, "ingress.enabled=true but ingress is missing hosts, path, tlsSecret")
}

func TestValidateIngressEnabledComplete(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	//nolint:gosec // G101 false positive on test fixture secret name.
	cfg.Ingress = IngressConfig{
		Enabled:   true,
		Hosts:     []string{"mcp.dictybase.dev"},
		Path:      "/",
		TLSSecret: "mcp-tls-secret",
		Label: LabelConfig{
			Name:  "kcert.dev/ingress",
			Value: "managed",
		},
	}
	require.NoError(t, cfg.Validate())
}

func TestInstallCreatesDeploymentAndServiceWithoutIngressByDefault(t *testing.T) {
	t.Parallel()
	mocks := &mcpResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	cfg := validBaseConfig()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewDcrMcp(cfg).Install(ctx)
	}, pulumi.WithMocks("dcr-mcp", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()

	dep, hasDep := mocks.resources["dcr-mcp-api-server"]
	require.True(t, hasDep)
	require.Contains(t, fmt.Sprint(dep.Inputs), "dictybase/dcr-mcp:v1.1.0")
	require.Contains(t, fmt.Sprint(dep.Inputs), "--graphql-endpoint")
	require.Contains(t, fmt.Sprint(dep.Inputs), "https://graphql.dictycr.org/graphql")
	require.Contains(t, fmt.Sprint(dep.Inputs), "--stateless=true")
	require.Contains(t, fmt.Sprint(dep.Inputs), "/healthz")

	svc, hasSvc := mocks.resources["dcr-mcp-api"]
	require.True(t, hasSvc)
	require.Contains(t, fmt.Sprint(svc.Inputs), "NodePort")

	_, hasIng := mocks.resources["dcr-mcp-ingress"]
	require.False(t, hasIng)
}

func TestInstallWithIngressEnabled(t *testing.T) {
	t.Parallel()
	mocks := &mcpResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	cfg := validBaseConfig()
	//nolint:gosec // G101 false positive on test fixture secret name.
	cfg.Ingress = IngressConfig{
		Enabled:   true,
		Hosts:     []string{"mcp.dictybase.dev"},
		Path:      "/",
		TLSSecret: "mcp-tls-secret",
		Label: LabelConfig{
			Name:  "kcert.dev/ingress",
			Value: "managed",
		},
	}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewDcrMcp(cfg).Install(ctx)
	}, pulumi.WithMocks("dcr-mcp", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()

	ing, hasIng := mocks.resources["dcr-mcp-ingress"]
	require.True(t, hasIng)
	require.Contains(t, fmt.Sprint(ing.Inputs), "mcp.dictybase.dev")
	require.Contains(t, fmt.Sprint(ing.Inputs), "dcr-mcp-api")
}
