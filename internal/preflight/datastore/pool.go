// Package datastore owns the shared pool check and per-datastore verify
// checks (arangodb, postgres, redis, minio, nats). Every check returns
// preflight Results and never short-circuits.
package datastore

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PoolConfig selects the stateful-db pool prerequisites.
type PoolConfig struct {
	Clients      kube.Clients
	Pool         string // node label pool=<pool>
	WantCount    int
	RequireAmd64 bool // ArangoDB requires amd64; postgres/redis/minio do not check arch
}

// CheckPool emits every pool check (node count, optional amd64 arch, the
// dedicated taint, zone spread info and Ready status) as one ordered slice.
// Each check re-lists the nodes, matching the shell probes it replaces.
func CheckPool(cfg PoolConfig) []IO.IO[preflight.Result] {
	selector := "pool=" + cfg.Pool
	name := "pool " + cfg.Pool
	checks := []IO.IO[preflight.Result]{
		withNodes(cfg.Clients, selector, name, func(n string, nodes []corev1.Node) preflight.Result {
			return poolCount(n, cfg, nodes)
		}),
	}
	if cfg.RequireAmd64 {
		checks = append(checks, withNodes(cfg.Clients, selector, name+" arch",
			func(n string, nodes []corev1.Node) preflight.Result { return poolArch(n, nodes) }))
	}
	checks = append(checks,
		withNodes(cfg.Clients, selector, name+" taint",
			func(n string, nodes []corev1.Node) preflight.Result { return poolTaint(n, cfg.Pool, nodes) }),
		withNodes(cfg.Clients, selector, name+" zones",
			func(n string, nodes []corev1.Node) preflight.Result { return poolZones(n, nodes) }),
		withNodes(cfg.Clients, selector, name+" ready",
			func(n string, nodes []corev1.Node) preflight.Result { return poolReady(n, nodes) }),
	)
	return checks
}

// withNodes fetches the pool nodes and delegates; a failed list is a
// classified failure under the check name.
func withNodes(
	clients kube.Clients,
	selector, name string,
	build func(string, []corev1.Node) preflight.Result,
) IO.IO[preflight.Result] {
	return func() preflight.Result {
		nodes, err := listPoolNodes(context.Background(), clients, selector)
		if err != nil {
			return lookupFailure(name, err)
		}
		return build(name, nodes)
	}
}

func poolCount(name string, cfg PoolConfig, nodes []corev1.Node) preflight.Result {
	count := len(nodes)
	detail := fmt.Sprintf("%d node(s) with pool=%s", count, cfg.Pool)
	if count == cfg.WantCount {
		return preflight.Result{Name: name, Detail: detail, Status: preflight.StatusPass}
	}
	return preflight.Result{
		Name: name, Detail: fmt.Sprintf("%s, expected %d", detail, cfg.WantCount),
		Status: preflight.StatusFail,
	}
}

func poolArch(name string, nodes []corev1.Node) preflight.Result {
	amd64Count := 0
	for i := range nodes {
		if nodes[i].Status.NodeInfo.Architecture == "amd64" {
			amd64Count++
		}
	}
	detail := fmt.Sprintf("%d/%d node(s) are amd64", amd64Count, len(nodes))
	if len(nodes) > 0 && amd64Count == len(nodes) {
		return preflight.Result{
			Name: name, Detail: fmt.Sprintf("all %d node(s) are amd64", amd64Count),
			Status: preflight.StatusPass,
		}
	}
	return preflight.Result{
		Name: name, Detail: detail + " — ArangoDB Cluster requires amd64",
		Status: preflight.StatusFail,
	}
}

func poolTaint(name, pool string, nodes []corev1.Node) preflight.Result {
	tainted := 0
	for i := range nodes {
		if hasPoolTaint(&nodes[i], pool) {
			tainted++
		}
	}
	detail := fmt.Sprintf("%d/%d nodes carry dedicated=%s:NoSchedule", tainted, len(nodes), pool)
	if len(nodes) > 0 && tainted == len(nodes) {
		return preflight.Result{
			Name: name, Detail: fmt.Sprintf("all %d node(s) carry dedicated=%s:NoSchedule", tainted, pool),
			Status: preflight.StatusPass,
		}
	}
	return preflight.Result{Name: name, Detail: detail, Status: preflight.StatusFail}
}

func poolZones(name string, nodes []corev1.Node) preflight.Result {
	zones := poolZoneList(nodes)
	return preflight.Result{
		Name:   name,
		Detail: fmt.Sprintf("%s (%d unique)", strings.Join(zones, ", "), len(zones)),
		Status: preflight.StatusInfo,
	}
}

