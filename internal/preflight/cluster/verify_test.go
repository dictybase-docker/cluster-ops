package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/custodian"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func okHA(context.Context, kubernetes.Interface) []custodian.HACheck {
	return []custodian.HACheck{
		{Name: "cluster-autoscaler", Status: haStatusPass, Message: "1 pod running"},
		{Name: "node-problem-detector", Status: haStatusPass, Message: "2 pods ready"},
		{Name: "metrics-server", Status: haStatusPass, Message: "2 pods running"},
		{Name: "cert-manager", Status: haStatusPass, Message: "3 pods running"},
		{Name: "node-local-dns", Status: haStatusPass, Message: "4 pods running"},
		{Name: "control-plane-zones", Status: haStatusPass, Message: "3 nodes across 3 zones"},
		{Name: "batch-taints", Status: haStatusPass, Message: "3 batch nodes have spot taint"},
	}
}

func stubNewClients(objs []runtime.Object) func(string) (kube.Clients, error) {
	return func(string) (kube.Clients, error) {
		return kube.Clients{Kube: k8sfake.NewSimpleClientset(objs...)}, nil
	}
}

func verifyCfg(newClients func(string) (kube.Clients, error), ha HAChecker) VerifyConfig {
	return VerifyConfig{
		Kubeconfig: "/nonexistent/kubeconfig",
		Validate: ValidateConfig{
			Cluster: "dcr", KopsName: "kn", State: "gs://s", WaitTime: 20,
			Validate: func(_, _ string, _ int) (string, error) {
				return "Your cluster kn is ready\n", nil
			},
		},
		MetricsFor: func(kube.Clients) MetricsProbe {
			return func(context.Context) error { return nil }
		},
		HA:         ha,
		NewClients: newClients,
	}
}

func TestVerifyClusterAllPass(t *testing.T) {
	got := preflight.Run(VerifyCluster(verifyCfg(stubNewClients(healthyFixtures()), okHA)))
	require.Len(t, got, 13, "1 kops + 7 HA + 5 hardening")
	for _, r := range got {
		require.Equal(t, preflight.StatusPass, r.Status, "%s: %s", r.Name, r.Detail)
	}
}

// Never short-circuit across groups: kops failure + an HA failure + a missing
// hardening component are all listed in one run.
func TestVerifyClusterReportsEveryGroup(t *testing.T) {
	exitErr := errors.New("exit status 3")
	haFail := okHA(context.Background(), nil)
	haFail[5] = custodian.HACheck{Name: "control-plane-zones", Status: "fail", Message: "spread across 1 zone"}
	cfg := verifyCfg(stubNewClients([]runtime.Object{
		npdDaemonSet(2, 2),
		podRunning("cert-manager-abc", certManagerLabels()),
		podRunning("node-local-dns-abc", nodeLocalDNSLabels()),
	}), func(context.Context, kubernetes.Interface) []custodian.HACheck { return haFail })
	cfg.Validate.Validate = func(_, _ string, _ int) (string, error) { return "", exitErr }
	got := preflight.Run(VerifyCluster(cfg))
	var fails []string
	for _, r := range got {
		if r.Status == preflight.StatusFail {
			fails = append(fails, r.Name)
		}
	}
	require.Contains(t, fails, "kops validate cluster")
	require.Contains(t, fails, "control-plane-zones")
	require.Contains(t, fails, "cluster-autoscaler")
}

// A missing kubeconfig fails the kube-dependent groups with a named cause and
// a fix, while the kops group still runs — nothing silently skips.
func TestVerifyClusterKubeconfigMissing(t *testing.T) {
	cfg := verifyCfg(func(string) (kube.Clients, error) {
		return kube.Clients{}, errors.New("no such file")
	}, okHA)
	got := preflight.Run(VerifyCluster(cfg))
	require.Len(t, got, 13)
	var fails, warns int
	for _, r := range got {
		switch r.Status {
		case preflight.StatusFail:
			fails++
			require.Equal(t,
				"not run — kubeconfig missing (run just gcp-cluster export-kubeconfig)",
				r.Detail)
		case preflight.StatusWarn:
			warns++
		}
	}
	require.Equal(t, 2, fails, "one FAIL per kube-dependent group")
	require.Equal(t, 10, warns, "remaining group checks report not-run")
	require.Equal(t, preflight.StatusPass, got[0].Status, "kops group runs without kubeconfig")
}
