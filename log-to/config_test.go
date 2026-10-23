package main

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type logtoResourceMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *logtoResourceMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *logtoResourceMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func sampleLogtoConfig(ingressEnabled bool) *LogtoConfig {
	//nolint:gosec // G101 false positive on test fixture secret names.
	return &LogtoConfig{
		Name:           "logto",
		Namespace:      "prod",
		StorageClass:   "dictycr-balanced",
		DiskSize:       "50Gi",
		DatabaseSecret: "logto-app",
		Endpoint:       "https://auth.example.com",
		APIPort:        3001,
		AdminPort:      3002,
		Image: ImageConfig{
			Name: "svhd/logto",
			Tag:  "1.43.0",
		},
		Ingress: IngressConfig{
			Enabled:      ingressEnabled,
			TLSSecret:    "logto-tls",
			BackendHosts: []string{"auth.example.com"},
			Label: LabelConfig{
				Name:  "kcert.dev/ingress",
				Value: "managed",
			},
		},
	}
}

func TestInstallSkipsIngressWhenDisabled(t *testing.T) {
	t.Parallel()
	mocks := &logtoResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	cfg := sampleLogtoConfig(false)

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewLogto(cfg).Install(ctx)
	}, pulumi.WithMocks("log-to", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	_, hasIngress := mocks.resources["logto-ingress"]
	_, hasAPISvc := mocks.resources["logto-api"]
	_, hasAdminSvc := mocks.resources["logto-admin"]
	mocks.mu.Unlock()
	require.False(t, hasIngress, "Ingress must not be created when ingress.enabled is false")
	require.True(t, hasAPISvc)
	require.True(t, hasAdminSvc)
}

func TestInstallCreatesIngressWhenEnabled(t *testing.T) {
	t.Parallel()
	mocks := &logtoResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	cfg := sampleLogtoConfig(true)

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewLogto(cfg).Install(ctx)
	}, pulumi.WithMocks("log-to", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	ing, hasIngress := mocks.resources["logto-ingress"]
	mocks.mu.Unlock()
	require.True(t, hasIngress, "Ingress must be created when ingress.enabled is true")
	require.Equal(t, "kubernetes:networking.k8s.io/v1:Ingress", ing.TypeToken)
}
