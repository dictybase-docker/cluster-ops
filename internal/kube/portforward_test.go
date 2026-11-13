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
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func endpointSliceWith(
	ns string,
	svc string,
	sliceName string,
	endpoints ...discoveryv1.Endpoint,
) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      sliceName,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: svc,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   endpoints,
	}
}

func podEndpoint(pod string, ready *bool) discoveryv1.Endpoint {
	return discoveryv1.Endpoint{
		Addresses:  []string{"10.0.0.1"},
		Conditions: discoveryv1.EndpointConditions{Ready: ready},
		TargetRef:  &corev1.ObjectReference{Kind: "Pod", Name: pod},
	}
}

func endpointSlicesWith(ns, svc string, ready, unready []string) []runtime.Object {
	svcObj := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: svc},
	}
	var endpoints []discoveryv1.Endpoint
	for _, pod := range ready {
		endpoints = append(endpoints, podEndpoint(pod, new(true)))
	}
	for _, pod := range unready {
		endpoints = append(endpoints, podEndpoint(pod, new(false)))
	}
	slice := endpointSliceWith(ns, svc, svc+"-slice-1", endpoints...)
	return []runtime.Object{svcObj, slice}
}

func TestResolvePodFirstReady(t *testing.T) {
	cs := fake.NewSimpleClientset(endpointSlicesWith("prod", "arangodb",
		[]string{"arangodb-0", "arangodb-1"}, []string{"arangodb-2"})...)
	pod, err := resolvePod(context.Background(), cs, "prod", "arangodb")
	require.NoError(t, err)
	require.Equal(t, "arangodb-0", pod, "first ready pod wins")
}

func TestResolvePodSkipsUnready(t *testing.T) {
	cs := fake.NewSimpleClientset(endpointSlicesWith("prod", "db", nil, []string{"db-0"})...)
	_, err := resolvePod(context.Background(), cs, "prod", "db")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no ready pods")
}

func TestResolvePodMultipleSlicesNilReadyAndUnrelatedService(t *testing.T) {
	svcObj := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "arangodb"},
	}
	unrelatedSlice := endpointSliceWith(
		"prod",
		"other-service",
		"other-slice",
		podEndpoint("wrong-pod", new(true)),
	)
	firstSlice := endpointSliceWith(
		"prod",
		"arangodb",
		"arangodb-slice-a",
		podEndpoint("arangodb-unready", new(false)),
		discoveryv1.Endpoint{
			Addresses:  []string{"10.0.0.9"},
			Conditions: discoveryv1.EndpointConditions{Ready: new(true)},
			TargetRef:  &corev1.ObjectReference{Kind: "Node", Name: "node-1"},
		},
		discoveryv1.Endpoint{
			Addresses:  []string{"10.0.0.10"},
			Conditions: discoveryv1.EndpointConditions{Ready: new(true)},
		},
	)
	secondSlice := endpointSliceWith(
		"prod",
		"arangodb",
		"arangodb-slice-b",
		// Per EndpointConditions spec, Ready == nil must be treated as true.
		podEndpoint("arangodb-nil-ready", nil),
	)

	cs := fake.NewSimpleClientset(svcObj, unrelatedSlice, firstSlice, secondSlice)
	pod, err := resolvePod(context.Background(), cs, "prod", "arangodb")
	require.NoError(t, err)
	require.Equal(t, "arangodb-nil-ready", pod)
}

func TestResolvePodMissingService(t *testing.T) {
	cs := fake.NewSimpleClientset()
	_, err := resolvePod(context.Background(), cs, "prod", "nope")
	require.Error(t, err)
	require.Equal(t, ReasonNotFound, Classify(err), "missing Service is NotFound")
}

func TestResolvePodMissingEndpointSlices(t *testing.T) {
	// A selectorless Service exists but has no EndpointSlice objects: same retry
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
