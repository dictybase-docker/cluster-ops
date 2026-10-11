package main

import (
	"bytes"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/pulumiauto"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// dashFolder is the shared --folder flag literal in the help contracts.
const (
	dashFolder = "--folder"
	dashStack  = "--stack"
)

func stackTestApp() *cli.App {
	app := &cli.App{
		Name:           appName,
		Commands:       []*cli.Command{stackCommand()},
		ExitErrHandler: func(*cli.Context, error) {},
	}
	return app
}

func stackHelp(t *testing.T, args ...string) string {
	t.Helper()
	app := stackTestApp()
	var buf bytes.Buffer
	app.Writer = &buf
	require.NoError(t, app.Run(append([]string{appName, "stack"}, args...)))
	return buf.String()
}

// Review Focus: the flag contracts the wrappers pass argv against.
func TestStackTreeFlagContract(t *testing.T) {
	for sub, flags := range map[string][]string{
		"ensure":  {dashFolder, dashStack, "--create", "--copy-config-from"},
		"preview": {dashFolder, dashStack, "--allow-protected"},
		"up":      {dashFolder, dashStack, "--dry-run", "--allow-protected"},
		"destroy": {dashFolder, dashStack, "--dry-run"},
		"outputs": {dashFolder, dashStack, "--key"},
	} {
		out := stackHelp(t, sub, "--help")
		for _, flag := range flags {
			require.Contains(t, out, flag, "%s must accept %s", sub, flag)
		}
	}
}

// The dry-run and protected-override switches map onto DeployMode.
func TestDeployModeFromFlags(t *testing.T) {
	require.Equal(t, pulumiauto.DeployMode{}, deployModeFrom(false, false))
	require.Equal(t, pulumiauto.DeployMode{DryRun: true}, deployModeFrom(true, false))
	require.Equal(t, pulumiauto.DeployMode{AllowProtected: true}, deployModeFrom(false, true))
}
