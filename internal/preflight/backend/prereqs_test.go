package backend_test

import (
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Fixture names shared by the backend_test files.
const (
	testNamespace  = "prod"
	testSecretName = "order"
	testUserKey    = "user"
	testPassKey    = "password"
)

func TestCheckNamespace(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
	}
	fakeClient := fake.NewSimpleClientset(ns)
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:   clients,
		Namespace: testNamespace,
	}

	result := backend.CheckNamespace(cfg)()
	require.Equal(t, preflight.StatusPass, result.Status)
	require.Contains(t, result.Name, "namespace prod")
}

func TestCheckNamespaceMissing(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:   clients,
		Namespace: "missing",
	}

	result := backend.CheckNamespace(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "not found")
}

func TestCheckSecret(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			testUserKey: []byte("root"),
			testPassKey: []byte("secret"),
		},
	}
	fakeClient := fake.NewSimpleClientset(secret)
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:    clients,
		Namespace:  testNamespace,
		SecretName: testSecretName,
		UserKey:    testUserKey,
		PassKey:    testPassKey,
	}

	result := backend.CheckSecret(cfg)()
	require.Equal(t, preflight.StatusPass, result.Status)
}

func TestCheckSecretMissingKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			testUserKey: []byte("root"),
			// password key missing
		},
	}
	fakeClient := fake.NewSimpleClientset(secret)
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:    clients,
		Namespace:  testNamespace,
		SecretName: testSecretName,
		UserKey:    testUserKey,
		PassKey:    testPassKey,
	}

	result := backend.CheckSecret(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "missing keys")
}

func TestCheckPort(t *testing.T) {
	tests := []struct {
		port   int
		expect preflight.Status
	}{
		{9250, preflight.StatusPass},
		{1024, preflight.StatusPass},
		{65535, preflight.StatusPass},
		{1023, preflight.StatusFail},
		{65536, preflight.StatusFail},
	}

	for _, tt := range tests {
		cfg := backend.PrereqsConfig{Port: tt.port}
		result := backend.CheckPort(cfg)()
		require.Equal(t, tt.expect, result.Status, "port %d", tt.port)
	}
}
