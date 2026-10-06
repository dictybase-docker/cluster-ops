//go:build integration

package backend_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Run with: just test-preflight-integration (k3d). Exercises the parts the
// fakes cannot: the real API server and terratest's port-forward.

func integrationClients(t *testing.T) kube.Clients {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set — run via just test-preflight-integration")
	}
	return F.Pipe1(
		kube.NewClients(kubeconfig, ""),
		E.Fold(
			func(err error) kube.Clients { t.Fatalf("kube clients: %v", err); return kube.Clients{} },
			F.Identity[kube.Clients],
		),
	)
}

func TestIntegrationNamespaceAndSecret(t *testing.T) {
	clients := integrationClients(t)
	ctx := context.Background()
	ns := fmt.Sprintf("preflight-it-%d", time.Now().UnixNano())
	_, err := clients.Kube.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, clients.Kube.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}))
	})

	cfg := backend.PrereqsConfig{
		Clients: clients, Namespace: ns, SecretName: testSecretName, UserKey: testUserKey, PassKey: testPassKey,
	}
	require.Equal(t, preflight.StatusPass, backend.CheckNamespace(cfg)().Status)

	missing := backend.CheckSecret(cfg)()
	require.Equal(t, preflight.StatusFail, missing.Status)
	require.Equal(t, "not found", missing.Detail)

	_, err = clients.Kube.CoreV1().Secrets(ns).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecretName},
		Data:       map[string][]byte{testUserKey: []byte("root"), testPassKey: []byte("pw")},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Equal(t, preflight.StatusPass, backend.CheckSecret(cfg)().Status)

	absent := cfg
	absent.Namespace = ns + "-absent"
	require.Equal(t, "not found", backend.CheckNamespace(absent)().Detail)
}

// The tunnel resolves a real Service to a ready pod on a free local port.
// k3s ships CoreDNS behind kube-system/kube-dns (TCP 53).
func TestIntegrationTunnelToService(t *testing.T) {
	clients := integrationClients(t)
	tunnel := F.Pipe1(
		kube.OpenServiceTunnel(clients, "kube-system", "kube-dns", 53)(),
		E.Fold(
			func(err error) *kube.Tunnel { t.Fatalf("open tunnel: %v", err); return nil },
			F.Identity[*kube.Tunnel],
		),
	)
	t.Cleanup(func() { _ = tunnel.Close()() })

	require.NotEqual(t, 18529, tunnel.LocalPort)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", tunnel.LocalPort), 5*time.Second)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

// A missing Service drives terratest through its internal t.Fatal path; the
// adapter must turn that into an error instead of crashing.
func TestIntegrationTunnelMissingServiceIsAnError(t *testing.T) {
	clients := integrationClients(t)
	got := kube.OpenServiceTunnel(clients, "kube-system", "no-such-service", 8529)()
	require.True(t, E.IsLeft(got), "missing Service must be an error, not a panic or nil tunnel")
}
