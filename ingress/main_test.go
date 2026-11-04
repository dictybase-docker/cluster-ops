package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type ingressResourceMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *ingressResourceMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *ingressResourceMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func validSampleIngressConfig() *Config {
	//nolint:gosec // G101: Kubernetes Secret name pointer, not a credential.
	cfg := &Config{
		Namespace: "dev",
		Frontend: IngressConfig{
			Hosts: []string{"dictybase.dev"},
			Label: LabelConfig{
				Name:  "kcert.dev/ingress",
				Value: "managed",
			},
			Secret: "frontend-tls",
			Services: []ServiceBackend{
				{Name: "frontpage-api", Path: "/", Port: 3000},
				{Name: "stockcenter-api", Path: "/stockcenter", Port: 3000},
				{Name: "publication-api", Path: "/publication", Port: 3000},
				{Name: "genomepage-api", Path: "/gene", Port: 3000},
			},
		},
		API: IngressConfig{
			Hosts: []string{"<api-host>"},
			Label: LabelConfig{
				Name:  "kcert.dev/ingress",
				Value: "managed",
			},
			Secret: "api-tls",
		},
	}
	cfg.ApplyDefaults()
	return cfg
}

func TestIngressConfigDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	cfg := validSampleIngressConfig()
	require.NoError(t, cfg.Validate())
	require.True(t, cfg.Frontend.IsEnabled())
	require.False(t, cfg.API.IsEnabled())
	require.Len(t, cfg.API.Services, 1)
	require.Equal(t, "stock-api", cfg.API.Services[0].Name)
	require.Equal(t, "/v1/stocks", cfg.API.Services[0].Path)
	require.Equal(t, 9345, cfg.API.Services[0].Port)
}

func TestIngressConfigValidateFailures(t *testing.T) {
	t.Parallel()

	t.Run("missing namespace", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleIngressConfig()
		cfg.Namespace = ""
		require.EqualError(t, cfg.Validate(), "ingress:properties is missing namespace")
	})

	t.Run("all ingresses disabled", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleIngressConfig()
		cfg.Frontend.Enabled = new(false)
		cfg.API.Enabled = new(false)
		require.EqualError(t, cfg.Validate(), "ingress:properties must enable at least one ingress")
	})

	t.Run("enabled api ingress contains placeholder host", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleIngressConfig()
		cfg.API.Enabled = new(true)
		require.EqualError(t, cfg.Validate(), "api.enabled=true but api contains placeholder in hosts")
	})

	t.Run("enabled api ingress missing hosts and secret", func(t *testing.T) {
		t.Parallel()
		cfg := validSampleIngressConfig()
		cfg.API.Enabled = new(true)
		cfg.API.Hosts = nil
		cfg.API.Secret = ""
		require.EqualError(t, cfg.Validate(), "api.enabled=true but api is missing hosts, secret")
	})
}

func TestInstallCreatesFrontendAndOptionalApiIngress(t *testing.T) {
	t.Parallel()

	t.Run("api disabled by default creates only frontend-ingress", func(t *testing.T) {
		t.Parallel()
		mocks := &ingressResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
		cfg := validSampleIngressConfig()

		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			return NewIngresses(cfg).Install(ctx)
		}, pulumi.WithMocks("ingress", "test", mocks))
		require.NoError(t, err)

		mocks.mu.Lock()
		defer mocks.mu.Unlock()

		fe, hasFE := mocks.resources["frontend-ingress"]
		require.True(t, hasFE)
		require.Equal(t, "kubernetes:networking.k8s.io/v1:Ingress", fe.TypeToken)
		require.Contains(t, fmt.Sprint(fe.Inputs), "dictybase.dev")
		require.Contains(t, fmt.Sprint(fe.Inputs), "kcert.dev/ingress")

		_, hasAPI := mocks.resources["api-ingress"]
		require.False(t, hasAPI)
	})

	t.Run("api enabled creates api-ingress with stock-api rule", func(t *testing.T) {
		t.Parallel()
		mocks := &ingressResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
		cfg := validSampleIngressConfig()
		cfg.API.Enabled = new(true)
		cfg.API.Hosts = []string{"api.dictybase.dev"}
		require.NoError(t, cfg.Validate())

		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			return NewIngresses(cfg).Install(ctx)
		}, pulumi.WithMocks("ingress", "test", mocks))
		require.NoError(t, err)

		mocks.mu.Lock()
		defer mocks.mu.Unlock()

		apiIng, hasAPI := mocks.resources["api-ingress"]
		require.True(t, hasAPI)
		require.Equal(t, "kubernetes:networking.k8s.io/v1:Ingress", apiIng.TypeToken)
		apiSpec := fmt.Sprint(apiIng.Inputs)
		require.Contains(t, apiSpec, "api.dictybase.dev")
		require.Contains(t, apiSpec, "api-tls")
		require.Contains(t, apiSpec, "stock-api")
		require.Contains(t, apiSpec, "/v1/stocks")
		require.Contains(t, apiSpec, "9345")
	})
}
