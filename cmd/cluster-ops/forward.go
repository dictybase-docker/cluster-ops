package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/urfave/cli/v2"
)

// childWaitDelay bounds how long a signalled child may linger before SIGKILL.
const childWaitDelay = 5 * time.Second

func kubeFlags() []cli.Flag {
	return []cli.Flag{
		kubeconfigFlag(),
		&cli.StringFlag{
			Name:  "context",
			Usage: "Kubeconfig context (defaults to current-context)",
		},
	}
}

func forwardCommand() *cli.Command {
	return &cli.Command{
		Name:  cmdForward,
		Usage: "Port-forward lifecycle for recipes and interactive dev access",
		Subcommands: []*cli.Command{
			{
				Name:  cmdForwardRun,
				Usage: "Forward to a Service, run a command with FORWARD_PORT exported, tear down on exit",
				Flags: append(kubeFlags(),
					&cli.StringFlag{Name: flagNS, Aliases: []string{"n"}, Required: true, Usage: usageNamespace},
					&cli.StringFlag{Name: flagSVC, Aliases: []string{"s"}, Required: true, Usage: "Service name"},
					&cli.IntFlag{Name: flagRemotePort, Required: true, Usage: "Remote port on the pod"},
					&cli.IntFlag{
						Name:  "local-port",
						Value: 0,
						Usage: "Local port (0 = free port, exported to the child)",
					},
				),
				Action: forwardRunAction,
			},
			{
				Name:  cmdForwardHold,
				Usage: "Hold a foreground forward (Ctrl-C to stop); reconnects on pod restart",
				Flags: append(kubeFlags(),
					&cli.StringFlag{Name: flagNS, Aliases: []string{"n"}, Required: true, Usage: usageNamespace},
					&cli.StringFlag{Name: flagSVC, Aliases: []string{"s"}, Required: true, Usage: "Service name"},
					&cli.IntFlag{Name: flagRemotePort, Required: true, Usage: "Remote port on the pod"},
					&cli.IntFlag{
						Name:  "local-port",
						Value: 0,
						Usage: "Local port (0 = free port; fixed port for mc alias compatibility)",
					},
				),
				Action: forwardHoldAction,
			},
		},
	}
}

func forwardOpts(c *cli.Context) kube.ForwardOpts {
	return kube.ForwardOpts{
		Namespace:  c.String("ns"),
		Service:    c.String("svc"),
		RemotePort: c.Int("remote-port"),
		LocalPort:  c.Int("local-port"),
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// childEnv exports the forward address to the child process.
func childEnv(parent []string, localPort int) []string {
	return append(
		parent,
		fmt.Sprintf("FORWARD_PORT=%d", localPort),
		fmt.Sprintf("FORWARD_ADDR=127.0.0.1:%d", localPort),
	)
}

// exitCodeFrom maps a child execution error to a process exit code.
func exitCodeFrom(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return 1
}

// runChild spawns cmd with the forward address exported and waits for it. On
// cancellation the child gets SIGTERM, childWaitDelay to flush, then SIGKILL.
func runChild(ctx context.Context, args []string, localPort int) error {
	//nolint:gosec // G204: the child command is the caller's explicit CLI argument
	child := exec.CommandContext(ctx, args[0], args[1:]...)
	child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
	child.WaitDelay = childWaitDelay
	child.Env = childEnv(os.Environ(), localPort)
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	return child.Run()
}

func newClientsIOE(c *cli.Context) IOE.IOEither[error, kube.Clients] {
	return IOE.FromEither(kube.NewClients(c.String(flagKubeconfig), c.String("context")))
}

func exitOrWrap(prefix string, err error) error {
	if err == nil {
		return nil
	}
	if exit, ok := errors.AsType[cli.ExitCoder](err); ok {
		return exit
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func forwardRunAction(c *cli.Context) error {
	args := c.Args().Slice()
	if len(args) == 0 {
		return errors.New("no command specified after --")
	}
	ctx, stop := signalContext()
	defer stop()

	runChildOnSession := func(sess *kube.Session) IOE.IOEither[error, struct{}] {
		return IOE.TryCatchError(func() (struct{}, error) {
			defer sess.Close()
			childCtx, cancelChild := childContext(ctx, sess.Done())
			defer cancelChild()
			if err := runChild(childCtx, args, sess.LocalPort); err != nil {
				return struct{}{}, supervisorErrOr(cli.Exit("", exitCodeFrom(err)), sess.Err())
			}
			return struct{}{}, nil
		})
	}
	return openForward(c, ctx, forwardOpts(c), runChildOnSession, "forward run")
}

// childContext scopes the forwarded child to its forward: it is canceled by
// the parent (signals) or when the supervisor gives up (done closes), so a
// dead forward never leaves an orphan child running.
func childContext(ctx context.Context, done <-chan struct{}) (context.Context, context.CancelFunc) {
	childCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-done:
			cancel()
		case <-childCtx.Done():
		}
	}()
	return childCtx, cancel
}

// supervisorErrOr prioritizes the supervisor's error over the child's exit
// status: when the forward died under a failing child, the dead forward is
// the root cause.
func supervisorErrOr(childErr, sessErr error) error {
	if sessErr != nil {
		return fmt.Errorf("forward ended while the child ran: %w", sessErr)
	}
	return childErr
}

func forwardHoldAction(c *cli.Context) error {
	ctx, stop := signalContext()
	defer stop()

	opts := forwardOpts(c)
	// Set before Forward starts: the supervisor calls it from its own
	// goroutine on the first dial and on every reconnect.
	opts.OnReady = func(localPort int, pod string) {
		fmt.Fprintf(os.Stderr, "Forwarding from 127.0.0.1:%d -> %s:%d (pod %s)\n",
			localPort, opts.Service, opts.RemotePort, pod)
	}
	holdSession := func(sess *kube.Session) IOE.IOEither[error, struct{}] {
		return IOE.TryCatchError(func() (struct{}, error) {
			defer sess.Close()
			select {
			case <-ctx.Done():
				fmt.Fprintln(os.Stderr, "Shutting down…")
				return struct{}{}, nil
			case <-sess.Done():
				if err := sess.Err(); err != nil {
					fmt.Fprintf(os.Stderr, "Reconnecting… exhausted: %v\n", err)
					return struct{}{}, cli.Exit("", 1)
				}
				return struct{}{}, nil
			}
		})
	}
	return openForward(c, ctx, opts, holdSession, "forward hold")
}

// openForward opens the forward, runs use on the live session and maps the
// outcome to the CLI error contract.
func openForward(
	c *cli.Context,
	ctx context.Context,
	opts kube.ForwardOpts,
	use func(*kube.Session) IOE.IOEither[error, struct{}],
	prefix string,
) error {
	return exitOrWrap(prefix, F.Pipe3(
		newClientsIOE(c),
		IOE.Chain(func(clients kube.Clients) IOE.IOEither[error, *kube.Session] {
			return kube.Forward(ctx, clients, opts)
		}),
		IOE.Chain(use),
		IOE.Fold(
			IO.Of[error],
			func(struct{}) IO.IO[error] { return IO.Of[error](nil) },
		),
	)())
}
