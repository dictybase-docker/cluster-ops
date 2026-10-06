package waitfor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// shortCfg keeps tests fast.
// waitResult evaluates an IOEither wait into a plain error for assertions.
func waitResult(res IOE.IOEither[error, struct{}]) error {
	return E.Fold(F.Identity[error], func(struct{}) error { return nil })(res())
}

func shortCfg() waitfor.WaitConfig {
	return waitfor.WaitConfig{Timeout: 3 * time.Second, Interval: 100 * time.Millisecond}
}

func testClients(objs ...runtime.Object) (kube.Clients, *k8sfake.Clientset) {
	fakeCS := k8sfake.NewSimpleClientset(objs...)
	return kube.Clients{Kube: fakeCS}, fakeCS
}

// podNamed builds a pod with a phase, container readiness, and optional
// waiting reasons per container (empty reason = ready container).
func podNamed(ns, name, phase string, ready bool, waitingReasons ...string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{"app": "test"}},
		Status:     corev1.PodStatus{Phase: corev1.PodPhase(phase)},
	}
	if len(waitingReasons) > 0 {
		for _, r := range waitingReasons {
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Ready: ready,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: r}},
			})
		}
	} else if ready {
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Ready: true}}
	}
	return pod
}

func TestPodsReady(t *testing.T) {
	t.Run("ready pods satisfy the count", func(t *testing.T) {
		clients, _ := testClients(podNamed("dev", "a-1", "Running", true))
		err := waitResult(waitfor.PodsReady(context.Background(), clients, shortCfg(),
			waitfor.PodReadyCondition{Namespace: "dev", Selector: "app=test", Count: 1}))
		require.NoError(t, err)
	})
	t.Run("crash loop is terminal and fast", func(t *testing.T) {
		pod := podNamed("dev", "a-1", "Running", false, "CrashLoopBackOff")
		pod.Status.ContainerStatuses[0].RestartCount = 3 // repeated crashes: unambiguous
		clients, _ := testClients(pod)
		err := waitResult(waitfor.PodsReady(context.Background(), clients, shortCfg(),
			waitfor.PodReadyCondition{Namespace: "dev", Selector: "app=test", Count: 1}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTerminal, we.Kind)
		require.Contains(t, err.Error(), "CrashLoopBackOff")
	})
	t.Run("image pull error is terminal", func(t *testing.T) {
		pod := podNamed("dev", "a-1", "Pending", false, "ErrImagePull")
		pod.Status.ContainerStatuses[0].RestartCount = 3
		clients, _ := testClients(pod)
		err := waitResult(waitfor.PodsReady(context.Background(), clients, shortCfg(),
			waitfor.PodReadyCondition{Namespace: "dev", Selector: "app=test", Count: 1}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTerminal, we.Kind)
	})
	t.Run("early crash-loop restart is NOT terminal (restartCount guard)", func(t *testing.T) {
		pod := podNamed("dev", "a-1", "Running", false, "CrashLoopBackOff")
		pod.Status.ContainerStatuses[0].RestartCount = 1
		clients, _ := testClients(pod)
		err := waitResult(waitfor.PodsReady(context.Background(), clients, shortCfg(),
			waitfor.PodReadyCondition{Namespace: "dev", Selector: "app=test", Count: 1}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTimeout, we.Kind, "low restartCount keeps waiting")
	})
	t.Run("unready pods time out", func(t *testing.T) {
		clients, _ := testClients(podNamed("dev", "a-1", "Running", false))
		err := waitResult(waitfor.PodsReady(context.Background(), clients, shortCfg(),
			waitfor.PodReadyCondition{Namespace: "dev", Selector: "app=test", Count: 1}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTimeout, we.Kind)
	})
	t.Run("no matching pods times out", func(t *testing.T) {
		clients, _ := testClients()
		err := waitResult(waitfor.PodsReady(context.Background(), clients, shortCfg(),
			waitfor.PodReadyCondition{Namespace: "dev", Selector: "app=absent", Count: 1}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTimeout, we.Kind)
	})
}

func TestJobComplete(t *testing.T) {
	t.Run("succeeded job passes", func(t *testing.T) {
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "build"},
			Status: batchv1.JobStatus{Succeeded: 1}}
		clients, _ := testClients(job)
		err := waitResult(waitfor.JobComplete(context.Background(), clients, shortCfg(),
			waitfor.JobCondition{Namespace: "dev", Name: "build"}))
		require.NoError(t, err)
	})
	t.Run("failed job is terminal", func(t *testing.T) {
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "build"},
			Status: batchv1.JobStatus{Failed: 1}}
		clients, _ := testClients(job)
		err := waitResult(waitfor.JobComplete(context.Background(), clients, shortCfg(),
			waitfor.JobCondition{Namespace: "dev", Name: "build"}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTerminal, we.Kind)
	})
	t.Run("job appearing late is fine", func(t *testing.T) {
		clients, fakeCS := testClients()
		go func() {
			time.Sleep(300 * time.Millisecond)
			_, _ = fakeCS.BatchV1().Jobs("dev").Create(context.Background(),
				&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "late"},
					Status: batchv1.JobStatus{Succeeded: 1}},
				metav1.CreateOptions{})
		}()
		err := waitResult(waitfor.JobComplete(context.Background(), clients, shortCfg(),
			waitfor.JobCondition{Namespace: "dev", Name: "late"}))
		require.NoError(t, err)
	})
	t.Run("job gone after seen passes (TTL cleanup)", func(t *testing.T) {
		clients, fakeCS := testClients()
		_, _ = fakeCS.BatchV1().Jobs("dev").Create(context.Background(),
			&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "ephemeral"}},
			metav1.CreateOptions{})
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = fakeCS.BatchV1().Jobs("dev").Delete(context.Background(), "ephemeral", metav1.DeleteOptions{})
		}()
		err := waitResult(waitfor.JobComplete(context.Background(), clients, shortCfg(),
			waitfor.JobCondition{Namespace: "dev", Name: "ephemeral"}))
		require.NoError(t, err)
	})
	t.Run("never appears: timeout", func(t *testing.T) {
		clients, _ := testClients()
		err := waitResult(waitfor.JobComplete(context.Background(), clients, shortCfg(),
			waitfor.JobCondition{Namespace: "dev", Name: "ghost"}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTimeout, we.Kind)
	})
}

