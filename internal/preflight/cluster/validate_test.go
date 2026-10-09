package cluster

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
)

func runValidate(cfg ValidateConfig) []preflight.Result {
	return preflight.Run([]IO.IO[preflight.Result]{CheckKopsValidate(cfg)})
}

// validateCfg seeds one fixed scenario; the validator is the variable under test.
func validateCfg(validate KopsValidator) ValidateConfig {
	return ValidateConfig{
		Cluster: testCluster, KopsName: "kn", State: "gs://s", WaitTime: 20,
		Validate: validate,
	}
}

func TestDeriveKopsName(t *testing.T) {
	t.Setenv("KOPS_CLUSTER_NAME", "")
	require.Equal(t, "dcr-k8s.local", DeriveKopsName(testCluster, ""))
	t.Setenv("KOPS_CLUSTER_NAME", "env-name")
	require.Equal(t, "env-name", DeriveKopsName(testCluster, ""))
	require.Equal(t, "explicit", DeriveKopsName(testCluster, "explicit"))
}

func TestDeriveState(t *testing.T) {
	t.Setenv("KOPS_STATE_STORE", "")
	require.Equal(t, "gs://kops-state-dcr", DeriveState(testCluster, ""))
	t.Setenv("KOPS_STATE_STORE", "gs://from-env/")
	require.Equal(t, "gs://from-env", DeriveState(testCluster, ""))
	require.Equal(t, "gs://explicit", DeriveState(testCluster, "gs://explicit/"))
}

func TestKopsValidatePass(t *testing.T) {
	got := runValidate(validateCfg(func(_, _ string, _ int) (string, error) {
		return "Your cluster kn is ready\n", nil
	}))
	require.Len(t, got, 1)
	require.Equal(t, preflight.StatusPass, got[0].Status)
	require.Equal(t, "cluster kn is ready", got[0].Detail)
}

func TestKopsValidateTimeout(t *testing.T) {
	got := runValidate(validateCfg(func(_, _ string, _ int) (string, error) {
		return "", context.DeadlineExceeded
	}))
	require.Equal(t, preflight.StatusFail, got[0].Status)
	require.Equal(t, "timed out after 20m waiting for kn", got[0].Detail)
}

func TestKopsValidateBinaryMissing(t *testing.T) {
	got := runValidate(validateCfg(func(_, _ string, _ int) (string, error) {
		return "", fmt.Errorf("run kops: %w", exec.ErrNotFound)
	}))
	require.Equal(t, preflight.StatusFail, got[0].Status)
	require.Equal(t, "kops not found on PATH — run just prepare-tools", got[0].Detail)
}

func TestKopsValidateExitError(t *testing.T) {
	cmd := exec.Command("false")
	exitErr := cmd.Run() // real *exec.ExitError
	require.Error(t, exitErr)
	got := runValidate(validateCfg(func(_, _ string, _ int) (string, error) {
		return "", exitErr
	}))
	require.Equal(t, preflight.StatusFail, got[0].Status)
	require.Contains(t, got[0].Detail, "kops validate cluster failed: ")
	require.Contains(t, got[0].Fix, "kops validate cluster --name=kn --state=gs://s")
}

func TestKopsValidateOtherError(t *testing.T) {
	got := runValidate(validateCfg(func(_, _ string, _ int) (string, error) {
		return "", errors.New("state store unreachable")
	}))
	require.Equal(t, preflight.StatusFail, got[0].Status)
	require.Equal(t, "kops validate cluster error: state store unreachable", got[0].Detail)
}

// Nothing derivable without a cluster name: callers must omit --name/--state
// (kops' own defaults) instead of passing "<empty>-k8s.local".
func TestDeriveEmptyCluster(t *testing.T) {
	t.Setenv("KOPS_CLUSTER_NAME", "")
	t.Setenv("KOPS_STATE_STORE", "")
	require.Empty(t, DeriveKopsName("", ""))
	require.Empty(t, DeriveState("", ""))
}
