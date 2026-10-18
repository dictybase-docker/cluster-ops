package main

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSecretPassword = "test-secret-password"
)

type arangoSingleMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *arangoSingleMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *arangoSingleMocks) NewResource(
	args pulumi.MockResourceArgs,
) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name, args.Inputs, nil
}

func newTestArangoConfig() *ArangoDeploymentConfig {
	cfg := &ArangoDeploymentConfig{
		Namespace: "dev",
		Version:   "3.11.6",
	}
	cfg.Storage.Class = "dictycr-balanced"
	cfg.Storage.Size = "150Gi"
	cfg.Secret.Name = "arangodb-pass"
	cfg.Secret.Password = testSecretPassword
	return cfg
}

func runArangoSingleInstall(
	t *testing.T,
	cfg *ArangoDeploymentConfig,
) []pulumi.MockResourceArgs {
	t.Helper()
	mocks := &arangoSingleMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewArangoDeployment(cfg).Install(ctx)
	}, pulumi.WithMocks("arangodb-single", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	return mocks.resources
}

func findMockResourceInputs(
	resources []pulumi.MockResourceArgs,
	name string,
) resource.PropertyMap {
	for _, res := range resources {
		if res.Name == name {
			return res.Inputs
		}
	}
	return nil
}

func TestInstallSecretIncludesRootUsername(t *testing.T) {
	resources := runArangoSingleInstall(t, newTestArangoConfig())
	secretInputs := findMockResourceInputs(resources, "arangodb-secret")
	require.NotNil(t, secretInputs, "expect arangodb-secret resource to be created")

	stringDataProp, ok := secretInputs["stringData"]
	require.True(t, ok, "expect stringData on arangodb-secret")
	if stringDataProp.IsSecret() {
		stringDataProp = stringDataProp.SecretValue().Element
	}
	require.True(t, stringDataProp.IsObject())
	stringData := stringDataProp.ObjectValue()

	passProp, hasPass := stringData["password"]
	require.True(t, hasPass, "expect password key in arangodb-secret stringData")
	assert.Equal(t, testSecretPassword, passProp.StringValue())

	patchInputs := findMockResourceInputs(resources, "arangodb-secret-username")
	require.NotNil(t, patchInputs, "expect arangodb-secret-username SecretPatch to be created")
	patchDataProp, ok := patchInputs["stringData"]
	require.True(t, ok, "expect stringData on arangodb-secret-username")
	if patchDataProp.IsSecret() {
		patchDataProp = patchDataProp.SecretValue().Element
	}
	require.True(t, patchDataProp.IsObject())
	userProp, hasUser := patchDataProp.ObjectValue()["username"]
	require.True(t, hasUser, "expect username key in arangodb-secret-username stringData")
	assert.Equal(t, "root", userProp.StringValue())
}

func TestInstallSingleSpecIncludesRocksDBArgs(t *testing.T) {
	resources := runArangoSingleInstall(t, newTestArangoConfig())
	arangoInputs := findMockResourceInputs(resources, "arangodb")
	require.NotNil(t, arangoInputs, "expect arangodb ArangoDeployment resource to be created")

	specProp, hasSpec := arangoInputs["spec"]
	require.True(t, hasSpec, "expect spec on ArangoDeployment")
	require.True(t, specProp.IsObject())
	singleProp, hasSingle := specProp.ObjectValue()["single"]
	require.True(t, hasSingle, "expect single spec on ArangoDeployment")
	require.True(t, singleProp.IsObject())
	argsProp, hasArgs := singleProp.ObjectValue()["args"]
	require.True(t, hasArgs, "expect args on single spec")
	require.True(t, argsProp.IsArray())

	var gotArgs []string
	for _, elem := range argsProp.ArrayValue() {
		gotArgs = append(gotArgs, elem.StringValue())
	}
	assert.Contains(t, gotArgs, "--rocksdb.block-cache-shard-bits=4")
	assert.NotContains(t, specProp.ObjectValue(), resource.PropertyKey("architecture"))
}

func TestInstallSetsArchitectureWhenConfigured(t *testing.T) {
	cfg := newTestArangoConfig()
	cfg.Architecture = "arm64"

	resources := runArangoSingleInstall(t, cfg)
	arangoInputs := findMockResourceInputs(resources, "arangodb")
	require.NotNil(t, arangoInputs)
	spec := arangoInputs["spec"].ObjectValue()
	archProp, hasArch := spec["architecture"]
	require.True(t, hasArch, "expect architecture when configured")
	require.Len(t, archProp.ArrayValue(), 1)
	assert.Equal(t, "arm64", archProp.ArrayValue()[0].StringValue())
}
