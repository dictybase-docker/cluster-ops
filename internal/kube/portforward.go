package kube

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// ForwardOpts selects the port-forward target.
type ForwardOpts struct {
	Namespace  string
	Service    string
	RemotePort int
	LocalPort  int // 0 = free port
	// OnReady, when set, is called after every successful dial (including
	// reconnects) with the local port and the pod behind the forward.
	OnReady func(localPort int, pod string)
}

// Session is a live port-forward. Close it exactly once (via
// IOE.WithResource or defer); the supervisor goroutine stops with it.
type Session struct {
	LocalPort int
	cancel    context.CancelFunc
	done      chan struct{} // closed when the supervisor gives up
	ready     chan struct{} // closed on the first successful dial
	readyOnce sync.Once
	failed    chan error // final, non-retryable supervisor error
}

// dialFunc dials the forward once on localPort and holds it until it ends or
// ctx is cancelled, calling onReady(pod) once the forward is up.
type dialFunc func(ctx context.Context, localPort int, onReady func(pod string)) error

// Close stops the forward and its reconnect supervisor.
func (s *Session) Close() {
	s.cancel()
	<-s.done
}

// Done is closed when the supervisor gives up (retries exhausted or parent
// context cancelled). The child process sees EOF on the local socket then.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err reports the final supervisor error after Done is closed.
func (s *Session) Err() error {
	select {
	case err := <-s.failed:
		return err
	default:
		return nil
	}
}

// reconnectAttempts and reconnectBackoff are the phase-1 defaults: 3 attempts
// with 2s/4s/8s exponential backoff.
const reconnectAttempts = 3

func backoffSchedule(attempts int) []time.Duration {
	out := make([]time.Duration, attempts)
	for i := range out {
		out[i] = time.Duration(1<<(i+1)) * time.Second
	}
	return out
}

// retryable reports whether a failed dial attempt is worth retrying:
// NotFound/Forbidden/Unauthorized are config or RBAC errors (fail fast);
// everything else (pod restart, network blip) retries.
func retryable(_ error, reason Reason) bool {
	switch reason {
	case ReasonNotFound, ReasonForbidden, ReasonUnauthorized:
		return false
	default:
		return true
	}
}

// Forward opens a port-forward to a Service, resolving to the first ready
// pod. LocalPort 0 → free port (returned in Session). On pod loss the
// supervisor re-resolves the pod and re-dials the SAME local port — the child
// process keeps dialing one address — up to reconnectAttempts with
// exponential backoff. NotFound/Forbidden/Unauthorized fail fast.
func Forward(ctx context.Context, clients Clients, opts ForwardOpts) IOE.IOEither[error, *Session] {
	return IOE.TryCatchError(func() (*Session, error) {
		localPort, err := pickLocalPort(opts.LocalPort)
		if err != nil {
			return nil, err
		}
		dial := func(ctx context.Context, port int, onReady func(string)) error {
			return dialOnce(ctx, clients, opts, port, onReady)
		}
		sess, err := supervise(ctx, dial, localPort, opts.OnReady, backoffSchedule(reconnectAttempts))
		if err != nil {
			return nil, fmt.Errorf("%s/%s:%d: %w", opts.Namespace, opts.Service, opts.RemotePort, err)
		}
		return sess, nil
	})
}

// supervise starts the reconnect loop and blocks until the first dial is
// ready, the loop fails without retry, or ctx is cancelled.
func supervise(
	ctx context.Context,
	dial dialFunc,
	localPort int,
	onReady func(localPort int, pod string),
	backoff []time.Duration,
) (*Session, error) {
	sctx, cancel := context.WithCancel(ctx)
	s := &Session{
		LocalPort: localPort,
		cancel:    cancel,
		done:      make(chan struct{}),
		ready:     make(chan struct{}),
		failed:    make(chan error, 1),
	}
	go s.loop(sctx, dial, onReady, backoff)

	// Block until the first dial is ready, the parent cancels, or the
	// supervisor fails without retry.
	select {
	case <-s.ready:
		return s, nil
	case err := <-s.failed:
		cancel()
		return nil, err
	case <-ctx.Done():
		cancel()
		<-s.done
		return nil, ctx.Err()
	}
}

