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

	A "github.com/IBM/fp-go/v2/array"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	L "github.com/IBM/fp-go/v2/optics/lens"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/urfave/cli/v2"
)

// childWaitDelay bounds how long a signalled child may linger before SIGKILL.
const childWaitDelay = 5 * time.Second

var (
	forwardNSLens = L.MakeLens(
		func(o kube.ForwardOpts) string { return o.Namespace },
		func(o kube.ForwardOpts, v string) kube.ForwardOpts { o.Namespace = v; return o },
	)
	forwardSVCLens = L.MakeLens(
		func(o kube.ForwardOpts) string { return o.Service },
		func(o kube.ForwardOpts, v string) kube.ForwardOpts { o.Service = v; return o },
	)
	forwardRemotePortLens = L.MakeLens(
		func(o kube.ForwardOpts) int { return o.RemotePort },
		func(o kube.ForwardOpts, v int) kube.ForwardOpts { o.RemotePort = v; return o },
	)
	forwardLocalPortLens = L.MakeLens(
		func(o kube.ForwardOpts) int { return o.LocalPort },
		func(o kube.ForwardOpts, v int) kube.ForwardOpts { o.LocalPort = v; return o },
	)
	forwardPersistentLens = L.MakeLens(
		func(o kube.ForwardOpts) bool { return o.PersistentReconnect },
		func(o kube.ForwardOpts, v bool) kube.ForwardOpts {
			o.PersistentReconnect = v
			return o
		},
	)
	forwardOnReadyLens = L.MakeLens(
		func(o kube.ForwardOpts) func(int, string) { return o.OnReady },
		func(o kube.ForwardOpts, v func(int, string)) kube.ForwardOpts {
			o.OnReady = v
			return o
		},
	)
	childSpecCtxLens = L.MakeLens(
		func(s childRunSpec) context.Context { return s.ctx },
		func(s childRunSpec, v context.Context) childRunSpec { s.ctx = v; return s },
	)
	childSpecArgsLens = L.MakeLens(
		func(s childRunSpec) []string { return s.args },
		func(s childRunSpec, v []string) childRunSpec { s.args = v; return s },
	)
	startReqCtxLens = L.MakeLens(
		func(r startForwardReq) context.Context { return r.ctx },
		func(r startForwardReq, v context.Context) startForwardReq { r.ctx = v; return r },
	)
	startReqOptsLens = L.MakeLens(
		func(r startForwardReq) kube.ForwardOpts { return r.opts },
		func(r startForwardReq, v kube.ForwardOpts) startForwardReq { r.opts = v; return r },
	)
	forwardEndpointSVCLens = L.MakeLens(
		func(e forwardEndpoint) string { return e.svc },
		func(e forwardEndpoint, v string) forwardEndpoint { e.svc = v; return e },
	)
	forwardEndpointPortLens = L.MakeLens(
		func(e forwardEndpoint) int { return e.remotePort },
		func(e forwardEndpoint, v int) forwardEndpoint { e.remotePort = v; return e },
	)
)

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
	return F.Pipe4(
		kube.ForwardOpts{},
		forwardNSLens.Set(c.String("ns")),
		forwardSVCLens.Set(c.String("svc")),
		forwardRemotePortLens.Set(c.Int("remote-port")),
		forwardLocalPortLens.Set(c.Int("local-port")),
	)
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// childEnv exports the forward address to the child process.
func childEnv(parent []string, localPort int) []string {
	return A.ArrayConcatAll(
		parent,
		[]string{
			fmt.Sprintf("FORWARD_PORT=%d", localPort),
			fmt.Sprintf("FORWARD_ADDR=127.0.0.1:%d", localPort),
		},
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

func exitOrWrap(prefix string, err error) error {
	if err == nil {
		return nil
	}
	if exit, ok := errors.AsType[cli.ExitCoder](err); ok {
		return exit
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

type childRunSpec struct {
	ctx  context.Context
	args []string
}

func runChildSession(spec childRunSpec, sess *kube.Session) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		defer sess.Close()
		childCtx, cancelChild := childContext(spec.ctx, sess.Done())
		defer cancelChild()
		if err := runChild(childCtx, spec.args, sess.LocalPort); err != nil {
			return struct{}{}, supervisorErrOr(cli.Exit("", exitCodeFrom(err)), sess.Err())
		}
		return struct{}{}, nil
	})
}

func forwardRunAction(c *cli.Context) error {
	args := c.Args().Slice()
	if len(args) == 0 {
		return errors.New("no command specified after --")
	}
	ctx, stop := signalContext()
	defer stop()

	spec := F.Pipe2(
		childRunSpec{},
		childSpecCtxLens.Set(ctx),
		childSpecArgsLens.Set(args),
	)
	return openForward(c, ctx, forwardOpts(c), F.Bind1st(runChildSession, spec), "forward run")
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

type forwardEndpoint struct {
	svc        string
	remotePort int
}

func (ep forwardEndpoint) logReady(localPort int, pod string) {
	fmt.Fprintf(
		os.Stderr,
		"Forwarding from 127.0.0.1:%d -> %s:%d (pod %s)\n",
		localPort,
		forwardEndpointSVCLens.Get(ep),
		forwardEndpointPortLens.Get(ep),
		pod,
	)
}

func forwardHoldOpts(c *cli.Context) kube.ForwardOpts {
	base := forwardOpts(c)
	ep := F.Pipe2(
		forwardEndpoint{},
		forwardEndpointSVCLens.Set(forwardSVCLens.Get(base)),
		forwardEndpointPortLens.Set(forwardRemotePortLens.Get(base)),
	)
	return F.Pipe2(
		base,
		forwardPersistentLens.Set(true),
		forwardOnReadyLens.Set(ep.logReady),
	)
}

func holdForwardSession(ctx context.Context, sess *kube.Session) IOE.IOEither[error, struct{}] {
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

func forwardHoldAction(c *cli.Context) error {
	ctx, stop := signalContext()
	defer stop()

	return openForward(c, ctx, forwardHoldOpts(c), F.Bind1st(holdForwardSession, ctx), "forward hold")
}

type startForwardReq struct {
	ctx  context.Context
	opts kube.ForwardOpts
}

func startForward(req startForwardReq, clients kube.Clients) IOE.IOEither[error, *kube.Session] {
	return kube.Forward(startReqCtxLens.Get(req), clients, startReqOptsLens.Get(req))
}

func newClientsFromCLI(c *cli.Context) E.Either[error, kube.Clients] {
	return kube.NewClients(c.String(flagKubeconfig), c.String("context"))
}

func newClientsIOE(c *cli.Context) IOE.IOEither[error, kube.Clients] {
	return F.Pipe1(
		IOE.Of[error](c),
		IOE.ChainEitherK(newClientsFromCLI),
	)
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
	req := F.Pipe2(
		startForwardReq{},
		startReqCtxLens.Set(ctx),
		startReqOptsLens.Set(opts),
	)
	eval := F.Pipe4(
		IOE.Of[error](c),
		IOE.ChainEitherK(newClientsFromCLI),
		IOE.Chain(F.Bind1st(startForward, req)),
		IOE.Chain(use),
		IOE.Fold(
			IO.Of[error],
			F.Constant1[struct{}](IO.Of[error](nil)),
		),
	)
	return exitOrWrap(prefix, eval())
}
