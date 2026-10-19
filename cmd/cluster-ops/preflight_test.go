package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func runPreflight(t *testing.T, args ...string) error {
	t.Helper()
	app := &cli.App{
		Name:           "preflight-test",
		Commands:       []*cli.Command{preflightCommand()},
		ExitErrHandler: func(*cli.Context, error) {}, // keep os.Exit out of tests
	}
	return app.Run(append([]string{"preflight-test", "preflight"}, args...))
}

func TestDeployerAccessNeedsCluster(t *testing.T) {
	t.Setenv("CLUSTER_NAME", "")
	err := runPreflight(t, "deployer-access", "--sa-key", "k.json")
	require.ErrorIs(t, err, errNoCluster)
}

func TestDeployerAccessUnknownCluster(t *testing.T) {
	err := runPreflight(t, "deployer-access", "--cluster", "nope", "--sa-key", "k.json",
		"--registry-dir", t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "registry entry")
}

func TestDeployerAccessClusterFromEnv(t *testing.T) {
	t.Setenv("CLUSTER_NAME", "from-env")
	err := runPreflight(t, "deployer-access", "--sa-key", "k.json", "--registry-dir", t.TempDir())
	require.Error(t, err)
	require.NotErrorIs(t, err, errNoCluster, "CLUSTER_NAME must default --cluster")
}

func TestDefaultDeployerKeyPath(t *testing.T) {
	require.Equal(t, filepath.Join("credentials", "dcr-kube1", "deployer.json"),
		defaultDeployerKeyPath("dcr-kube1", ""))
	require.Equal(t, "/custom/key.json",
		defaultDeployerKeyPath("dcr-kube1", "/custom/key.json"))
}

func TestBackendPrereqsNeedsStack(t *testing.T) {
	t.Setenv(envPulumiStack, "")
	err := runPreflight(t, "backend-prereqs", "--folder", "modware-order")
	require.ErrorIs(t, err, errNoStack)
}

func TestBackendPrereqsMissingStackFile(t *testing.T) {
	err := runPreflight(t, "backend-prereqs", "--folder", filepath.Join(t.TempDir(), "modware-order"),
		"--stack", "dcr-kube1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scaffold it first")
}

func TestPulumiBackendUnsetEnvExitsOne(t *testing.T) {
	for _, v := range []string{"PULUMI_GCP_CREDENTIALS", "PULUMI_SECRET_PROVIDER", "PULUMI_BACKEND_URL", "PULUMI_STACK"} {
		t.Setenv(v, "")
	}
	stdout := os.Stdout
	devnull, err := os.Open(os.DevNull)
	require.NoError(t, err)
	os.Stdout = devnull
	t.Cleanup(func() { os.Stdout = stdout; _ = devnull.Close() })

	err = runPreflight(t, "pulumi-backend")
	var exit cli.ExitCoder
	require.True(t, errors.As(err, &exit), "gate failure must be a cli.ExitCoder")
	require.Equal(t, 1, exit.ExitCode())
}

func TestPreflightDatastoreSubcommands(t *testing.T) {
	cmd := preflightCommand()
	names := map[string]bool{}
	for _, sub := range cmd.Subcommands {
		names[sub.Name] = true
	}
	for _, want := range []string{"pool", "arangodb", "arango-app-credentials", "postgres", "redis", "minio", "nats"} {
		require.True(t, names[want], "missing preflight subcommand %s", want)
	}
}

func TestPoolRequiresKubeconfigContext(t *testing.T) {
	// kube.NewClients with no cluster fails: API-error exit, not a flag error.
	err := runPreflight(t, "pool", "--pool", "database", "--count", "3")
	require.Error(t, err)
}

func TestArangodbVerifyFlagsRequired(t *testing.T) {
	require.Error(t, runPreflight(t, "arangodb"), "arangodb needs --namespace")
	require.Error(t, runPreflight(t, "arangodb", "--namespace", "prod"), "arangodb needs --members etc.")
}

// An empty wrapper placeholder must fall back to the cluster-shell
// environment (urfave treats explicitly-empty flags as set).
func TestResolveNameFallsBackToEnv(t *testing.T) {
	t.Setenv("CLUSTER_NAME", "from-env")
	require.Equal(t, "explicit", resolveName("explicit", "CLUSTER_NAME"))
	require.Equal(t, "from-env", resolveName("", "CLUSTER_NAME"))
	t.Setenv("CLUSTER_NAME", "")
	require.Empty(t, resolveName("", "CLUSTER_NAME"))
}
