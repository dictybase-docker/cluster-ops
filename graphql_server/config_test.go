package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateDefaultsDisabled(t *testing.T) {
	t.Parallel()
	cfg := &GraphqlServerConfig{}
	require.NoError(t, cfg.validate())
}

func TestValidateAuthEnabledMissingSecrets(t *testing.T) {
	t.Parallel()
	cfg := &GraphqlServerConfig{
		AuthEnabled: true,
		Secrets: SecretsConfig{
			Auth: AuthConfig{
				AppId: "id-1",
			},
		},
	}
	err := cfg.validate()
	require.Error(t, err)
	require.EqualError(t, err, "authEnabled=true but secrets.auth is missing appSecret, jwksURI, jwtIssuer, jwtAudience")
}

func TestValidateIngressEnabledMissingFields(t *testing.T) {
	t.Parallel()
	cfg := &GraphqlServerConfig{
		Ingress: IngressConfig{
			Enabled: true,
		},
	}
	err := cfg.validate()
	require.Error(t, err)
	require.EqualError(t, err, "ingress.enabled=true but ingress is missing hosts, path, tlsSecret")
}

func TestValidateIngressEnabledComplete(t *testing.T) {
	t.Parallel()
	//nolint:gosec // G101 false positive on struct field named TLSSecret.
	cfg := &GraphqlServerConfig{
		Ingress: IngressConfig{
			Enabled:   true,
			Hosts:     []string{"graphql.dictybase.dev"},
			Path:      "/",
			TLSSecret: "graphql-tls-cert",
		},
	}
	require.NoError(t, cfg.validate())
}
