package preflight_test

import (
	"bytes"
	"testing"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
)

func TestRun_NoShortCircuit(t *testing.T) {
	order := []string{}
	checks := []IO.IO[preflight.Result]{
		func() preflight.Result {
			order = append(order, "check1")
			return preflight.Result{Name: "check1", Status: preflight.StatusFail}
		},
		func() preflight.Result {
			order = append(order, "check2")
			return preflight.Result{Name: "check2", Status: preflight.StatusPass}
		},
		func() preflight.Result {
			order = append(order, "check3")
			return preflight.Result{Name: "check3", Status: preflight.StatusFail}
		},
	}

	results := preflight.Run(checks)
	require.Len(t, results, 3, "Run must execute all checks, not short-circuit")
	require.Equal(t, []string{"check1", "check2", "check3"}, order)
	require.Equal(t, preflight.StatusFail, results[0].Status)
	require.Equal(t, preflight.StatusPass, results[1].Status)
	require.Equal(t, preflight.StatusFail, results[2].Status)
}

func TestFailed(t *testing.T) {
	require.False(t, preflight.Failed([]preflight.Result{}))
	require.False(t, preflight.Failed([]preflight.Result{
		{Status: preflight.StatusPass},
		{Status: preflight.StatusInfo},
	}))
	require.True(t, preflight.Failed([]preflight.Result{
		{Status: preflight.StatusPass},
		{Status: preflight.StatusFail},
	}))
}

func TestPrint_PlainFormat(t *testing.T) {
	t.Setenv("CHECK_COLOR", "0")
	var buf bytes.Buffer
	results := []preflight.Result{
		{Name: "namespace prod", Detail: "exists", Status: preflight.StatusPass},
		{
			Name:   "secret order",
			Detail: "not found",
			Fix:    "create it with the ArangoDB credentials",
			Status: preflight.StatusFail,
		},
		{Name: "port 9250", Detail: "in range", Status: preflight.StatusPass},
	}
	preflight.Print(&buf, results)
	output := buf.String()

	// Plain format: "PASS  ", "FAIL  ", no ANSI codes
	require.Contains(t, output, "PASS  namespace prod exists\n")
	require.Contains(t, output, "FAIL  secret order not found — create it with the ArangoDB credentials\n")
	require.Contains(t, output, "PASS  port 9250 in range\n")
	require.NotContains(t, output, "\033[") // No ANSI codes
}

func TestPrint_ColorFormat(t *testing.T) {
	t.Setenv("CHECK_COLOR", "1")
	var buf bytes.Buffer
	results := []preflight.Result{
		{Name: "test", Detail: "ok", Status: preflight.StatusPass},
	}
	preflight.Print(&buf, results)
	output := buf.String()

	// Color format: ANSI codes present
	require.Contains(t, output, "\033[") // ANSI codes present
}

func TestPrint_NoFix(t *testing.T) {
	t.Setenv("CHECK_COLOR", "0")
	var buf bytes.Buffer
	results := []preflight.Result{
		{Name: "port 9250", Detail: "in range", Status: preflight.StatusPass, Fix: ""}, // No Fix
	}
	preflight.Print(&buf, results)
	output := buf.String()

	// No " — " suffix when Fix is empty
	require.Equal(t, "PASS  port 9250 in range\n", output)
	require.NotContains(t, output, " — ")
}
