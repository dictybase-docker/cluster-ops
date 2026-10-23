package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type gatewayResourceMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *gatewayResourceMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *gatewayResourceMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func validSampleGatewayConfig() *GatewayConfig {
	//nolint:gosec // G101 false positive on test fixture secret names.
	cfg := &GatewayConfig{
		Namespace: "prod",
		Issuer: IssuerConfig{
			Email: "ops@dictycr.org",
		},
		Routes: RoutesConfig{
			Frontend: FrontendRouteConfig{
				Enabled:   true,
				Hosts:     []string{"dictycr.org"},
				TLSSecret: "frontend-tls",
				Services: []FrontendServiceBackend{
					{Name: "frontpage-api", Path: "/", Port: 3000},
					{Name: "stockcenter-api", Path: "/stockcenter", Port: 3000},
					{Name: "publication-api", Path: "/publication", Port: 3000},
					{Name: "genomepage-api", Path: "/gene", Port: 3000},
				},
			},
			Graphql: ServiceRouteConfig{
				Enabled:     true,
				Hosts:       []string{"graphql.dictycr.org"},
				TLSSecret:   "graphql-tls",
				ServiceName: "graphql-api",
				ServicePort: 8080,
				Path:        "/",
			},
			Minio: ServiceRouteConfig{
				Enabled:     true,
				Hosts:       []string{"storage.dictycr.org"},
				TLSSecret:   "minio-tls",
				ServiceName: defaultMinioService,
				ServicePort: 9000,
				Path:        "/",
			},
			Logto: ServiceRouteConfig{
				Enabled:   false,
				Hosts:     []string{"<logto-host>"},
				TLSSecret: "logto-tls",
			},
		},
	}
	cfg.ApplyDefaults()
	return cfg
}

func TestConfigValidateDefaultsAndSuccess(t *testing.T) {
	t.Parallel()
	cfg := validSampleGatewayConfig()
	require.NoError(t, cfg.Validate())
	require.Equal(t, "dictycr-gateway", cfg.Name)
	require.Equal(t, "cilium", cfg.GatewayClassName)
	require.Equal(t, "letsencrypt-issuer", cfg.Issuer.Name)
	require.Equal(t, "https://acme-v02.api.letsencrypt.org/directory", cfg.Issuer.Server)
	require.Equal(t, "letsencrypt-account-key", cfg.Issuer.SecretName)
	require.Equal(t, "logto-api", cfg.Routes.Logto.ServiceName)
	require.Equal(t, 3001, cfg.Routes.Logto.ServicePort)
	require.Equal(t, "/", cfg.Routes.Logto.Path)
}

func TestConfigValidateFailures(t *testing.T) {
	t.Parallel()

	t.Run("missing base fields", func(t *testing.T) {
		t.Parallel()
		cfg := &GatewayConfig{}
		cfg.ApplyDefaults()
		err := cfg.Validate()
		require.EqualError(t, err, "gateway:properties is missing namespace, issuer.email")
	})

	t.Run("placeholder in issuer email", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleGatewayConfig()
		cfg.Issuer.Email = "<acme-email>"
		err := cfg.Validate()
		require.EqualError(t, err, "gateway:properties contains placeholder in issuer.email")
	})

	t.Run("all routes disabled", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleGatewayConfig()
		cfg.Routes.Frontend.Enabled = false
		cfg.Routes.Graphql.Enabled = false
		cfg.Routes.Minio.Enabled = false
		cfg.Routes.Logto.Enabled = false
		err := cfg.Validate()
		require.EqualError(t, err, "gateway:properties must enable at least one route")
	})

	t.Run("enabled route missing hosts and tlsSecret", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleGatewayConfig()
		cfg.Routes.Graphql.Hosts = nil
		cfg.Routes.Graphql.TLSSecret = ""
		err := cfg.Validate()
		require.EqualError(t, err, "routes.graphql.enabled=true but routes.graphql is missing hosts, tlsSecret")
	})

	t.Run("enabled route contains placeholder host", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleGatewayConfig()
		cfg.Routes.Graphql.Hosts = []string{"<graphql-host>"}
		err := cfg.Validate()
		require.EqualError(t, err, "routes.graphql.enabled=true but routes.graphql contains placeholder in hosts")
	})

	t.Run("enabled frontend route missing services", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleGatewayConfig()
		//nolint:gosec // G101 false positive on test fixture secret name.
		cfg.Routes.Frontend = FrontendRouteConfig{
			Enabled:   true,
			Hosts:     []string{"dictycr.org"},
			TLSSecret: "frontend-tls",
		}
		err := cfg.Validate()
		require.EqualError(t, err, "routes.frontend.enabled=true but routes.frontend is missing services")
	})
}

