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
)

// MinioConfig selects the minio verify checks.
type MinioConfig struct {
	Clients   kube.Clients
	Namespace string
	Secret    string
	Pool      string
	WantNodes int
}

// minioPort is the default MinIO S3 API port.
const minioPort = int32(9000)

// VerifyMinio is the post-install check: pool, pod, minio PVCs, Service and
// root Secret. Never short-circuits.
func VerifyMinio(cfg MinioConfig) []IO.IO[preflight.Result] {
	selector := "app.kubernetes.io/name=minio"
	return append(CheckPool(PoolConfig{
		Clients: cfg.Clients, Pool: cfg.Pool, WantCount: cfg.WantNodes,
	}),
		PodsReadyCount(cfg.Clients, cfg.Namespace, selector, 1, "MinIO pod"),
		minioPVCsBound(cfg.Clients, cfg.Namespace),
		minioService(cfg.Clients, cfg.Namespace),
		secretPresent(cfg.Clients, cfg.Namespace, cfg.Secret),
	)
}

// minioPVCsBound counts Bound PVCs named minio*.
func minioPVCsBound(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	const label = "minio PVC(s)"
	return func() preflight.Result {
		pvcs, err := clients.Kube.CoreV1().PersistentVolumeClaims(ns).List(
			context.Background(), metav1.ListOptions{},
		)
		if err != nil {
			return lookupFailure(label, err)
		}
		count := 0
		for i := range pvcs.Items {
			pvc := &pvcs.Items[i]
			if strings.HasPrefix(pvc.Name, "minio") && pvc.Status.Phase == corev1.ClaimBound {
				count++
			}
		}
		if count >= 1 {
			return preflight.Result{
				Name: label, Detail: fmt.Sprintf("%d Bound minio PVC(s)", count), Status: preflight.StatusPass,
			}
		}
		return preflight.Result{Name: label, Detail: "no Bound minio PVC in " + ns, Status: preflight.StatusFail}
	}
}

func minioService(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	label := "Service minio"
	return func() preflight.Result {
		svc, err := clients.Kube.CoreV1().Services(ns).Get(
			context.Background(), "minio", metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(label, err)
		}
		if len(svc.Spec.Ports) > 0 && svc.Spec.Ports[0].Port == minioPort {
			return preflight.Result{Name: label, Detail: fmt.Sprintf("exposes %d", minioPort), Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name: label, Detail: fmt.Sprintf("missing or not on %d", minioPort), Status: preflight.StatusFail,
		}
	}
}

// secretPresent verifies a Secret exists (never its contents).
func secretPresent(clients kube.Clients, ns, secret string) IO.IO[preflight.Result] {
	label := "Secret " + secret
	return func() preflight.Result {
		if _, err := clients.Kube.CoreV1().Secrets(ns).Get(
			context.Background(), secret, metav1.GetOptions{},
		); err != nil {
			return lookupFailure(label, err)
		}
		return preflight.Result{Name: label, Detail: detailPresent, Status: preflight.StatusPass}
	}
}
