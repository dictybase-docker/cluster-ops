package datastore_test

import (
	"strings"
	"testing"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/datastore"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// nodeByName builds a pool node.
const testPool = "database"

func nodeByName(name string, opts ...func(*corev1.Node)) *corev1.Node {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"pool": testPool, "topology.kubernetes.io/zone": "us-central1-a"},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{Architecture: "amd64"},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

func withTaint(n *corev1.Node) {
	n.Spec.Taints = []corev1.Taint{{Key: "dedicated", Value: testPool, Effect: corev1.TaintEffectNoSchedule}}
}

func withZone(zone string) func(*corev1.Node) {
	return func(n *corev1.Node) {
		n.Labels["topology.kubernetes.io/zone"] = zone
	}
}

func notReady(n *corev1.Node) {
	n.Status.Conditions = []corev1.NodeCondition{
		{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
	}
}

func withArch(arch string) func(*corev1.Node) {
	return func(n *corev1.Node) {
		n.Status.NodeInfo.Architecture = arch
	}
}

func TestCheckPool(t *testing.T) {
	t.Run("all green", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(
			nodeByName("n1", withTaint, withZone("us-central1-a")),
			nodeByName("n2", withTaint, withZone("us-central1-b")),
			nodeByName("n3", withTaint, withZone("us-central1-c")),
		)}
		results := preflight.Run(datastore.CheckPool(datastore.PoolConfig{
			Clients: clients, Pool: testPool, WantCount: 3, RequireAmd64: true,
		}))
		require.False(t, preflight.Failed(results), "3 tainted ready amd64 nodes must pass: %v", results)
		zones := results[3] // count, arch, taint, zones, ready
		require.Equal(t, preflight.StatusInfo, zones.Status)
		require.Contains(t, zones.Detail, "us-central1-a, us-central1-b, us-central1-c")
	})

	t.Run("2 of 3 nodes ready fails with the named ratio", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(
			nodeByName("n1", withTaint),
			nodeByName("n2", withTaint),
			nodeByName("n3", withTaint, notReady),
		)}
		results := preflight.Run(datastore.CheckPool(datastore.PoolConfig{
			Clients: clients, Pool: testPool, WantCount: 3,
		}))
		ready := results[len(results)-1]
		require.Equal(t, preflight.StatusFail, ready.Status)
		require.Equal(t, "2/3 nodes Ready", ready.Detail)
	})

	t.Run("arm64 node fails the amd64 check", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(
			nodeByName("n1", withTaint, withArch("arm64")),
			nodeByName("n2", withTaint),
			nodeByName("n3", withTaint),
		)}
		results := preflight.Run(datastore.CheckPool(datastore.PoolConfig{
			Clients: clients, Pool: testPool, WantCount: 3, RequireAmd64: true,
		}))
		arch := findResult(results, " arch")
		require.Equal(t, preflight.StatusFail, arch.Status, "arch check must fail for arm64")
	})

	t.Run("untainted node fails the taint check", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(
			nodeByName("n1"), nodeByName("n2", withTaint), nodeByName("n3", withTaint),
		)}
		results := preflight.Run(datastore.CheckPool(datastore.PoolConfig{
			Clients: clients, Pool: testPool, WantCount: 3,
		}))
		taint := findResult(results, " taint")
		require.Equal(t, preflight.StatusFail, taint.Status)
		require.Equal(t, "2/3 nodes carry dedicated=database:NoSchedule", taint.Detail)
	})

	t.Run("wrong count fails", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(nodeByName("n1", withTaint))}
		results := preflight.Run(datastore.CheckPool(datastore.PoolConfig{
			Clients: clients, Pool: testPool, WantCount: 3,
		}))
		require.Equal(t, preflight.StatusFail, results[0].Status)
		require.Contains(t, results[0].Detail, "expected 3")
	})
}

// findResult locates a result by Name suffix (arch/taint/zones/ready).
func findResult(results []preflight.Result, suffix string) preflight.Result {
	for _, r := range results {
		if strings.HasSuffix(r.Name, suffix) {
			return r
		}
	}
	return preflight.Result{}
}

const appSelectorLabel = "app.kubernetes.io/name"

func TestOperatorRunning(t *testing.T) {
	opPod := func(ns, app string) *corev1.Pod { //nolint:unparam // fixture symmetry
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: ns, Name: app + "-0", Labels: map[string]string{appSelectorLabel: app},
		}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	}
	t.Run("running operator passes", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(opPod("operators", "cloudnative-pg"))}
		result := datastore.OperatorRunning(clients, "operators", "cloudnative-pg")()
		require.Equal(t, preflight.StatusPass, result.Status)
	})
	t.Run("missing operator fails", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset()}
		result := datastore.OperatorRunning(clients, "operators", "cloudnative-pg")()
		require.Equal(t, preflight.StatusFail, result.Status)
		require.Contains(t, result.Detail, "operators")
	})
}

func TestStorageClassesPresent(t *testing.T) {
	sc := func(name string) runtime.Object {
		return &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
	}
	t.Run("both classes present", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(sc("dictycr-balanced"), sc("dictycr-ssd"))}
		result := datastore.StorageClassesPresent(clients, "dictycr-balanced", "dictycr-ssd")()
		require.Equal(t, preflight.StatusPass, result.Status)
	})
	t.Run("missing class names it", func(t *testing.T) {
		clients := kube.Clients{Kube: fake.NewSimpleClientset(sc("dictycr-balanced"))}
		result := datastore.StorageClassesPresent(clients, "dictycr-balanced", "dictycr-ssd")()
		require.Equal(t, preflight.StatusFail, result.Status)
		require.Contains(t, result.Detail, "dictycr-ssd")
	})
}
