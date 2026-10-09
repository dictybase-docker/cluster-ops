// Package cluster migrates the cluster-level check/validate recipes to Go.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/custodian"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// nameMetricsServer is the shared component name (custodian parity).
const nameMetricsServer = "metrics-server"

// custodian.HACheck status values (mirrors internal/custodian's unexported consts).
const (
	haStatusPass = "pass"
	haStatusWarn = "warn"
	haStatusInfo = "info"
)

// errMetricsNotReporting marks a reachable metrics API that is not serving
// (404 / non-2xx) — distinct from transport errors. Matched with errors.Is,
// never by message.
var errMetricsNotReporting = errors.New("metrics not reporting")

// MetricsProbe reports whether the metrics API is serving (kubectl top parity).
type MetricsProbe func(ctx context.Context) error

// HardeningConfig selects the validate-hardening checks.
type HardeningConfig struct {
	Clients kube.Clients
	Metrics MetricsProbe
}

// CheckHardening returns the 5 post-provisioning hardening checks: 4 component
// checks mapped from custodian plus the metrics-API probe.
func CheckHardening(cfg HardeningConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		mapHACheck(cfg, custodian.CheckClusterAutoscaler),
		mapHACheck(cfg, custodian.CheckNodeProblemDetector),
		checkMetrics(cfg),
		mapHACheck(cfg, custodian.CheckCertManager),
		mapHACheck(cfg, custodian.CheckNodeLocalDNS),
	}
}

// mapHACheck adapts a custodian component check into a preflight check. The
// gate mirrors the old shell recipe: only a full pass passes — partial
// readiness (warn) and errors fail the gate, so operators see every broken
// component before continuing.
func mapHACheck(
	cfg HardeningConfig,
	check func(context.Context, kubernetes.Interface) custodian.HACheck,
) IO.IO[preflight.Result] {
	return func() preflight.Result {
		h := check(context.Background(), cfg.Clients.Kube)
		status := preflight.StatusFail
		switch h.Status {
		case haStatusPass:
			status = preflight.StatusPass
		case haStatusInfo:
			status = preflight.StatusInfo
		}
		return preflight.Result{Name: h.Name, Detail: h.Message, Status: status}
	}
}

func checkMetrics(cfg HardeningConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if err := cfg.Metrics(context.Background()); err != nil {
			if errors.Is(err, errMetricsNotReporting) {
				return preflight.Result{
					Name:   nameMetricsServer,
					Detail: "not reporting (wait 60s and retry)",
					Status: preflight.StatusFail,
				}
			}
			return preflight.Result{
				Name:   nameMetricsServer,
				Detail: fmt.Sprintf("metrics API error: %v", err),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name: nameMetricsServer, Detail: "Reporting metrics", Status: preflight.StatusPass,
		}
	}
}

// RestMetricsProbe probes GET /apis/metrics.k8s.io/v1beta1/nodes through the
// cluster REST config: transport errors propagate, a non-2xx response (e.g.
// 404 while metrics-server is not serving) is errMetricsNotReporting. A served
// response counts as reporting even when the list is empty.
func RestMetricsProbe(clients kube.Clients) MetricsProbe {
	return func(ctx context.Context) error {
		transport, err := rest.TransportFor(clients.Rest)
		if err != nil {
			return fmt.Errorf("metrics transport: %w", err)
		}
		url := strings.TrimSuffix(clients.Rest.Host, "/") + "/apis/metrics.k8s.io/v1beta1/nodes"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("metrics request: %w", err)
		}
		resp, err := (&http.Client{Transport: transport}).Do(req)
		if err != nil {
			return fmt.Errorf("metrics request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errMetricsNotReporting
		}
		return nil
	}
}
