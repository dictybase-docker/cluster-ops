package kube

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const testLocalPort = 40123

// fastBackoff keeps retry tests in milliseconds: 3 dials, 2 waits.
var fastBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

// holdAfterReady is a dialFunc that reports ready, then holds the forward
// until shutdown — a healthy session.
func holdAfterReady(pod string) dialFunc {
	return func(ctx context.Context, _ int, onReady func(string)) error {
		onReady(pod)
		<-ctx.Done()
		return ctx.Err()
	}
}

// superviseWithin fails the test when supervise has not returned in time —
// the hang the forward commands had.
func superviseWithin(
	t *testing.T, dial dialFunc, onReady func(int, string),
) (*Session, error) {
	t.Helper()
	type result struct {
		s   *Session
		err error
	}
	got := make(chan result, 1)
	go func() {
		s, err := supervise(context.Background(), dial, testLocalPort, onReady, fastBackoff)
		got <- result{s, err}
	}()
	select {
	case r := <-got:
		return r.s, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("supervise did not return after the first successful dial")
		return nil, nil
	}
}

func TestSuperviseReturnsAfterFirstDial(t *testing.T) {
	var gotPort atomic.Int64
	var gotPod atomic.Value
	sess, err := superviseWithin(t, holdAfterReady("arangodb-0"), func(port int, pod string) {
		gotPort.Store(int64(port))
		gotPod.Store(pod)
	})
	require.NoError(t, err)
	require.Equal(t, testLocalPort, sess.LocalPort)
	require.Equal(t, int64(testLocalPort), gotPort.Load())
	require.Equal(t, "arangodb-0", gotPod.Load())
	sess.Close()
}

func TestSuperviseFailsFastOnNotFound(t *testing.T) {
	var dials atomic.Int32
	notFound := func(_ context.Context, _ int, _ func(string)) error {
		dials.Add(1)
		return apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, "arangodb")
	}
	_, err := superviseWithin(t, notFound, nil)
	require.Error(t, err)
	require.Equal(t, int32(1), dials.Load(), "a missing Service is not retried")
}

func TestSuperviseGivesUpAfterRetries(t *testing.T) {
	var dials atomic.Int32
	gone := func(_ context.Context, _ int, _ func(string)) error {
		dials.Add(1)
		return errors.New("pod terminated")
	}
	_, err := superviseWithin(t, gone, nil)
	require.ErrorContains(t, err, "after 3 attempt(s)")
	require.Equal(t, int32(3), dials.Load())
}

// A forward that keeps reconnecting successfully must not exhaust the retry
// budget: the budget limits consecutive failures, not drops over a lifetime.
func TestReconnectBudgetResetsAfterHealthyDial(t *testing.T) {
	var dials, readies atomic.Int32
	flaky := func(ctx context.Context, _ int, onReady func(string)) error {
		n := dials.Add(1)
		onReady("minio-0")
		if n <= 5 {
			return errors.New("connection reset") // drop after each healthy dial
		}
		<-ctx.Done()
		return ctx.Err()
	}
	sess, err := superviseWithin(t, flaky, func(int, string) { readies.Add(1) })
	require.NoError(t, err)
	require.Eventually(t, func() bool { return dials.Load() == 6 }, 2*time.Second, time.Millisecond)
	select {
	case <-sess.Done():
		t.Fatalf("session gave up after %d healthy reconnects: %v", dials.Load()-1, sess.Err())
	default:
	}
	require.Equal(t, int32(6), readies.Load())
	sess.Close()
}
