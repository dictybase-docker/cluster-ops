package main

import (
	"fmt"
	"sync"
	"testing"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

const backendPassKey = "password"

type arangoResourceMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *arangoResourceMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *arangoResourceMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func TestInstallDoesNotPutAppPasswordInPodArgs(t *testing.T) {
	const appPassword = "destination-only-secret"
	mocks := &arangoResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	cfg := &ArangoDBConfig{
		Namespace: "prod",
		Databases: []string{"annotation"},
		Grant:     "rw",
	}
	cfg.ArangodbSecret.Name = "backend"
	cfg.ArangodbSecret.User = "app-user"
	cfg.ArangodbSecret.Pass = appPassword
	cfg.ArangodbSecret.UserKey = "user"
	cfg.ArangodbSecret.PassKey = backendPassKey
	cfg.ArangodbCredentials.Name = "arangodb-pass"
	cfg.ArangodbCredentials.PassKey = backendPassKey
	cfg.Image.Name = "ghcr.io/dictybase-docker/arangoadmin"
	cfg.Image.Tag = "fec50dd"

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewArangoDB(cfg).Install(ctx)
	}, pulumi.WithMocks("create-arangodb-databases", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	job := mocks.resources["backend-create-databases-manual"]
	mocks.mu.Unlock()
	require.Equal(t, "kubernetes:batch/v1:Job", job.TypeToken)
	require.Contains(
		t,
		fmt.Sprint(job.Inputs),
		"ensure-database-annotation",
		"default create-databases operation must keep database creation enabled",
	)
	require.NotContains(
		t,
		job.Inputs["spec"].ObjectValue(),
		resource.PropertyKey("ttlSecondsAfterFinished"),
		"Pulumi-managed Jobs must remain until their next tracked replacement",
	)
	require.NotContains(
		t,
		fmt.Sprint(job.Inputs),
		appPassword,
		"application password must never appear in the Pulumi Job spec",
	)
	podSpec := job.Inputs["spec"].ObjectValue()["template"].ObjectValue()["spec"].ObjectValue()
	userContainer := podSpec["initContainers"].ArrayValue()[0]
	userArgs := F.Pipe1(userContainer.ObjectValue()["args"].ArrayValue(), A.Map(resource.PropertyValue.StringValue))
	require.Contains(t, userArgs, "$(ARANGODB_APP_PASSWORD)")
	require.Contains(t, fmt.Sprint(userContainer), "ARANGODB_APP_PASSWORD")
	require.Contains(t, fmt.Sprint(userContainer), "backend")
}

func TestCredentialOnlyInstallUpdatesUserAndGrantsWithoutCreatingDatabases(t *testing.T) {
	createDatabases := false
	cfg := &ArangoDBConfig{
		Namespace:       "prod",
		CreateDatabases: &createDatabases,
		RunID:           "run-42",
		Databases:       []string{"annotation", "order"},
		Grant:           "rw",
	}
	cfg.ArangodbSecret.Name = "backend"
	cfg.ArangodbSecret.User = "app-user"
	cfg.ArangodbSecret.Pass = "destination-password"
	cfg.ArangodbSecret.UserKey = "user"
	cfg.ArangodbSecret.PassKey = backendPassKey
	cfg.ArangodbCredentials.Name = "arangodb-pass"
	cfg.ArangodbCredentials.PassKey = backendPassKey
	cfg.Image.Name = "ghcr.io/dictybase-docker/arangoadmin"
	cfg.Image.Tag = "fec50dd"

	mocks := &arangoResourceMocks{resources: make(map[string]pulumi.MockResourceArgs)}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewArangoDB(cfg).Install(ctx)
	}, pulumi.WithMocks("create-arangodb-databases", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	job, ok := mocks.resources["backend-configure-app-credentials-run-42"]
	mocks.mu.Unlock()
	require.True(t, ok, "credential-only operation must register uniquely named Pulumi Job")
	require.NotContains(t, job.Inputs["spec"].ObjectValue(), resource.PropertyKey("ttlSecondsAfterFinished"))

	jobSpec := job.Inputs["spec"].ObjectValue()
	template := jobSpec["template"].ObjectValue()
	podSpec := template["spec"].ObjectValue()
	initContainers := podSpec["initContainers"].ArrayValue()
	mainContainers := podSpec["containers"].ArrayValue()

	initNames := make([]string, 0, len(initContainers))
	for _, container := range initContainers {
		initNames = append(initNames, container.ObjectValue()["name"].StringValue())
	}
	grantNames := make([]string, 0, len(mainContainers))
	for _, container := range mainContainers {
		grantNames = append(grantNames, container.ObjectValue()["name"].StringValue())
	}
	require.Equal(t, []string{"ensure-user"}, initNames)
	require.Equal(t, []string{"ensure-grant-annotation", "ensure-grant-order"}, grantNames)
}
