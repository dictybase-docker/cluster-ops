package custodian

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
				{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
			},
			wantStatus: statusFail,
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
