package datastore

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// RedisConfig selects the redis verify checks.
type RedisConfig struct {
	Clients   kube.Clients
	Namespace string
	Name      string
	Pool      string
	WantNodes int
}

// redisPort is the default Redis client port.
const redisPort = int32(6379)

// VerifyRedis is the post-install check: pool, pod, data PVC, Service and a
// PING handshake inside the pod. Never short-circuits.
func VerifyRedis(cfg RedisConfig) []IO.IO[preflight.Result] {
	selector := "app=" + cfg.Name
	return append(CheckPool(PoolConfig{
		Clients: cfg.Clients, Pool: cfg.Pool, WantCount: cfg.WantNodes,
	}),
		PodsReadyCount(cfg.Clients, cfg.Namespace, selector, 1, "Redis pod"),
		redisPVCBound(cfg.Clients, cfg.Namespace, cfg.Name),
		redisService(cfg.Clients, cfg.Namespace, cfg.Name),
		redisPing(cfg.Clients, cfg.Namespace, cfg.Name),
	)
}

func redisPVCBound(clients kube.Clients, ns, name string) IO.IO[preflight.Result] {
	pvcName := name + "-data"
	label := "PVC " + pvcName
	return func() preflight.Result {
		pvc, err := clients.Kube.CoreV1().PersistentVolumeClaims(ns).Get(
			context.Background(), pvcName, metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(label, err)
		}
		if pvc.Status.Phase == corev1.ClaimBound {
			return preflight.Result{Name: label, Detail: "Bound", Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name: label, Detail: fmt.Sprintf("not Bound (status: %s)", pvc.Status.Phase),
			Status: preflight.StatusFail,
		}
	}
}

func redisService(clients kube.Clients, ns, name string) IO.IO[preflight.Result] {
	label := "Service " + name
	return func() preflight.Result {
		svc, err := clients.Kube.CoreV1().Services(ns).Get(
			context.Background(), name, metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(label, err)
		}
		if len(svc.Spec.Ports) > 0 && svc.Spec.Ports[0].Port == redisPort {
			return preflight.Result{
				Name: label, Detail: fmt.Sprintf("exposes %d", redisPort),
				Status: preflight.StatusPass,
			}
		}
		return preflight.Result{
			Name: label, Detail: fmt.Sprintf("missing or not on %d", redisPort),
			Status: preflight.StatusFail,
		}
	}
}

// redisPing runs redis-cli PING inside the first ready pod via exec. Needs a
// real API server: on a fake clientset the exec fails, so the check reports
// a failure instead of a false pass.
func redisPing(clients kube.Clients, ns, name string) IO.IO[preflight.Result] {
	const label = "PING"
	return func() preflight.Result {
		pods, err := clients.Kube.CoreV1().Pods(ns).List(
			context.Background(), metav1.ListOptions{LabelSelector: "app=" + name},
		)
		if err != nil || len(pods.Items) == 0 {
			return preflight.Result{
				Name: label, Detail: "no ready Redis pod to exec into", Status: preflight.StatusFail,
			}
		}
		// Exec needs a live API server; the fake clientset cannot run it.
		if clients.Rest == nil {
			return preflight.Result{
				Name:   label,
				Detail: "exec handshake requires a live API server (not verifiable here)",
				Status: preflight.StatusFail,
			}
		}
		var stdout, stderr bytes.Buffer
		req := clients.Kube.CoreV1().RESTClient().Post().
			Namespace(ns).Resource("pods").Name(pods.Items[0].Name).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Command: []string{"sh", "-c", "redis-cli PING"},
				Stdout:  true, Stderr: true,
			}, scheme.ParameterCodec).URL()
		exec, err := remotecommand.NewSPDYExecutor(clients.Rest, http.MethodPost, req)
		if err != nil {
			return preflight.Result{Name: label, Detail: "exec setup: " + err.Error(), Status: preflight.StatusFail}
		}
		if err := exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdout: &stdout, Stderr: &stderr,
		}); err != nil {
			return preflight.Result{
				Name: label, Detail: "PING failed: " + err.Error(), Status: preflight.StatusFail,
			}
		}
		if strings.TrimSpace(stdout.String()) == "PONG" {
			return preflight.Result{Name: label, Detail: "PING returned PONG", Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name: label, Detail: fmt.Sprintf("PING failed (got: %s)", stdout.String()),
			Status: preflight.StatusFail,
		}
	}
}
