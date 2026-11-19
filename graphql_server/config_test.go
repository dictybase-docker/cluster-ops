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

func ncbiConfig() *GraphqlServerConfig {
	cfg := validBaseConfig()
	//nolint:gosec // G101 false positive on test fixture values.
	cfg.Secrets.Ncbi.Values = NcbiFields{
		APIKey: "test-ncbi-key",
		Email:  "dev@example.org",
		Tool:   "graphql-server",
	}
	return cfg
}

func TestValidateNcbiUnconfiguredStaysValid(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	require.False(t, cfg.Secrets.Ncbi.Enabled())
	require.NoError(t, cfg.validate())
}

func TestNcbiDefaults(t *testing.T) {
	t.Parallel()
	ncbi := validBaseConfig().Secrets.Ncbi
	require.Equal(t, ncbiSecretName, ncbi.Name)
	require.Equal(t, NcbiFields{
		APIKey: ncbiKeyAPIKey,
		Email:  ncbiKeyEmail,
		Tool:   ncbiKeyTool,
	}, ncbi.Keys)
}

func TestValidateNcbiComplete(t *testing.T) {
	t.Parallel()
	require.NoError(t, ncbiConfig().validate())
}

func TestValidateNcbiRequiresAllValues(t *testing.T) {
	t.Parallel()
	cfg := ncbiConfig()
	cfg.Secrets.Ncbi.Values.Email = ""
	cfg.Secrets.Ncbi.Values.Tool = ""
	require.EqualError(
		t,
		cfg.validate(),
		"secrets.ncbi is partly set; properties is missing "+
			"secrets.ncbi.values.email, secrets.ncbi.values.tool",
	)
}
