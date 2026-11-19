package main

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type graphqlMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *graphqlMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *graphqlMocks) NewResource(
	args pulumi.MockResourceArgs,
) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func runGraphqlInstall(
	t *testing.T,
	cfg *GraphqlServerConfig,
) map[string]pulumi.MockResourceArgs {
	t.Helper()
	mocks := &graphqlMocks{resources: map[string]pulumi.MockResourceArgs{}}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewGraphqlServer(cfg).Install(ctx)
	}, pulumi.WithMocks("graphql", "test", mocks))
	require.NoError(t, err)
	return mocks.resources
}

func containerEnv(t *testing.T, dep pulumi.MockResourceArgs) map[string]resource.PropertyMap {
	t.Helper()
	spec := dep.Inputs["spec"].ObjectValue()
	tmpl := spec["template"].ObjectValue()
	containers := tmpl["spec"].ObjectValue()["containers"].ArrayValue()
	require.Len(t, containers, 1)
	env := map[string]resource.PropertyMap{}
	for _, item := range containers[0].ObjectValue()["env"].ArrayValue() {
		obj := item.ObjectValue()
		env[obj["name"].StringValue()] = obj["valueFrom"].ObjectValue()["secretKeyRef"].ObjectValue()
	}
	return env
}

func plainOrSecret(v resource.PropertyValue) string {
	if v.IsSecret() {
		return v.SecretValue().Element.StringValue()
	}
	return v.StringValue()
}

func TestInstallCreatesNcbiSecretAndEnv(t *testing.T) {
	res := runGraphqlInstall(t, ncbiConfig())

	secret, ok := res[ncbiSecretName]
	require.True(t, ok, "expect the NCBI Secret resource")
	stringData := secret.Inputs["stringData"]
	require.True(t, stringData.IsSecret(), "expect the Secret data to stay secret")
	data := stringData.SecretValue().Element.ObjectValue()
	require.Equal(t, "test-ncbi-key", plainOrSecret(data[ncbiKeyAPIKey]))
	require.Equal(t, "dev@example.org", plainOrSecret(data[ncbiKeyEmail]))
	require.Equal(t, "graphql-server", plainOrSecret(data[ncbiKeyTool]))

	env := containerEnv(t, res["graphql-api-server"])
	for name, key := range map[string]string{
		"NCBI_API_KEY": ncbiKeyAPIKey,
		"NCBI_EMAIL":   ncbiKeyEmail,
		"NCBI_TOOL":    ncbiKeyTool,
	} {
		ref, found := env[name]
		require.True(t, found, "expect env %s", name)
		require.Equal(t, key, ref["key"].StringValue())
		require.Equal(t, ncbiSecretName, ref["name"].StringValue())
		require.False(t, ref["optional"].BoolValue(), "expect a required reference")
	}
	require.Contains(t, env, "SECRET_KEY", "expect the MinIO env to stay")
}

func TestInstallWithoutNcbiKeepsBaseline(t *testing.T) {
	res := runGraphqlInstall(t, validBaseConfig())

	require.NotContains(t, res, ncbiSecretName)
	env := containerEnv(t, res["graphql-api-server"])
	require.NotContains(t, env, "NCBI_API_KEY")
	require.Contains(t, env, "SECRET_KEY")
	require.Contains(t, env, "ACCESS_KEY")
}
