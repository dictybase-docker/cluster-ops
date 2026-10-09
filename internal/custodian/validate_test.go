package custodian

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// The exported checks accept kubernetes.Interface so preflight tests can use
// the fake clientset.
func TestExportedChecksUseInterface(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "autoscaler-abc", Namespace: "kube-system",
			Labels: map[string]string{"app": "cluster-autoscaler"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	got := CheckClusterAutoscaler(context.Background(), k8sfake.NewSimpleClientset(pod))
	require.Equal(t, "cluster-autoscaler", got.Name)
	require.Equal(t, statusPass, got.Status)

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-problem-detector", Namespace: "kube-system"},
		Status: appsv1.DaemonSetStatus{
			NumberReady: 1, DesiredNumberScheduled: 2,
		},
	}
	npd := CheckNodeProblemDetector(context.Background(), k8sfake.NewSimpleClientset(ds))
	require.Equal(t, statusWarn, npd.Status)
	require.Equal(t, "1/2 ready", npd.Message)
}

func TestValidateDaemonSet(t *testing.T) {
	tests := []struct {
		name       string
		ds         *appsv1.DaemonSet
		wantStatus string
	}{
		{
			name: "all ready",
			ds: &appsv1.DaemonSet{
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 3,
					NumberReady:            3,
				},
			},
			wantStatus: statusPass,
		},
		{
			name: "partial ready",
			ds: &appsv1.DaemonSet{
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 3,
					NumberReady:            2,
				},
			},
			wantStatus: statusWarn,
		},
		{
			name: "none scheduled",
			ds: &appsv1.DaemonSet{
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 0,
				},
			},
			wantStatus: statusFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := validateDaemonSet("test", tt.ds)
			if check.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", check.Status, tt.wantStatus)
			}
		})
	}
}

func TestValidatePodsRunning(t *testing.T) {
	tests := []struct {
		name       string
		pods       []corev1.Pod
		wantStatus string
	}{
		{
			name: "all running",
			pods: []corev1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
			},
			wantStatus: statusPass,
		},
		{
			name:       "no pods",
			pods:       []corev1.Pod{},
			wantStatus: statusFail,
		},
		{
			name: "pod pending",
			pods: []corev1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pending-pod"}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
			},
			wantStatus: statusFail,
		},
		{
			name: "pod failed",
			pods: []corev1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "failed-pod"}, Status: corev1.PodStatus{Phase: corev1.PodFailed}},
			},
			wantStatus: statusFail,
		},
		{
			name: "multiple pods running",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "cm-1"},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "cm-cainjector-1"},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "cm-webhook-1"},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning},
				},
			},
			wantStatus: statusPass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := validatePodsRunning("test", tt.pods)
			if check.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", check.Status, tt.wantStatus)
			}
		})
	}
}

// API failures are not "component missing": only a typed NotFound may say
// "daemonset not found"; outages keep statusError with the cause.
func TestComponentAPIErrorsAreNotMissing(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	cs.PrependReactor("get", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	npd := CheckNodeProblemDetector(context.Background(), cs)
	require.Equal(t, statusError, npd.Status)
	require.Contains(t, npd.Message, "connection refused")

	cs2 := k8sfake.NewSimpleClientset()
	cs2.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	cm := CheckCertManager(context.Background(), cs2)
	require.Equal(t, statusError, cm.Status)

	missing := CheckNodeProblemDetector(context.Background(), k8sfake.NewSimpleClientset())
	require.Equal(t, statusFail, missing.Status)
	require.Equal(t, "daemonset not found", missing.Message)
}
