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

func TestBackendPrereqsNeedsStack(t *testing.T) {
	t.Setenv("PULUMI_STACK", "")
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