func poolReady(name string, nodes []corev1.Node) preflight.Result {
	ready := 0
	for i := range nodes {
		if nodeReady(&nodes[i]) {
			ready++
		}
	}
	detail := fmt.Sprintf("%d/%d nodes Ready", ready, len(nodes))
	if len(nodes) > 0 && ready == len(nodes) {
		return preflight.Result{Name: name, Detail: "all nodes are Ready", Status: preflight.StatusPass}
	}
	return preflight.Result{Name: name, Detail: detail, Status: preflight.StatusFail}
}

func listPoolNodes(ctx context.Context, clients kube.Clients, selector string) ([]corev1.Node, error) {
	list, err := clients.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func hasPoolTaint(node *corev1.Node, pool string) bool {
	for _, t := range node.Spec.Taints {
		if t.Key == "dedicated" && t.Value == pool && t.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func nodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func poolZoneList(nodes []corev1.Node) []string {
	zones := make([]string, 0, len(nodes))
	for i := range nodes {
		zone, ok := nodes[i].Labels["topology.kubernetes.io/zone"]
		if !ok {
			zone = "unknown"
		}
		if !slices.Contains(zones, zone) {
			zones = append(zones, zone)
		}
	}
	sort.Strings(zones)
	return zones
}

// OperatorRunning verifies at least one Running pod behind the operator
// selector exists in the namespace.
func OperatorRunning(clients kube.Clients, ns, app string) IO.IO[preflight.Result] {
	name := fmt.Sprintf("operator %s in %s", app, ns)
	return func() preflight.Result {
		pods, err := clients.Kube.CoreV1().Pods(ns).List(
			context.Background(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		for i := range pods.Items {
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				return preflight.Result{Name: name, Detail: "Running", Status: preflight.StatusPass}
			}
		}
		return preflight.Result{
			Name: name, Detail: fmt.Sprintf("no Running operator pod in %s", ns), Status: preflight.StatusFail,
		}
	}
}

// StorageClassesPresent verifies each named StorageClass exists.
func StorageClassesPresent(clients kube.Clients, classNames ...string) IO.IO[preflight.Result] {
	name := "storage classes " + strings.Join(classNames, ",")
	return func() preflight.Result {
		var missing []string
		for _, className := range classNames {
			_, err := clients.Kube.StorageV1().StorageClasses().Get(
				context.Background(), className, metav1.GetOptions{},
			)
			if err != nil {
				missing = append(missing, className)
			}
		}
		if len(missing) == 0 {
			return preflight.Result{Name: name, Detail: "all present", Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name: name, Detail: "missing: " + strings.Join(missing, ", "), Status: preflight.StatusFail,
		}
	}
}

// PodsReadyCount emits one result: exactly want pods Running+ready behind the
// selector (pass), otherwise fail with "got/want".
func PodsReadyCount(clients kube.Clients, ns, selector string, want int, label string) IO.IO[preflight.Result] {
	return func() preflight.Result {
		got, err := countReadyPods(context.Background(), clients, ns, selector)
		if err != nil {
			return lookupFailure(label, err)
		}
		detail := fmt.Sprintf("%d/%d pod(s) Running and ready", got, want)
		status := preflight.StatusFail
		if got >= want {
			status = preflight.StatusPass
		}
		return preflight.Result{Name: label, Detail: detail, Status: status}
	}
}

// countReadyPods counts pods Running with every container ready.
func countReadyPods(ctx context.Context, clients kube.Clients, ns, selector string) (int, error) {
	pods, err := clients.Kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return 0, err
	}
	count := 0
	for i := range pods.Items {
		if podReady(&pods.Items[i]) {
			count++
		}
	}
	return count, nil
}

func podReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	statuses := pod.Status.ContainerStatuses
	if len(statuses) == 0 {
		return false
	}
	for _, cs := range statuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

// lookupFailure turns a failed list/get into a classified failure result.
func lookupFailure(name string, err error) preflight.Result {
	switch kube.Classify(err) {
	case kube.ReasonForbidden, kube.ReasonUnauthorized:
		return preflight.Result{Name: name, Detail: "access denied: " + err.Error(), Status: preflight.StatusFail}
	case kube.ReasonUnreachable:
		return preflight.Result{Name: name, Detail: "API server unreachable: " + err.Error(), Status: preflight.StatusFail}
	default:
		return preflight.Result{Name: name, Detail: err.Error(), Status: preflight.StatusFail}
	}
}
