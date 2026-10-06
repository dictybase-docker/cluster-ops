package kube

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// endpointsWith builds a Service + Endpoints object pair with the given
// ready and unready pod names. corev1.Endpoints is deprecated in favour of
// discoveryv1.EndpointSlice, but the fake clientset round-trips Endpoints
// written by the API server regardless.
func endpointsWith(ns, svc string, ready, unready []string) []runtime.Object {
	svcObj := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: svc},
	}
	eps := &corev1.Endpoints{ //nolint:staticcheck // SA1019: see doc comment above
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: svc},
	}
	subset := corev1.EndpointSubset{} //nolint:staticcheck // SA1019: see endpointsWith comment
	for _, pod := range ready {
		subset.Addresses = append(subset.Addresses, corev1.EndpointAddress{
			IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: pod}, //nolint:goconst // fixture literal
		})
	}
	for _, pod := range unready {
		subset.NotReadyAddresses = append(subset.NotReadyAddresses, corev1.EndpointAddress{
			IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: pod}, //nolint:goconst // fixture literal
		})
	}
	if len(ready) > 0 || len(unready) > 0 {
		eps.Subsets = []corev1.EndpointSubset{subset} //nolint:staticcheck // SA1019: see endpointsWith comment
	}
	return []runtime.Object{svcObj, eps}
}

func TestResolvePodFirstReady(t *testing.T) {
	cs := fake.NewSimpleClientset(endpointsWith("prod", "arangodb",
		[]string{"arangodb-0", "arangodb-1"}, []string{"arangodb-2"})...)
	pod, err := resolvePod(context.Background(), cs, "prod", "arangodb")
	require.NoError(t, err)
	require.Equal(t, "arangodb-0", pod, "first ready pod wins")
}

func TestResolvePodSkipsUnready(t *testing.T) {
	cs := fake.NewSimpleClientset(endpointsWith("prod", "db", nil, []string{"db-0"})...)
	_, err := resolvePod(context.Background(), cs, "prod", "db")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no ready pods")
}

func TestResolvePodMissingService(t *testing.T) {
	cs := fake.NewSimpleClientset()
	_, err := resolvePod(context.Background(), cs, "prod", "nope")
	require.Error(t, err)
	require.Equal(t, ReasonNotFound, Classify(err), "missing Service is NotFound")
}

func TestResolvePodMissingEndpoints(t *testing.T) {
	// A selectorless Service exists but has no Endpoints object: same retry
	// class as no ready pods (pod may not be scheduled yet).
	cs := fake.NewSimpleClientset(
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "selectorless"}},
	)
	_, err := resolvePod(context.Background(), cs, "prod", "selectorless")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no ready pods")
}

func TestPickFreePort(t *testing.T) {
	port, err := pickLocalPort(0)
	require.NoError(t, err)
	require.Greater(t, port, 1023, "free port must be ephemeral, not privileged")
	require.LessOrEqual(t, port, 65535)
}

func TestPickFreePortFixedInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	taken := l.Addr().(*net.TCPAddr).Port //nolint:errcheck // *net.TCPAddr type assertion cannot fail

	_, err = pickLocalPort(taken)
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("local port %d in use", taken))
}

func TestPickFreePortFixedFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port //nolint:errcheck // *net.TCPAddr type assertion cannot fail
	require.NoError(t, l.Close())

	got, err := pickLocalPort(port)
	require.NoError(t, err)
	require.Equal(t, port, got)
}

// retryDecision is the reconnect policy: NotFound/Forbidden/Unauthorized fail
// fast (config/RBAC errors), everything else retries within the budget.
func TestRetryDecisions(t *testing.T) {
	require.False(t, retryable(errors.New("x"), ReasonNotFound), "NotFound fails fast")
	require.False(t, retryable(errors.New("x"), ReasonForbidden), "Forbidden fails fast")
	require.False(t, retryable(errors.New("x"), ReasonUnauthorized), "Unauthorized fails fast")
	require.True(t, retryable(errors.New("x"), ReasonUnreachable), "Unreachable retries")
	require.True(t, retryable(errors.New("x"), ReasonOther), "Other retries")
}

func TestBackoffSchedule(t *testing.T) {
	require.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}, backoffSchedule(3))
}
