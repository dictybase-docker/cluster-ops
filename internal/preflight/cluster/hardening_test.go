package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
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

func healthyFixtures() []runtime.Object {
	return []runtime.Object{
		podRunning("autoscaler-abc", map[string]string{"app": "cluster-autoscaler"}),
		npdDaemonSet(2, 2),
		podRunning("cert-manager-abc",
			map[string]string{"app.kubernetes.io/instance": "cert-manager"}),
		podRunning("node-local-dns-abc",
			map[string]string{"k8s-app": "node-local-dns"}),
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
		podRunning("cert-manager-abc",
			map[string]string{"app.kubernetes.io/instance": "cert-manager"}),
		podRunning("node-local-dns-abc",
			map[string]string{"k8s-app": "node-local-dns"}),
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
