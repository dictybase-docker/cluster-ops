package main

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
)

func TestApplyDefaults(t *testing.T) {
	cfg := &ResetRootConfig{}
	cfg.applyDefaults()

	assert.Equal(t, "arangodb", cfg.Server)
	assert.Equal(t, 8529, cfg.Port)
	assert.Equal(t, "arangodb", cfg.Image.Name)
	assert.Equal(t, "3.12.11", cfg.Image.Tag)
	assert.Equal(t, "arangodb-jwt", cfg.JwtSecret.Name)
	assert.Equal(t, "token", cfg.JwtSecret.TokenKey)
	assert.Equal(t, "manual", cfg.RunID)
}

type resetRootMocks struct {
	mu        sync.Mutex
	resources []pulumi.MockResourceArgs
}

func (m *resetRootMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *resetRootMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, args)
	return args.Name, args.Inputs, nil
}

func TestInstallUsesRunIDForDistinctJobName(t *testing.T) {
	cfg := &ResetRootConfig{Namespace: "prod", RunID: "run-42"}
	cfg.applyDefaults()
	mocks := &resetRootMocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewResetRoot(cfg).Install(ctx)
	}, pulumi.WithMocks("reset-root-password", "test", mocks))
	assert.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	assert.Len(t, mocks.resources, 1)
	assert.Equal(t, "arangodb-reset-root-password-run-42", mocks.resources[0].Name)
	assert.NotContains(t, mocks.resources[0].Inputs["spec"].ObjectValue(), resource.PropertyKey("ttlSecondsAfterFinished"))
}

func TestApplyDefaults_NoOverride(t *testing.T) {
	cfg := &ResetRootConfig{
		Server: "custom",
		Port:   9529,
		Image: struct {
			Name string
			Tag  string
		}{
			Name: "custom/image",
			Tag:  "1.0",
		},
		JwtSecret: struct {
			Name     string
			TokenKey string
		}{
			Name:     "custom-jwt",
			TokenKey: "custom-token",
		},
	}
	cfg.applyDefaults()

	assert.Equal(t, "custom", cfg.Server)
	assert.Equal(t, 9529, cfg.Port)
	assert.Equal(t, "custom/image", cfg.Image.Name)
	assert.Equal(t, "1.0", cfg.Image.Tag)
	assert.Equal(t, "custom-jwt", cfg.JwtSecret.Name)
	assert.Equal(t, "custom-token", cfg.JwtSecret.TokenKey)
}
