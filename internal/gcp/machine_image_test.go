package gcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	"github.com/stretchr/testify/require"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// tickWithin fails the test when waitTick has not returned in time.
func tickWithin(t *testing.T, cfg waitfor.WaitConfig, start time.Time) error {
	t.Helper()
	got := make(chan error, 1)
	go func() { got <- waitTick(context.Background(), cfg, start, "dcr-node-image") }()
	select {
	case err := <-got:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("waitTick ignored cfg.Timeout and kept waiting")
		return nil
	}
}

// The CLI passes a signal-only context (no deadline): --timeout must be
// enforced from cfg, or a machine image stuck in CREATING waits forever.
func TestWaitTickEnforcesTimeout(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: 50 * time.Millisecond, Interval: time.Hour}
	err := tickWithin(t, cfg, time.Now().Add(-time.Second))
	var werr *waitfor.Error
	require.True(t, errors.As(err, &werr), "want *waitfor.Error, got %v", err)
	require.Equal(t, waitfor.KindTimeout, werr.Kind)
}

// A long interval must not overshoot the deadline: the last sleep is capped
// at the time remaining, and when that sleep spends the budget the tick is
// the timeout — no extra poll sneaks past the deadline.
func TestWaitTickFinalSleepExpiresAtDeadline(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: 100 * time.Millisecond, Interval: time.Hour}
	err := tickWithin(t, cfg, time.Now())
	var werr *waitfor.Error
	require.True(t, errors.As(err, &werr), "want *waitfor.Error, got %v", err)
	require.Equal(t, waitfor.KindTimeout, werr.Kind)
}

// --timeout must also bound the API calls themselves: a machine-image
// describe that hangs must not outlive the budget (the CLI context only
// carries signals).
func TestWaitMachineImageBoundsHungCalls(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hang until the client gives up
	}))
	defer ts.Close()
	cfg := waitfor.WaitConfig{Timeout: 100 * time.Millisecond, Interval: time.Hour}
	got := make(chan error, 1)
	go func() { got <- waitHungImage(ts.URL, cfg) }()
	select {
	case err := <-got:
		var werr *waitfor.Error
		require.True(t, errors.As(err, &werr), "want *waitfor.Error, got %v", err)
		require.Equal(t, waitfor.KindTimeout, werr.Kind)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitMachineImage ignored cfg.Timeout and kept waiting on a hung API call")
	}
}

// waitHungImage polls a machine image through a fake endpoint that never
// answers — the way a stalled API request behaves.
func waitHungImage(endpoint string, cfg waitfor.WaitConfig) error {
	svc, err := compute.NewService(context.Background(),
		option.WithEndpoint(endpoint), option.WithoutAuthentication())
	if err != nil {
		return err
	}
	return pollMachineImage(context.Background(), svc, "proj", "dcr-node-image", cfg)
}

func TestWaitTickSleepsOneInterval(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: time.Hour, Interval: time.Millisecond}
	require.NoError(t, tickWithin(t, cfg, time.Now()))
}
