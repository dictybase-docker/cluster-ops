package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyDefaults(t *testing.T) {
	cfg := &ResetRootConfig{}
	cfg.applyDefaults()

	assert.Equal(t, "arangodb", cfg.Server)
	assert.Equal(t, 8529, cfg.Port)
	assert.Equal(t, "arangodb/arangodb", cfg.Image.Name)
	assert.Equal(t, "3.12.10.1", cfg.Image.Tag)
	assert.Equal(t, "arangodb-jwt", cfg.JwtSecret.Name)
	assert.Equal(t, "token", cfg.JwtSecret.TokenKey)
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
