package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/stretchr/testify/require"
)

const testCluster = "dcr-kube1"

func TestLoadByName(t *testing.T) {
	dir := t.TempDir()
	content := `cluster: dcr-kube1
stack: prod
gcp_project: dcr-kube1
namespace: prod
kms_secrets_provider: gcpkms://projects/dcr-kube1/locations/us-central1/keyRings/dcr-kube1/cryptoKeys/dcr-kube1
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dcr-kube1.yaml"), []byte(content), 0o600))

	result := registry.LoadByName(dir, testCluster)
	require.True(t, E.IsRight(result))
	entry := F.Pipe1(result, E.GetOrElse(func(_ error) registry.Entry { return registry.Entry{} }))
	require.Equal(t, testCluster, entry.Cluster)
	require.Equal(t, "prod", entry.Stack)
}

func TestLoadByStack(t *testing.T) {
	dir := t.TempDir()
	content := `cluster: dcr-kube1
stack: prod
gcp_project: dcr-kube1
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dcr-kube1.yaml"), []byte(content), 0o600))

	result := registry.LoadByStack(dir, "prod")
	require.True(t, E.IsRight(result))
	entry := F.Pipe1(result, E.GetOrElse(func(_ error) registry.Entry { return registry.Entry{} }))
	require.Equal(t, testCluster, entry.Cluster)
}

func TestLoadByStackNotFound(t *testing.T) {
	dir := t.TempDir()
	result := registry.LoadByStack(dir, "missing")
	require.True(t, E.IsLeft(result))
}

func TestParseKMSKey(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantErr  bool
		expected registry.KMSKey
	}{
		{
			name: "valid URI",
			uri:  "gcpkms://projects/dcr-kube1/locations/us-central1/keyRings/dcr-kube1/cryptoKeys/dcr-kube1",
			expected: registry.KMSKey{
				Project:  testCluster,
				Location: "us-central1",
				KeyRing:  testCluster,
				Key:      testCluster,
			},
		},
		{
			name: "different location",
			uri:  "gcpkms://projects/foo/locations/us-east1/keyRings/bar/cryptoKeys/baz",
			expected: registry.KMSKey{
				Project:  "foo",
				Location: "us-east1",
				KeyRing:  "bar",
				Key:      "baz",
			},
		},
		{
			name:    "malformed URI",
			uri:     "gcpkms://invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := registry.ParseKMSKey(tt.uri)
			if tt.wantErr {
				require.True(t, E.IsLeft(result))
			} else {
				require.True(t, E.IsRight(result))
				key := F.Pipe1(result, E.GetOrElse(func(_ error) registry.KMSKey { return registry.KMSKey{} }))
				require.Equal(t, tt.expected, key)
			}
		})
	}
}
