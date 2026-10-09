package backend

import (
	"errors"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testProvisioner = "pd.csi.storage.gke.io"
	testClassBal    = "dictycr-balanced"
)

func scObj(name, provisioner string, isDefault bool) *storagev1.StorageClass {
	annotations := map[string]string{}
	if isDefault {
		annotations["storageclass.kubernetes.io/is-default-class"] = "true"
	}
	return &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: name, Annotations: annotations},
		Provisioner: provisioner,
	}
}

func runStorageClass(objs []runtime.Object, classes []string) []preflight.Result {
	clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(objs...)}
	return preflight.Run(CheckStorageClass(StorageClassConfig{
		Clients: clients, Classes: classes, Provisioner: testProvisioner,
	}))
}

func TestStorageClassAllMatch(t *testing.T) {
	got := runStorageClass([]runtime.Object{
		scObj(testClassBal, testProvisioner, true),
		scObj("dictycr-ssd", testProvisioner, false),
	}, []string{testClassBal, "dictycr-ssd"})
	require.Len(t, got, 3)
	require.Equal(t, preflight.StatusPass, got[0].Status)
	require.Equal(t, "StorageClass "+testClassBal+" uses "+testProvisioner, got[0].Name+" "+got[0].Detail)
	require.Equal(t, preflight.StatusPass, got[1].Status)
	require.Equal(t, preflight.StatusInfo, got[2].Status)
	require.Equal(t, "default StorageClass", got[2].Name)
	require.Equal(t, testClassBal, got[2].Detail)
}

func TestStorageClassMissing(t *testing.T) {
	got := runStorageClass([]runtime.Object{scObj(testClassBal, testProvisioner, false)},
		[]string{"nope"})
	require.Equal(t, preflight.StatusFail, got[0].Status)
	require.Equal(t, "StorageClass", got[0].Name[:len("StorageClass")])
	require.Equal(t,
		"missing — is it declared in storage_class/Pulumi.<stack>.yaml?",
		got[0].Detail)
}

func TestStorageClassWrongProvisioner(t *testing.T) {
	got := runStorageClass([]runtime.Object{scObj(testClassBal, "other.csi", false)},
		[]string{testClassBal})
	require.Equal(t, preflight.StatusFail, got[0].Status)
	require.Equal(t,
		"uses other.csi, expected "+testProvisioner,
		got[0].Detail)
}

func TestStorageClassNoDefault(t *testing.T) {
	got := runStorageClass([]runtime.Object{scObj(testClassBal, testProvisioner, false)},
		[]string{testClassBal})
	require.Equal(t, preflight.StatusInfo, got[1].Status)
	require.Equal(t,
		"no default StorageClass set — every PVC must name one explicitly",
		got[1].Detail)
}

func TestStorageClassListError(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	cs.PrependReactor("list", "storageclasses",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("connection refused")
		})
	got := preflight.Run(CheckStorageClass(StorageClassConfig{
		Clients: kube.Clients{Kube: cs}, Classes: []string{testClassBal}, Provisioner: testProvisioner,
	}))
	var fails int
	for _, r := range got {
		if r.Status == preflight.StatusFail {
			fails++
			require.Equal(t, "cannot reach the cluster — check KUBECONFIG", r.Detail)
		}
	}
	require.Equal(t, 1, fails, "the outage is reported once, not per check")
}

func TestStorageClassNeverShortCircuits(t *testing.T) {
	got := runStorageClass([]runtime.Object{scObj("other.csi-class", "other.csi", true)},
		[]string{"nope", "other.csi-class"})
	var fails int
	for _, r := range got {
		if r.Status == preflight.StatusFail {
			fails++
		}
	}
	require.Equal(t, 2, fails, "missing + wrong provisioner both listed")
}
