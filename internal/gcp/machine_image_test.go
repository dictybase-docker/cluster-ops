package gcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	"github.com/stretchr/testify/require"
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
// at the time remaining.
func TestWaitTickCapsSleepAtDeadline(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: 100 * time.Millisecond, Interval: time.Hour}
	require.NoError(t, tickWithin(t, cfg, time.Now()))
}

func TestWaitTickSleepsOneInterval(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: time.Hour, Interval: time.Millisecond}
	require.NoError(t, tickWithin(t, cfg, time.Now()))
}
