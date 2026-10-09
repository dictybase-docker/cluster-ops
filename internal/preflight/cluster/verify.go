package cluster

import (
	"context"
	"sync"

	E "github.com/IBM/fp-go/v2/either"
	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/custodian"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"k8s.io/client-go/kubernetes"
)

const (
	// haCheckCount is custodian.RunHAChecks' suite size.
	haCheckCount = 7
	// hardeningCheckCount is CheckHardening's suite size.
	hardeningCheckCount = 5

	nameVerifyGroup = "verify-cluster"
	detailNotRun    = "not run — kubeconfig missing"
	detailNotRunFix = "not run — kubeconfig missing (run just gcp-cluster export-kubeconfig)"
)

// HAChecker runs the HA topology suite against an existing client.
type HAChecker func(ctx context.Context, cs kubernetes.Interface) []custodian.HACheck

// VerifyConfig selects the verify-cluster composite.
type VerifyConfig struct {
	Kubeconfig string
	Validate   ValidateConfig
	MetricsFor func(kube.Clients) MetricsProbe
	HA         HAChecker
	NewClients func(kubeconfig string) (kube.Clients, error)
}

// ClientsFromKubeconfig is the production NewClients adapter.
func ClientsFromKubeconfig(kubeconfig string) (kube.Clients, error) {
	return E.UnwrapError(kube.NewClients(kubeconfig, ""))
}

// resultSet computes one group's results at most once (shared lazy fetch).
type resultSet func() ([]preflight.Result, error)

// VerifyCluster composes the three verify-cluster groups: kops validation
// (needs no kubeconfig), the 7-check HA suite and the 5 hardening checks.
// Kube-dependent groups whose client cannot be built report one FAIL each
// ("not run — kubeconfig missing …") and WARN fills for their remaining
// slots — nothing silently skips.
func VerifyCluster(cfg VerifyConfig) []IO.IO[preflight.Result] {
	clients := sync.OnceValues(func() (kube.Clients, error) {
		return cfg.NewClients(cfg.Kubeconfig)
	})
	ha := sync.OnceValues(func() ([]preflight.Result, error) {
		cs, err := clients()
		if err != nil {
			return nil, err
		}
		return mapHAKeeper(cfg.HA(context.Background(), cs.Kube)), nil
	})
	hardening := sync.OnceValues(func() ([]preflight.Result, error) {
		cs, err := clients()
		if err != nil {
			return nil, err
		}
		return preflight.Run(CheckHardening(HardeningConfig{
			Clients: cs, Metrics: cfg.MetricsFor(cs),
		})), nil
	})
	checks := make([]IO.IO[preflight.Result], 0, 1+haCheckCount+hardeningCheckCount)
	checks = append(checks, CheckKopsValidate(cfg.Validate))
	checks = append(checks, groupChecks(ha, haCheckCount)...)
	checks = append(checks, groupChecks(hardening, hardeningCheckCount)...)
	return checks
}

// groupChecks projects a lazily-computed group into one check per slot.
func groupChecks(group resultSet, size int) []IO.IO[preflight.Result] {
	checks := make([]IO.IO[preflight.Result], 0, size)
	for i := range size {
		checks = append(checks, groupResult(group, i))
	}
	return checks
}

func groupResult(group resultSet, index int) IO.IO[preflight.Result] {
	return func() preflight.Result {
		results, err := group()
		if err != nil {
			if index == 0 {
				return preflight.Result{
					Name: nameVerifyGroup, Detail: detailNotRunFix, Status: preflight.StatusFail,
				}
			}
			return preflight.Result{
				Name: nameVerifyGroup, Detail: detailNotRun, Status: preflight.StatusWarn,
			}
		}
		return results[index]
	}
}

// mapHAKeeper maps the HA suite's checks keeping custodian's own status
// semantics (validate-kops-ha never gated on warnings).
func mapHAKeeper(checks []custodian.HACheck) []preflight.Result {
	results := make([]preflight.Result, len(checks))
	for i, h := range checks {
		status := preflight.StatusFail
		switch h.Status {
		case haStatusPass:
			status = preflight.StatusPass
		case haStatusWarn:
			status = preflight.StatusWarn
		case haStatusInfo:
			status = preflight.StatusInfo
		}
		results[i] = preflight.Result{Name: h.Name, Detail: h.Message, Status: status}
	}
	return results
}
