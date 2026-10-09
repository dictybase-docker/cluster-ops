package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

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
	return app.Run(append([]string{"forward-test", cmdForward}, args...))
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

// exitOrWrap must pass success through untouched: fmt.Errorf("%s: %w", prefix,
// nil) is itself non-nil, and a successful forward must exit 0.
func TestExitOrWrapPassesThroughSuccess(t *testing.T) {
	require.NoError(t, exitOrWrap("forward run", nil))
	require.NoError(t, exitOrWrap("forward hold", nil))
}

// The child must not outlive its forward: when the supervisor gives up (the
// reconnect budget is spent), the child context is canceled so the child gets
// SIGTERM, the childWaitDelay grace, then SIGKILL.
func TestRunChildStopsWhenForwardDies(t *testing.T) {
	done := make(chan struct{})
	ctx, cancel := childContext(context.Background(), done)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- runChild(ctx, []string{"sleep", "30"}, 45678) }()
	close(done) // the supervisor gives up
	select {
	case err := <-errCh:
		require.Error(t, err, "child must stop when the forward is gone")
	case <-time.After(5 * time.Second):
		t.Fatal("child outlived the forward")
	}
}

func TestChildContextCancelsWithParent(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	ctx, cancel := childContext(parent, make(chan struct{}))
	defer cancel()
	stop()
	require.Eventually(t, func() bool { return ctx.Err() != nil },
		2*time.Second, time.Millisecond)
}

// When the supervisor died under a failing child, the supervisor's error is
// the root cause and outranks the child's exit status.
func TestSupervisorErrOrPrioritizesSupervisorError(t *testing.T) {
	serr := errors.New("reconnects exhausted")
	childErr := errors.New("exit status 1")
	require.ErrorIs(t, supervisorErrOr(childErr, serr), serr)
	require.Equal(t, childErr, supervisorErrOr(childErr, nil))
	require.NoError(t, supervisorErrOr(nil, nil))
}
