package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// runForward executes the forward command tree; keeps os.Exit out of tests.
func runForward(t *testing.T, args ...string) error {
	t.Helper()
	app := &cli.App{
		Name:           "forward-test",
		Commands:       []*cli.Command{forwardCommand()},
		ExitErrHandler: func(*cli.Context, error) {},
	}
	return app.Run(append([]string{"forward-test", "forward"}, args...))
}

func TestForwardRunRequiresService(t *testing.T) {
	err := runForward(t, "run", "--ns", "prod", "--remote-port", "8529", "--", "sleep", "0")
	require.Error(t, err, "--svc is required")
}

func TestForwardRunRequiresCommand(t *testing.T) {
	err := runForward(t, "run", "--ns", "prod", "--svc", "arangodb", "--remote-port", "8529")
	require.Error(t, err, "command after -- is required")
}

func TestChildEnvExportsForwardVars(t *testing.T) {
	env := childEnv(os.Environ(), 43121)
	require.Contains(t, env, "FORWARD_PORT=43121")
	require.Contains(t, env, "FORWARD_ADDR=127.0.0.1:43121")
	require.Len(t, env, len(os.Environ())+2)
}

func TestExitCodeFromChild(t *testing.T) {
	cmd := exec.Command("false")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitCodeFrom(err), "child exit code propagates")
	require.Equal(t, 1, exitCodeFrom(errors.New("plain")), "non-exit errors map to 1")
}

func TestForwardHoldRequiresService(t *testing.T) {
	err := runForward(t, "hold", "--ns", "dev", "--remote-port", "9000")
	require.Error(t, err, "--svc is required")
}
