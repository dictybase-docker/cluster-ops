package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func podRunning(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kube-system", Labels: labels},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func npdDaemonSet(ready, desired int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-problem-detector", Namespace: "kube-system"},
		Status: appsv1.DaemonSetStatus{
			NumberReady: ready, DesiredNumberScheduled: desired,
		},
	}
}

func certManagerLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/instance": "cert-manager"}
}

func nodeLocalDNSLabels() map[string]string {
	return map[string]string{"k8s-app": "node-local-dns"}
}

func healthyFixtures() []runtime.Object {
	return []runtime.Object{
		podRunning("autoscaler-abc", map[string]string{"app": "cluster-autoscaler"}),
		npdDaemonSet(2, 2),
		podRunning("cert-manager-abc", certManagerLabels()),
		podRunning("node-local-dns-abc", nodeLocalDNSLabels()),
	}
}

func runHardening(objs []runtime.Object, metrics MetricsProbe) []preflight.Result {
	return preflight.Run(CheckHardening(HardeningConfig{
		Clients: kube.Clients{Kube: k8sfake.NewSimpleClientset(objs...)},
		Metrics: metrics,
	}))
}

func TestHardeningAllRunning(t *testing.T) {
	got := runHardening(healthyFixtures(), func(context.Context) error { return nil })
	require.Len(t, got, 5)
	for _, r := range got {
		require.Equal(t, preflight.StatusPass, r.Status, "%s: %s", r.Name, r.Detail)
	}
}

// Never short-circuit: the missing autoscaler and the partially-ready
// node-problem-detector are both listed in one run.
func TestHardeningReportsEveryFailure(t *testing.T) {
	got := runHardening([]runtime.Object{
		npdDaemonSet(1, 2),
		podRunning("cert-manager-abc", certManagerLabels()),
		podRunning("node-local-dns-abc", nodeLocalDNSLabels()),
	}, func(context.Context) error { return nil })
	require.Len(t, got, 5)
	fails := []string{}
	for _, r := range got {
		if r.Status == preflight.StatusFail {
			fails = append(fails, r.Detail)
		}
	}
	require.Len(t, fails, 2, "autoscaler missing + NPD partial both listed")
	require.Contains(t, fails, "no pods found")
	require.Contains(t, fails, "1/2 ready")
}

// Metrics-server installed but the metrics API not serving is a distinct
// failure, not an infra error.
func TestHardeningMetricsNotReporting(t *testing.T) {
	got := runHardening(healthyFixtures(),
		func(context.Context) error { return errMetricsNotReporting })
	require.Equal(t, preflight.StatusFail, got[2].Status)
	require.Equal(t, "not reporting (wait 60s and retry)", got[2].Detail)
}

func TestHardeningMetricsAPIError(t *testing.T) {
	got := runHardening(healthyFixtures(), func(context.Context) error {
		return fmt.Errorf("probe: %w", errors.New("connection refused"))
	})
	require.Equal(t, preflight.StatusFail, got[2].Status)
	require.Equal(t, "metrics API error: probe: connection refused", got[2].Detail)
}

// The metrics probe must be bounded and must not call every failure "not
// reporting": only unavailable statuses get that class (Review Focus 2, 5).
func TestRestMetricsProbeClasses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()
	err := RestMetricsProbe(kube.Clients{Rest: &rest.Config{Host: ts.URL}})(context.Background())
	require.Error(t, err)
	require.NotErrorIs(t, err, errMetricsNotReporting)
	require.Contains(t, err.Error(), "403")

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()
	err = RestMetricsProbe(kube.Clients{Rest: &rest.Config{Host: notFound.URL}})(context.Background())
	require.ErrorIs(t, err, errMetricsNotReporting)

	hung := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hung.Close()
	restore := metricProbeTimeout
	metricProbeTimeout = 50 * time.Millisecond
	defer func() { metricProbeTimeout = restore }()
	start := time.Now()
	err = RestMetricsProbe(kube.Clients{Rest: &rest.Config{Host: hung.URL}})(context.Background())
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "probe must be bounded")
}
