package backend_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/stretchr/testify/require"
	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// ssarReactor answers every SelfSubjectAccessReview with the given verdict.
func ssarReactor(allowed bool) k8stesting.ReactionFunc {
	return func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectAccessReview{
			Status: authv1.SubjectAccessReviewStatus{Allowed: allowed},
		}, nil
	}
}

// tempKubeconfig is a fake exporter that writes a real temp file, so tests
// can assert the check removes it afterwards.
func tempKubeconfig(t *testing.T, path *string) backend.KubeconfigExporter {
	t.Helper()
	return func(_ backend.DeployerConfig) IOE.IOEither[error, string] {
		return IOE.TryCatchError(func() (string, error) {
			f, err := os.CreateTemp(t.TempDir(), "kubeconfig-*")
			if err != nil {
				return "", err
			}
			*path = f.Name()
			return f.Name(), f.Close()
		})
	}
}

func fakeClients(allowed bool) backend.ClientsFactory {
	return func(_ string) IOE.IOEither[error, kube.Clients] {
		cs := fake.NewSimpleClientset()
		cs.PrependReactor("create", "selfsubjectaccessreviews", ssarReactor(allowed))
		return IOE.Of[error](kube.Clients{Kube: cs})
	}
}

func deployerCfg() backend.DeployerConfig {
	return backend.DeployerConfig{Entry: registry.Entry{Namespace: testNamespace, GCPProject: "dcr-kube1"}}
}

func TestCheckDeploymentPermissionAllowed(t *testing.T) {
	var kubeconfig string
	result := backend.CheckDeploymentPermission(deployerCfg(), tempKubeconfig(t, &kubeconfig), fakeClients(true))()
	require.Equal(t, preflight.StatusPass, result.Status)
	require.NoFileExists(t, kubeconfig, "exported kubeconfig must be removed")
}

func TestCheckDeploymentPermissionDenied(t *testing.T) {
	var kubeconfig string
	result := backend.CheckDeploymentPermission(deployerCfg(), tempKubeconfig(t, &kubeconfig), fakeClients(false))()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Equal(t, "denied", result.Detail)
	require.NoFileExists(t, kubeconfig)
}

func TestCheckDeploymentPermissionExportFails(t *testing.T) {
	failing := func(_ backend.DeployerConfig) IOE.IOEither[error, string] {
		return IOE.Left[string](errors.New("kops export kubeconfig from gs://kops-state: exit status 1"))
	}
	result := backend.CheckDeploymentPermission(deployerCfg(), failing, fakeClients(true))()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "kops export kubeconfig")
}

func writeKey(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployer.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestCheckKeyProjectMatches(t *testing.T) {
	cfg := deployerCfg()
	cfg.SAKeyPath = writeKey(t, `{"project_id": "dcr-kube1", "type": "service_account"}`)
	require.Equal(t, preflight.StatusPass, backend.CheckKeyProject(cfg)().Status)
}

func TestCheckKeyProjectMismatchIsNamed(t *testing.T) {
	cfg := deployerCfg()
	cfg.SAKeyPath = writeKey(t, `{"project_id": "dcr-experiments", "type": "service_account"}`)
	result := backend.CheckKeyProject(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "does not match registry gcp_project 'dcr-kube1'")
}

func TestCheckKeyProjectNotAKey(t *testing.T) {
	cfg := deployerCfg()
	cfg.SAKeyPath = writeKey(t, `{"hello": "world"}`)
	result := backend.CheckKeyProject(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "is not a GCP service account key JSON")
}