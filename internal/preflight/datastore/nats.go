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

// NatsConfig selects the nats verify checks.
type NatsConfig struct {
	Clients   kube.Clients
	Namespace string
}

// natsClientPort is the default NATS client port.
const natsClientPort = int32(4222)

// VerifyNats is the post-install check: server StatefulSet readiness, client
// Service port, and the nats-box rtt handshake. Never short-circuits.
func VerifyNats(cfg NatsConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		natsStatefulSet(cfg.Clients, cfg.Namespace),
		natsService(cfg.Clients, cfg.Namespace),
		natsBoxRTT(cfg.Clients, cfg.Namespace),
	}
}

func natsStatefulSet(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	label := "StatefulSet nats"
	return func() preflight.Result {
		sts, err := clients.Kube.AppsV1().StatefulSets(ns).Get(
			context.Background(), "nats", metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(label, err)
		}
		if sts.Status.ReadyReplicas >= 1 {
			return preflight.Result{
				Name: label, Detail: fmt.Sprintf("%d ready replica(s)", sts.Status.ReadyReplicas),
				Status: preflight.StatusPass,
			}
		}
		return preflight.Result{
			Name: label, Detail: fmt.Sprintf("no ready NATS server replica in %s", ns),
			Status: preflight.StatusFail,
		}
	}
}

func natsService(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	label := "Service nats"
	return func() preflight.Result {
		svc, err := clients.Kube.CoreV1().Services(ns).Get(
			context.Background(), "nats", metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(label, err)
		}
		for _, p := range svc.Spec.Ports {
			if p.Name == "nats" && p.Port == natsClientPort {
				return preflight.Result{Name: label, Detail: fmt.Sprintf("exposes %d", natsClientPort), Status: preflight.StatusPass}
			}
		}
		return preflight.Result{Name: label, Detail: "missing or client port not 4222", Status: preflight.StatusFail}
	}
}

// natsBoxRTT runs `nats rtt` inside the chart's nats-box deployment (carries
// the nats CLI). Needs a real API server; fails honestly on a fake clientset.
func natsBoxRTT(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	label := "client rtt"
	return func() preflight.Result {
		if _, err := clients.Kube.AppsV1().Deployments(ns).Get(
			context.Background(), "nats-box", metav1.GetOptions{},
		); err != nil {
			return preflight.Result{
				Name:   label,
				Detail: "nats-box deployment missing in " + ns + " (handshake skipped)",
				Status: preflight.StatusFail,
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
			Namespace(ns).Resource("pods").Name("deploy/nats-box").
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Command: []string{"nats", "--server",
					fmt.Sprintf("nats://nats.%s.svc.cluster.local:%d", ns, natsClientPort), "rtt"},
				Stdout: true, Stderr: true,
			}, scheme.ParameterCodec).URL()
		exec, err := remotecommand.NewSPDYExecutor(clients.Rest, http.MethodPost, req)
		if err != nil {
			return preflight.Result{Name: label, Detail: "exec setup: " + err.Error(), Status: preflight.StatusFail}
		}
		if err := exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdout: &stdout, Stderr: &stderr,
		}); err != nil {
			return preflight.Result{
				Name:   label,
				Detail: "rtt failed — see reference/nats/troubleshooting.md: " + err.Error(),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: label, Detail: strings.TrimSpace(stdout.String()), Status: preflight.StatusPass}
	}
}
