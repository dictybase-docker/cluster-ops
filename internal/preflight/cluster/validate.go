package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
)

// nameKopsValidate is the shared check name.
const nameKopsValidate = "kops validate cluster"

// testCluster is the shared fixture cluster name.
const testCluster = "dcr"

// KopsValidator runs `kops validate cluster` (injected for tests).
type KopsValidator func(name, state string, waitMinutes int) (string, error)

// ValidateConfig selects the validate-cluster check.
type ValidateConfig struct {
	Cluster  string // CLUSTER_NAME (diagnostics)
	KopsName string // resolved kops DNS name
	State    string // resolved state URI
	WaitTime int    // minutes
	Validate KopsValidator
}

// DeriveKopsName resolves the kops DNS name: explicit override, then
// $KOPS_CLUSTER_NAME, then <cluster>-k8s.local (the recipe's fallback chain).
func DeriveKopsName(cluster, override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv("KOPS_CLUSTER_NAME"); env != "" {
		return env
	}
	if cluster == "" {
		return ""
	}
	return cluster + "-k8s.local"
}

// DeriveState resolves the kops state URI: explicit override, then
// $KOPS_STATE_STORE, then gs://kops-state-<cluster>; trailing slashes stripped.
func DeriveState(cluster, override string) string {
	if override != "" {
		return strings.TrimSuffix(override, "/")
	}
	if env := os.Getenv("KOPS_STATE_STORE"); env != "" {
		return strings.TrimSuffix(env, "/")
	}
	if cluster == "" {
		return ""
	}
	return "gs://kops-state-" + cluster
}

// CheckKopsValidate runs kops validate cluster and classifies the outcome by
// error type (errors.Is/As — never by rendered message).
func CheckKopsValidate(cfg ValidateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if _, err := cfg.Validate(cfg.KopsName, cfg.State, cfg.WaitTime); err != nil {
			return validateFailure(cfg, err)
		}
		return preflight.Result{
			Name:   nameKopsValidate,
			Detail: "cluster " + cfg.KopsName + " is ready",
			Status: preflight.StatusPass,
		}
	}
}

func validateFailure(cfg ValidateConfig, err error) preflight.Result {
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return preflight.Result{
			Name:   nameKopsValidate,
			Detail: "kops not found on PATH — run just prepare-tools",
			Status: preflight.StatusFail,
		}
	case errors.Is(err, context.DeadlineExceeded):
		return preflight.Result{
			Name:   nameKopsValidate,
			Detail: fmt.Sprintf("timed out after %dm waiting for %s", cfg.WaitTime, cfg.KopsName),
			Status: preflight.StatusFail,
		}
	case errors.As(err, &exitErr):
		return preflight.Result{
			Name:   nameKopsValidate,
			Detail: "kops validate cluster failed: " + err.Error(),
			Fix:    fmt.Sprintf("run 'kops validate cluster --name=%s --state=%s' for details", cfg.KopsName, cfg.State),
			Status: preflight.StatusFail,
		}
	default:
		return preflight.Result{
			Name:   nameKopsValidate,
			Detail: "kops validate cluster error: " + err.Error(),
			Status: preflight.StatusFail,
		}
	}
}
