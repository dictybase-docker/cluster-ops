package backend

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func writeKubeconfig(t *testing.T, cluster, server, tlsName string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[cluster] = &clientcmdapi.Cluster{
		Server:                   server,
		TLSServerName:            tlsName,
		CertificateAuthorityData: []byte("ca-bytes"),
	}
	require.NoError(t, clientcmd.WriteToFile(*cfg, path))
	return path
}

func TestAlignAPIServerUsesLoadBalancerFromKubeconfig(t *testing.T) {
	const cluster = "dcr-kube1-k8s.local"
	exported := writeKubeconfig(t, cluster, "https://api.does-not-exist.invalid", "")
	source := writeKubeconfig(t, cluster, "https://35.226.214.200", "api.internal.dcr-kube1-k8s.local")

	require.NoError(t, alignAPIServer(exported, cluster, source))

	got, err := clientcmd.LoadFromFile(exported)
	require.NoError(t, err)
	require.Equal(t, "https://35.226.214.200", got.Clusters[cluster].Server)
	require.Equal(t, "api.internal.dcr-kube1-k8s.local", got.Clusters[cluster].TLSServerName)
	require.Equal(t, []byte("ca-bytes"), got.Clusters[cluster].CertificateAuthorityData)
}

func TestAlignAPIServerLeavesResolvableServer(t *testing.T) {
	const cluster = "dcr-kube1-k8s.local"
	exported := writeKubeconfig(t, cluster, "https://127.0.0.1", "")
	source := writeKubeconfig(t, cluster, "https://35.226.214.200", "api.internal.dcr-kube1-k8s.local")

	require.NoError(t, alignAPIServer(exported, cluster, source))

	got, err := clientcmd.LoadFromFile(exported)
	require.NoError(t, err)
	require.Equal(t, "https://127.0.0.1", got.Clusters[cluster].Server)
	require.Empty(t, got.Clusters[cluster].TLSServerName)
}

func TestAlignAPIServerIgnoresOtherCluster(t *testing.T) {
	const cluster = "dcr-kube1-k8s.local"
	exported := writeKubeconfig(t, cluster, "https://api.does-not-exist.invalid", "")
	source := writeKubeconfig(t, "other-k8s.local", "https://35.226.214.200", "api.internal.other")

	require.NoError(t, alignAPIServer(exported, cluster, source))

	got, err := clientcmd.LoadFromFile(exported)
	require.NoError(t, err)
	require.Equal(t, "https://api.does-not-exist.invalid", got.Clusters[cluster].Server)
}
