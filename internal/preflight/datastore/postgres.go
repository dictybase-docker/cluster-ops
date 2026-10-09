package datastore

import (
	"context"
	"fmt"
	"strings"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// PostgresConfig selects the postgres verify checks.
type PostgresConfig struct {
	Clients           kube.Clients
	Namespace         string
	OperatorNamespace string
	Cluster           string
	Instances         int
	Pool              string
	WantNodes         int
}

// cnpgGVR is the CloudNativePG Cluster custom resource.
var cnpgGVR = schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}

// scheduledBackupGVR is the CloudNativePG ScheduledBackup custom resource.
var scheduledBackupGVR = schema.GroupVersionResource{
	Group: "postgresql.cnpg.io", Version: "v1", Resource: "scheduledbackups",
}

const (
	pgHealthyPhase = "Cluster in healthy state"
	pgServicePort  = int32(5432)
)

// VerifyPostgres is the full post-install check: pool, operator, storage
// classes, Cluster CR phase, instance pods, PVCs, rw/ro/r Services and
// ScheduledBackup presence. Never short-circuits.
func VerifyPostgres(cfg PostgresConfig) []IO.IO[preflight.Result] {
	clusterSelector := "cnpg.io/cluster=" + cfg.Cluster
	checks := CheckPool(PoolConfig{
		Clients: cfg.Clients, Pool: cfg.Pool, WantCount: cfg.WantNodes,
	})
	checks = append(checks,
		OperatorRunning(cfg.Clients, cfg.OperatorNamespace, "cloudnative-pg"),
		StorageClassesPresent(cfg.Clients, "dictycr-balanced", "dictycr-ssd"),
		cnpgClusterPhase(cfg.Clients, cfg.Namespace, cfg.Cluster),
		PodsReadyCount(cfg.Clients, cfg.Namespace, clusterSelector, cfg.Instances, "instance pods"),
		pvcsBoundCount(cfg.Clients, cfg.Namespace, clusterSelector, cfg.Instances, "data PVC(s)"),
		cnpgServices(cfg.Clients, cfg.Namespace, cfg.Cluster),
		scheduledBackupsPresent(cfg.Clients, cfg.Namespace),
	)
	return checks
}

// cnpgClusterPhase verifies the Cluster CR exists and reports the healthy
// phase.
func cnpgClusterPhase(clients kube.Clients, ns, cluster string) IO.IO[preflight.Result] {
	name := fmt.Sprintf("Cluster %s phase", cluster)
	return func() preflight.Result {
		if clients.Dynamic == nil {
			return preflight.Result{Name: name, Detail: "no dynamic client configured", Status: preflight.StatusFail}
		}
		obj, err := clients.Dynamic.Resource(cnpgGVR).Namespace(ns).Get(
			context.Background(), cluster, metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		phase, found, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if !found {
			return preflight.Result{Name: name, Detail: "status.phase not set yet", Status: preflight.StatusFail}
		}
		if phase != pgHealthyPhase {
			return preflight.Result{
				Name: name, Detail: fmt.Sprintf("%s (expected %q)", phase, pgHealthyPhase),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: name, Detail: phase, Status: preflight.StatusPass}
	}
}

// pvcsBoundCount requires exactly want Bound PVCs behind the selector (the
// shell gate's -eq): a leftover PVC from a deleted instance must not pass.
func pvcsBoundCount(
	clients kube.Clients, ns, selector string, want int, role string,
) IO.IO[preflight.Result] {
	name := fmt.Sprintf("Bound %s in %s", role, ns)
	return func() preflight.Result {
		pvcs, err := clients.Kube.CoreV1().PersistentVolumeClaims(ns).List(
			context.Background(), metav1.ListOptions{LabelSelector: selector},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		count := 0
		for i := range pvcs.Items {
			if pvcs.Items[i].Status.Phase == corev1.ClaimBound {
				count++
			}
		}
		detail := fmt.Sprintf("%d Bound %s", count, role)
		if count == want {
			return preflight.Result{Name: name, Detail: detail, Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name: name, Detail: fmt.Sprintf("%s, expected %d", detail, want), Status: preflight.StatusFail,
		}
	}
}

// cnpgServices verifies the rw/ro/r Services exist and expose 5432.
func cnpgServices(clients kube.Clients, ns, cluster string) IO.IO[preflight.Result] {
	suffixes := []string{"rw", "ro", "r"}
	return func() preflight.Result {
		var missing []string
		for _, suffix := range suffixes {
			svcName := cluster + "-" + suffix
			svc, err := clients.Kube.CoreV1().Services(ns).Get(
				context.Background(), svcName, metav1.GetOptions{},
			)
			if err != nil {
				missing = append(missing, svcName)
				continue
			}
			if len(svc.Spec.Ports) == 0 || svc.Spec.Ports[0].Port != pgServicePort {
				missing = append(missing, svcName)
			}
		}
		if len(missing) == 0 {
			return preflight.Result{
				Name:   fmt.Sprintf("Services %s-rw/ro/r", cluster),
				Detail: "all expose 5432", Status: preflight.StatusPass,
			}
		}
		return preflight.Result{
			Name:   fmt.Sprintf("Services %s-rw/ro/r", cluster),
			Detail: fmt.Sprintf("missing or not on 5432: %s", strings.Join(missing, ", ")),
			Status: preflight.StatusFail,
		}
	}
}

// scheduledBackupsPresent verifies at least one ScheduledBackup exists — the
// production stack always creates one.
func scheduledBackupsPresent(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	name := "ScheduledBackup"
	return func() preflight.Result {
		if clients.Dynamic == nil {
			return preflight.Result{Name: name, Detail: "no dynamic client configured", Status: preflight.StatusFail}
		}
		list, err := clients.Dynamic.Resource(scheduledBackupGVR).Namespace(ns).List(
			context.Background(), metav1.ListOptions{},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		count := len(list.Items)
		if count >= 1 {
			return preflight.Result{Name: name, Detail: fmt.Sprintf("%d present", count), Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name:   name,
			Detail: "none found — the production stack always creates one",
			Status: preflight.StatusFail,
		}
	}
}