func TestInstallCreatesIssuerGatewayAndEnabledHTTPRoutes(t *testing.T) {
	t.Parallel()
	mocks := &gatewayResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	cfg := validSampleGatewayConfig()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewGatewayStack(cfg).Install(ctx)
	}, pulumi.WithMocks("gateway", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()

	issuer, hasIssuer := mocks.resources["letsencrypt-issuer"]
	require.True(t, hasIssuer)
	require.Equal(t, "kubernetes:cert-manager.io/v1:Issuer", issuer.TypeToken)
	require.Contains(t, fmt.Sprint(issuer.Inputs), "dictycr-gateway")
	require.Contains(t, fmt.Sprint(issuer.Inputs), "ops@dictycr.org")

	gw, hasGW := mocks.resources["dictycr-gateway"]
	require.True(t, hasGW)
	require.Equal(t, "kubernetes:gateway.networking.k8s.io/v1:Gateway", gw.TypeToken)
	gwSpec := fmt.Sprint(gw.Inputs)
	require.Contains(t, gwSpec, "cilium")
	require.Contains(t, gwSpec, "cert-manager.io/issuer")
	require.Contains(t, gwSpec, "https-frontend-0")
	require.Contains(t, gwSpec, "https-graphql-0")
	require.Contains(t, gwSpec, "https-minio-0")
	require.NotContains(t, gwSpec, "https-logto")

	feRoute, hasFE := mocks.resources["frontend-route"]
	require.True(t, hasFE)
	require.Equal(t, "kubernetes:gateway.networking.k8s.io/v1:HTTPRoute", feRoute.TypeToken)
	require.Contains(t, fmt.Sprint(feRoute.Inputs), "frontpage-api")
	require.Contains(t, fmt.Sprint(feRoute.Inputs), "/stockcenter")

	gqlRoute, hasGQL := mocks.resources["graphql-route"]
	require.True(t, hasGQL)
	require.Contains(t, fmt.Sprint(gqlRoute.Inputs), "graphql-api")

	minioRoute, hasMinio := mocks.resources["minio-route"]
	require.True(t, hasMinio)
	require.Contains(t, fmt.Sprint(minioRoute.Inputs), "minio")

	_, hasLogto := mocks.resources["logto-route"]
	require.False(t, hasLogto, "disabled logto route must not be created")
}

func TestInstallWithLogtoEnabled(t *testing.T) {
	t.Parallel()
	mocks := &gatewayResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	cfg := validSampleGatewayConfig()
	cfg.Routes.Logto.Enabled = true
	cfg.Routes.Logto.Hosts = []string{"auth.dictycr.org"}
	require.NoError(t, cfg.Validate())

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewGatewayStack(cfg).Install(ctx)
	}, pulumi.WithMocks("gateway", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()

	gw := mocks.resources["dictycr-gateway"]
	require.Contains(t, fmt.Sprint(gw.Inputs), "https-logto-0")

	logtoRoute, hasLogto := mocks.resources["logto-route"]
	require.True(t, hasLogto)
	require.Contains(t, fmt.Sprint(logtoRoute.Inputs), "logto-api")
}
