package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// runWait executes the wait command tree; keeps os.Exit out of tests.
func runWait(t *testing.T, args ...string) error {
	t.Helper()
	app := &cli.App{
		Name:           "wait-test",
		Commands:       []*cli.Command{waitCommand()},
		ExitErrHandler: func(*cli.Context, error) {},
	}
	return app.Run(append([]string{"wait-test", "wait"}, args...))
}

func TestWaitSubcommandsRegistered(t *testing.T) {
	cmd := waitCommand()
	names := map[string]bool{}
	for _, sub := range cmd.Subcommands {
		names[sub.Name] = true
	}
	for _, want := range []string{
		"pods-ready", "job-complete", "resources-gone", "deployment-rollout",
		"custom-resource", "crd-established", "storage-classes", "nodes-ready",
	} {
		require.True(t, names[want], "missing subcommand %s", want)
	}
}

func TestWaitRequiresFlags(t *testing.T) {
	require.Error(t, runWait(t, "pods-ready"), "pods-ready needs --namespace/--selector")
	require.Error(t, runWait(t, "job-complete", "--namespace", "dev"), "job-complete needs --job")
	require.Error(t, runWait(t, "resources-gone", "--namespace", "dev"), "resources-gone needs --kind")
}

func TestWaitExitCodes(t *testing.T) {
	tests := []struct {
		kind waitfor.Kind
		want int
	}{
		{waitfor.KindTimeout, 1},
		{waitfor.KindTerminal, 2},
		{waitfor.KindAPI, 3},
	}
	for _, tt := range tests {
		err := fmt.Errorf("wrapped: %w", &waitfor.Error{Kind: tt.kind, Msg: "x"})
		require.Equal(t, tt.want, exitForWait(err), "kind %d", tt.kind)
	}
	require.Equal(t, 1, exitForWait(errors.New("plain")), "unknown errors exit 1")
}
