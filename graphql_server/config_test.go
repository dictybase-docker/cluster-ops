package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validBaseConfig() *GraphqlServerConfig {
	cfg := &GraphqlServerConfig{
		Namespace: "prod",
		Image: ImageConfig{
			Tag: "develop-5afc6ba",
		},
		Endpoints: EndpointsConfig{
			Store: "https://storage.dictybase.dev",
		},
		Secrets: SecretsConfig{
			Minio: MinioConfig{
				Name:    "minio-root",
				UserKey: "rootUser",
				PassKey: "rootPassword",
			},
		},
	}
	cfg.applyDefaults()
	return cfg
}

func TestValidateDefaultsDisabled(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	require.NoError(t, cfg.validate())
	require.Equal(t, "graphql", cfg.Name)
	require.Equal(t, 8080, cfg.Port)
	require.Equal(t, "dictybase/graphql-server", cfg.Image.Name)
}

func TestValidateBaseMissingFields(t *testing.T) {
	t.Parallel()
	cfg := &GraphqlServerConfig{}
	cfg.applyDefaults()
	err := cfg.validate()
	require.Error(t, err)
	require.EqualError(
		t,
		err,
		"graphql_server:properties is missing namespace, image.tag, "+
			"endpoints.store, secrets.minio.name, secrets.minio.userKey, secrets.minio.passKey",
	)
}

func TestValidateAuthEnabledMissingSecrets(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	cfg.AuthEnabled = true
	cfg.Secrets.Auth.AppId = "id-1"
	err := cfg.validate()
	require.Error(t, err)
	require.EqualError(t, err, "authEnabled=true but secrets.auth is missing appSecret, jwksURI, jwtIssuer, jwtAudience")
}

func TestValidateIngressEnabledMissingFields(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	cfg.Ingress = IngressConfig{
		Enabled: true,
	}
	err := cfg.validate()
	require.Error(t, err)
	require.EqualError(t, err, "ingress.enabled=true but ingress is missing hosts, path, tlsSecret")
}

func TestValidateIngressEnabledComplete(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	//nolint:gosec // G101 false positive on struct field named TLSSecret.
	cfg.Ingress = IngressConfig{
		Enabled:   true,
		Hosts:     []string{"graphql.dictybase.dev"},
		Path:      "/",
		TLSSecret: "graphql-tls-cert",
	}
	require.NoError(t, cfg.validate())
}