// loop re-dials after every drop. The retry budget counts consecutive failed
// dials; a dial that reached ready resets it, so a long-lived forward
// survives any number of separate pod restarts.
func (s *Session) loop(
	ctx context.Context,
	dial dialFunc,
	onReady func(localPort int, pod string),
	backoff []time.Duration,
) {
	defer close(s.done)
	for attempt := 0; ; attempt++ {
		connected := false
		err := dial(ctx, s.LocalPort, func(pod string) {
			connected = true
			s.readyOnce.Do(func() { close(s.ready) })
			if onReady != nil {
				onReady(s.LocalPort, pod)
			}
		})
		if ctx.Err() != nil {
			return // parent cancelled: not an error, just shutdown
		}
		if connected {
			attempt = 0
		}
		if !retryable(err, Classify(err)) || attempt >= len(backoff)-1 {
			s.failed <- fmt.Errorf(
				"port-forward after %d attempt(s): %w", attempt+1, err,
			)
			return
		}
		select {
		case <-time.After(backoff[attempt]):
		case <-ctx.Done():
			return
		}
	}
}

// dialOnce resolves the Service to a ready pod, dials the port-forward and
// holds it until the forward ends or ctx is cancelled.
//
//nolint:cyclop // single linear select sequence; branches mirror the forward lifecycle
func dialOnce(
	ctx context.Context,
	clients Clients,
	opts ForwardOpts,
	localPort int,
	onReady func(pod string),
) error {
	pod, err := resolvePod(ctx, clients.Kube, opts.Namespace, opts.Service)
	if err != nil {
		return err
	}

	restURL, err := url.Parse(clients.Rest.Host)
	if err != nil {
		return fmt.Errorf("parse API server host %q: %w", clients.Rest.Host, err)
	}
	restURL.Path = fmt.Sprintf(
		"/api/v1/namespaces/%s/pods/%s/portforward", opts.Namespace, pod,
	)
	transport, upgrader, err := spdy.RoundTripperFor(clients.Rest)
	if err != nil {
		return fmt.Errorf("SPDY transport: %w", err)
	}

	readyCh := make(chan struct{})
	stopCh := make(chan struct{})
	errCh := make(chan error, 1)
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, restURL)
	fw, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, opts.RemotePort)},
		stopCh,
		readyCh,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		return fmt.Errorf("port-forward %s/%s: %w", opts.Namespace, opts.Service, err)
	}
	go func() { errCh <- fw.ForwardPorts() }()

	select {
	case <-readyCh:
		if onReady != nil {
			onReady(pod)
		}
	case <-ctx.Done():
		close(stopCh)
		return ctx.Err()
	case err := <-errCh:
		return err
	}
	// Hold until the forward ends (pod death, protocol error) or shutdown.
	select {
	case err := <-errCh:
		close(stopCh)
		return err
	case <-ctx.Done():
		close(stopCh)
		return ctx.Err()
	}
}

// pickLocalPort returns localPort when free, or a fresh ephemeral port when
// localPort is 0.
func pickLocalPort(localPort int) (int, error) {
	if localPort != 0 {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
		if err != nil {
			return 0, fmt.Errorf("local port %d in use: %w", localPort, err)
		}
		return localPort, l.Close()
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("no free local port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port //nolint:errcheck // *net.TCPAddr type assertion cannot fail
	return port, l.Close()
}

// resolvePod resolves a Service to its first ready backing pod. A missing
// Service fails fast (NotFound); a Service without Endpoints or ready pods is
// a retryable "no ready pods" error — pods may not be scheduled yet.
func resolvePod(ctx context.Context, kube kubernetes.Interface, ns, svc string) (string, error) {
	if _, err := kube.CoreV1().Services(ns).Get(ctx, svc, metav1.GetOptions{}); err != nil {
		return "", err
	}
	eps, err := kube.CoreV1().Endpoints(ns).Get(ctx, svc, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("no ready pods for service %s/%s: %w", ns, svc, err)
	}
	for _, subset := range eps.Subsets {
		for _, addr := range subset.Addresses {
			const podKind = "Pod"
			if addr.TargetRef != nil && addr.TargetRef.Kind == podKind && addr.TargetRef.Name != "" {
				return addr.TargetRef.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no ready pods for service %s/%s", ns, svc)
}