func TestResourcesGone(t *testing.T) {
	t.Run("no pods: gone", func(t *testing.T) {
		clients, _ := testClients()
		err := waitResult(waitfor.ResourcesGone(context.Background(), clients, shortCfg(),
			"dev", "pods", "app=test"))
		require.NoError(t, err)
	})
	t.Run("pods present then deleted", func(t *testing.T) {
		pod := podNamed("dev", "a-1", "Running", true)
		clients, fakeCS := testClients(pod)
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = fakeCS.CoreV1().Pods("dev").Delete(context.Background(), "a-1", metav1.DeleteOptions{})
		}()
		err := waitResult(waitfor.ResourcesGone(context.Background(), clients, shortCfg(),
			"dev", "pods", "app=test"))
		require.NoError(t, err)
	})
	t.Run("still present: timeout", func(t *testing.T) {
		clients, _ := testClients(podNamed("dev", "a-1", "Running", true))
		err := waitResult(waitfor.ResourcesGone(context.Background(), clients, shortCfg(),
			"dev", "pods", "app=test"))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTimeout, we.Kind)
	})
	t.Run("missing namespace counts as gone", func(t *testing.T) {
		clients, _ := testClients()
		err := waitResult(waitfor.ResourcesGone(context.Background(), clients, shortCfg(),
			"absent-ns", "pods", ""))
		require.NoError(t, err)
	})
}

func TestDeploymentRollout(t *testing.T) {
	dep := func(gen int64, observed int64, updated, available, specReplicas int32, deadlineExceeded bool) *appsv1.Deployment {
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "api", Generation: gen},
			Spec:       appsv1.DeploymentSpec{Replicas: &specReplicas},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: observed, UpdatedReplicas: updated, AvailableReplicas: available,
			},
		}
		if deadlineExceeded {
			d.Status.Conditions = []appsv1.DeploymentCondition{{
				Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse,
				Reason: "ProgressDeadlineExceeded",
			}}
		}
		return d
	}
	t.Run("fully rolled out", func(t *testing.T) {
		clients, _ := testClients(dep(2, 2, 3, 3, 3, false))
		err := waitResult(waitfor.DeploymentRollout(context.Background(), clients, shortCfg(),
			waitfor.DeploymentRolloutCondition{Namespace: "dev", Name: "api"}))
		require.NoError(t, err)
	})
	t.Run("stale observedGeneration does not pass", func(t *testing.T) {
		clients, _ := testClients(dep(2, 1, 3, 3, 3, false))
		err := waitResult(waitfor.DeploymentRollout(context.Background(), clients, shortCfg(),
			waitfor.DeploymentRolloutCondition{Namespace: "dev", Name: "api"}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTimeout, we.Kind)
	})
	t.Run("progress deadline exceeded is terminal", func(t *testing.T) {
		clients, _ := testClients(dep(2, 2, 1, 1, 3, true))
		err := waitResult(waitfor.DeploymentRollout(context.Background(), clients, shortCfg(),
			waitfor.DeploymentRolloutCondition{Namespace: "dev", Name: "api"}))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTerminal, we.Kind)
		require.Contains(t, err.Error(), "ProgressDeadlineExceeded")
	})
}

func TestCRDEstablished(t *testing.T) {
	crd := func(established bool) *apiextv1.CustomResourceDefinition {
		c := &apiextv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "arangodeployments.database.arangodb.com"},
		}
		if established {
			c.Status.Conditions = []apiextv1.CustomResourceDefinitionCondition{
				{Type: apiextv1.Established, Status: apiextv1.ConditionTrue},
			}
		}
		return c
	}
	t.Run("established passes", func(t *testing.T) {
		apiext := apiextfake.NewSimpleClientset(crd(true))
		err := waitResult(waitfor.CRDEstablished(context.Background(), apiext.ApiextensionsV1(), shortCfg(),
			"arangodeployments.database.arangodb.com"))
		require.NoError(t, err)
	})
	t.Run("not established times out", func(t *testing.T) {
		apiext := apiextfake.NewSimpleClientset(crd(false))
		err := waitResult(waitfor.CRDEstablished(context.Background(), apiext.ApiextensionsV1(), shortCfg(),
			"arangodeployments.database.arangodb.com"))
		var we *waitfor.Error
		require.True(t, errors.As(err, &we))
		require.Equal(t, waitfor.KindTimeout, we.Kind)
	})
}
