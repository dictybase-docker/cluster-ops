package backend_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
)

const testBackendURL = "gs://pulumi-state-dcr-kube1"

func fullPulumiCfg() backend.PulumiBackendConfig {
	return backend.PulumiBackendConfig{ //nolint:gosec // G101: fixture values, not credentials
		CredsPath:       "/creds/pulumi-manager.json",
		SecretsProvider: "gcpkms://projects/dcr-kube1/locations/us-east1/keyRings/r/cryptoKeys/k",
		BackendURL:      testBackendURL,
		Stack:           "dcr-kube1",
	}
}

func loginAs(url string) backend.LoginReader {
	return func() IOE.IOEither[error, string] { return IOE.Of[error](url) }
}

func TestCheckEnvVarsAllSet(t *testing.T) {
	require.Equal(t, preflight.StatusPass, backend.CheckEnvVars(fullPulumiCfg())().Status)
}

func TestCheckEnvVarsNamesEveryUnsetVar(t *testing.T) {
	cfg := fullPulumiCfg()
	cfg.Stack, cfg.BackendURL = "", ""
	result := backend.CheckEnvVars(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Equal(t, "unset: PULUMI_BACKEND_URL, PULUMI_STACK", result.Detail)
}

func TestCheckManagerKey(t *testing.T) {
	cfg := fullPulumiCfg()
	cfg.CredsPath = filepath.Join(t.TempDir(), "pulumi-manager.json")
	require.Equal(t, preflight.StatusFail, backend.CheckManagerKey(cfg)().Status)

	require.NoError(t, os.WriteFile(cfg.CredsPath, []byte("{}"), 0o600))
	require.Equal(t, preflight.StatusPass, backend.CheckManagerKey(cfg)().Status)
}

func TestDependentChecksSkipWhenInputUnset(t *testing.T) {
	cfg := backend.PulumiBackendConfig{}
	for _, check := range backend.PulumiChecks(cfg)[1:] {
		require.Equal(t, preflight.StatusInfo, check().Status)
	}
}

func TestSecretsProviderNonKMSIsInfo(t *testing.T) {
	cfg := fullPulumiCfg()
	cfg.SecretsProvider = "passphrase"
	require.Equal(t, preflight.StatusInfo, backend.CheckSecretsProviderKey(cfg)().Status)
}

func TestCheckPulumiLogin(t *testing.T) {
	cfg := fullPulumiCfg()
	require.Equal(t, preflight.StatusPass, backend.CheckPulumiLogin(cfg, loginAs(testBackendURL+"/"))().Status)

	result := backend.CheckPulumiLogin(cfg, loginAs("gs://pulumi-state-dcr-experiments"))()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "expected "+testBackendURL)

	// A substring match used to pass a login to a different bucket.
	result = backend.CheckPulumiLogin(cfg, loginAs(testBackendURL+"-old"))()
	require.Equal(t, preflight.StatusFail, result.Status)

	failing := func() IOE.IOEither[error, string] { return IOE.Left[string](errors.New("not logged in")) }
	require.Equal(t, preflight.StatusFail, backend.CheckPulumiLogin(cfg, failing)().Status)
}

func TestParseWhoami(t *testing.T) {
	got := backend.ParseWhoami([]byte(`{"user":"me","url":"gs://pulumi-state-dcr-kube1"}`))
	require.Equal(t, E.Right[error]("gs://pulumi-state-dcr-kube1"), got)
	require.True(t, E.IsLeft(backend.ParseWhoami([]byte(`{"user":"me"}`))))
	require.True(t, E.IsLeft(backend.ParseWhoami([]byte(`not json`))))
}
