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

	A "github.com/IBM/fp-go/v2/array"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	L "github.com/IBM/fp-go/v2/optics/lens"
	O "github.com/IBM/fp-go/v2/option"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
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

const podTargetKind = "Pod"

var (
	listOptsLabelSelLens = L.MakeLens(
		func(o metav1.ListOptions) string { return o.LabelSelector },
		func(o metav1.ListOptions, v string) metav1.ListOptions {
			o.LabelSelector = v
			return o
		},
	)

	sliceListItemsLens = L.MakeLensRef(
		func(l *discoveryv1.EndpointSliceList) []discoveryv1.EndpointSlice {
			return l.Items
		},
		func(
			l *discoveryv1.EndpointSliceList,
			v []discoveryv1.EndpointSlice,
		) *discoveryv1.EndpointSliceList {
			l.Items = v
			return l
		},
	)

	sliceEndpointsLens = L.MakeLens(
		func(s discoveryv1.EndpointSlice) []discoveryv1.Endpoint { return s.Endpoints },
		func(
			s discoveryv1.EndpointSlice,
			v []discoveryv1.Endpoint,
		) discoveryv1.EndpointSlice {
			s.Endpoints = v
			return s
		},
	)

	endpointCondsLens = L.MakeLens(
		func(e discoveryv1.Endpoint) discoveryv1.EndpointConditions {
			return e.Conditions
		},
		func(
			e discoveryv1.Endpoint,
			v discoveryv1.EndpointConditions,
		) discoveryv1.Endpoint {
			e.Conditions = v
			return e
		},
	)

	condReadyLens = L.MakeLens(
		func(c discoveryv1.EndpointConditions) *bool { return c.Ready },
		func(
			c discoveryv1.EndpointConditions,
			v *bool,
		) discoveryv1.EndpointConditions {
			c.Ready = v
			return c
		},
	)

	endpointTargetRefLens = L.MakeLens(
		func(e discoveryv1.Endpoint) *corev1.ObjectReference { return e.TargetRef },
		func(
			e discoveryv1.Endpoint,
			v *corev1.ObjectReference,
		) discoveryv1.Endpoint {
			e.TargetRef = v
			return e
		},
	)

	objRefKindLens = L.MakeLensRef(
		func(r *corev1.ObjectReference) string { return r.Kind },
		func(r *corev1.ObjectReference, v string) *corev1.ObjectReference {
			r.Kind = v
			return r
		},
	)

	objRefNameLens = L.MakeLensRef(
		func(r *corev1.ObjectReference) string { return r.Name },
		func(r *corev1.ObjectReference, v string) *corev1.ObjectReference {
			r.Name = v
			return r
		},
	)

	isEndpointReady = F.Flow3(
		F.Flow2(endpointCondsLens.Get, condReadyLens.Get),
		O.FromNillable2[bool],
		O.GetOrElse(F.Constant(true)),
	)

	isPodTargetRef = F.Pipe1(
		P.IsStrictEqual[string]()(podTargetKind),
		P.ContraMap(objRefKindLens.Get),
	)

	readyPodNameFromEndpoint = F.Flow5(
		O.FromPredicate(isEndpointReady),
		O.Chain(F.Flow2(endpointTargetRefLens.Get, O.FromPredicate(P.IsNonZero[*corev1.ObjectReference]()))),
		O.Chain(O.FromPredicate(isPodTargetRef)),
		O.Map(objRefNameLens.Get),
		O.Chain(O.FromPredicate(S.IsNonEmpty)),
	)

	firstReadyPodInSlices = F.Flow3(
		sliceListItemsLens.Get,
		A.Chain(sliceEndpointsLens.Get),
		A.FindFirstMap(readyPodNameFromEndpoint),
	)
)

func listServiceEndpointSlices(
	ctx context.Context,
	kube kubernetes.Interface,
	ns string,
	svc string,
) IOE.IOEither[error, *discoveryv1.EndpointSliceList] {
	return F.Pipe1(
		IOE.TryCatchError(func() (*discoveryv1.EndpointSliceList, error) {
			return kube.DiscoveryV1().EndpointSlices(ns).List(
				ctx,
				F.Pipe1(
					metav1.ListOptions{},
					listOptsLabelSelLens.Set(discoveryv1.LabelServiceName+"="+svc),
				),
			)
		}),
		IOE.MapLeft[*discoveryv1.EndpointSliceList](func(err error) error {
			return fmt.Errorf("no ready pods for service %s/%s: %w", ns, svc, err)
		}),
	)
}

// resolvePod resolves a Service to its first ready backing pod via
// discoveryv1.EndpointSlice. A missing Service fails fast (NotFound); a Service
// without EndpointSlices or ready pods is a retryable "no ready pods" error —
// pods may not be scheduled yet.
func resolvePod(
	ctx context.Context,
	kube kubernetes.Interface,
	ns string,
	svc string,
) (string, error) {
	return E.UnwrapError(
		F.Pipe3(
			IOE.TryCatchError(func() (*corev1.Service, error) {
				return kube.CoreV1().Services(ns).Get(ctx, svc, metav1.GetOptions{})
			}),
			IOE.Chain(
				F.Constant1[*corev1.Service](
					listServiceEndpointSlices(ctx, kube, ns, svc),
				),
			),
			IOE.Map[error](firstReadyPodInSlices),
			IOE.ChainEitherK(
				E.FromOption[string](func() error {
					return fmt.Errorf("no ready pods for service %s/%s", ns, svc)
				}),
			),
		)(),
	)
}
