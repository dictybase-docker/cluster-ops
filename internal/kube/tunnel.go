package kube

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	ttk8s "github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	ttesting "github.com/gruntwork-io/terratest/modules/testing"
)

// Tunnel wraps a terratest tunnel. Close it exactly once (via
// IOE.WithResource): terratest's Close closes a channel and panics on reuse.
type Tunnel struct {
	tt        *ttk8s.Tunnel
	LocalPort int
}

// Close stops the tunnel.
func (t *Tunnel) Close() IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		t.tt.Close()
		return struct{}{}, nil
	})
}

// OpenServiceTunnel port-forwards a free local port to remotePort of a
// Service. terratest resolves the Service to an available pod and blocks
// until the forward is ready.
func OpenServiceTunnel(
	clients Clients,
	namespace, service string,
	remotePort int,
) IOE.IOEither[error, *Tunnel] {
	return IOE.TryCatchError(func() (*Tunnel, error) {
		opts := &ttk8s.KubectlOptions{Namespace: namespace, RestConfig: clients.Rest}
		tt, err := runT(
			&testingAdapter{name: "tunnel " + namespace + "/" + service},
			func(t ttesting.TestingT) (*ttk8s.Tunnel, error) {
				tunnel := ttk8s.NewTunnelWithLogger(
					opts,
					ttk8s.ResourceTypeService,
					service,
					0, // free local port: never collides with a stale forward
					remotePort,
					logger.Discard,
				)
				return tunnel, tunnel.ForwardPortE(t)
			},
		)
		if err != nil {
			return nil, err
		}
		port, err := parsePort(tt.Endpoint())
		if err != nil {
			tt.Close()
			return nil, err
		}
		return &Tunnel{tt: tt, LocalPort: port}, nil
	})
}

// fatalErr is the sentinel panic value raised by Fatal/Fatalf/FailNow.
type fatalErr struct{ msg string }

// testingAdapter satisfies terratest's TestingT outside `go test`.
// Fatal/FailNow panic with fatalErr (runT recovers it); Error/Errorf are
// recorded and fail the call, matching the list-every-failure contract.
type testingAdapter struct {
	name   string
	errors []string
}

func (t *testingAdapter) Fail()             { t.errors = append(t.errors, t.name+": failed") }
func (t *testingAdapter) FailNow()          { panic(fatalErr{t.name + ": failed"}) }
func (t *testingAdapter) Fatal(args ...any) { panic(fatalErr{fmt.Sprint(args...)}) }
func (t *testingAdapter) Fatalf(format string, args ...any) {
	panic(fatalErr{fmt.Sprintf(format, args...)})
}
func (t *testingAdapter) Error(args ...any) { t.errors = append(t.errors, fmt.Sprint(args...)) }
func (t *testingAdapter) Errorf(format string, args ...any) {
	t.errors = append(t.errors, fmt.Sprintf(format, args...))
}
func (t *testingAdapter) Name() string { return t.name }
func (t *testingAdapter) Helper()      {}

// runT runs fn with the adapter. A fatalErr panic becomes the returned error;
// any other panic is a real bug and is re-raised.
func runT[A any](
	adapter *testingAdapter,
	fn func(ttesting.TestingT) (A, error),
) (result A, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		fe, ok := r.(fatalErr)
		if !ok {
			panic(r)
		}
		var zero A
		result, err = zero, errors.New(fe.msg)
	}()
	result, err = fn(adapter)
	if err == nil && len(adapter.errors) > 0 {
		var zero A
		return zero, errors.New(strings.Join(adapter.errors, "; "))
	}
	return result, err
}

// parsePort extracts the port from terratest's "localhost:PORT" endpoint.
func parsePort(endpoint string) (int, error) {
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return 0, fmt.Errorf("tunnel endpoint %q: %w", endpoint, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("tunnel endpoint %q: %w", endpoint, err)
	}
	return n, nil
}
