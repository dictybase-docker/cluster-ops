package datastore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

const natsTestNS = "dev"

func natsBoxDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: natsBoxName, Namespace: natsTestNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/component": natsBoxName}},
		},
	}
}

func natsBoxPodObj(name string, ready bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: natsTestNS,
			Labels: map[string]string{"app.kubernetes.io/component": natsBoxName},
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Ready: ready}},
		},
	}
}

// The exec API needs a pod name; `deploy/nats-box` is kubectl shorthand that
// the API server rejects with 404.
func TestNatsBoxPodResolvesDeploymentToReadyPod(t *testing.T) {
	objs := []runtime.Object{
		natsBoxDeployment(),
		natsBoxPodObj("nats-box-6c9f-old", false),
		natsBoxPodObj("nats-box-6c9f-abcde", true),
	}
	pod, err := natsBoxPod(context.Background(), k8sfake.NewSimpleClientset(objs...), natsTestNS)
	require.NoError(t, err)
	require.Equal(t, "nats-box-6c9f-abcde", pod)
}

func TestNatsBoxPodNoReadyPod(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(natsBoxDeployment(), natsBoxPodObj("nats-box-x", false))
	_, err := natsBoxPod(context.Background(), cs, natsTestNS)
	require.ErrorContains(t, err, "no ready nats-box pod")
}

func TestNatsBoxPodNoSelector(t *testing.T) {
	dep := natsBoxDeployment()
	dep.Spec.Selector = nil
	_, err := natsBoxPod(context.Background(), k8sfake.NewSimpleClientset(dep), natsTestNS)
	require.ErrorContains(t, err, "has no pod selector")
}

func TestNatsBoxPodMissingDeployment(t *testing.T) {
	_, err := natsBoxPod(context.Background(), k8sfake.NewSimpleClientset(), natsTestNS)
	require.ErrorContains(t, err, "nats-box deployment missing")
}
