// Package waitfor owns every Kubernetes wait/poll condition the recipes need:
// typed poll loops with progress logging, terminal-failure detection, and
// classified errors (timeout / terminal / API) that map to CLI exit codes.
package waitfor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1client "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Kind classifies a wait failure; the CLI maps it to exit codes
// 1 (timeout), 2 (terminal), 3 (API).
type Kind int

const (
	// KindTimeout: the condition was not met within the budget.
	KindTimeout Kind = iota
	// KindTerminal: a terminal failure was observed (CrashLoopBackOff,
	// Job failed, ProgressDeadlineExceeded) — fail fast, do not retry.
	KindTerminal
	// KindAPI: the API server errored in a non-condition way.
	KindAPI
)

// Error is the classified wait failure.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func (e *Error) Unwrap() error { return nil }

func terminalErr(format string, args ...any) *Error {
	return &Error{Kind: KindTerminal, Msg: fmt.Sprintf(format, args...)}
}

func apiErr(err error, format string, args ...any) *Error {
	return &Error{Kind: KindAPI, Msg: fmt.Sprintf("%s: %v", fmt.Sprintf(format, args...), err)}
}

// WaitConfig sets the poll budget. Zero Interval defaults to 5s.
type WaitConfig struct {
	Timeout  time.Duration
	Interval time.Duration
	Logger   *slog.Logger // optional progress logging
}

// PodReadyCondition waits for N ready pods behind a label selector.
type PodReadyCondition struct {
	Namespace string
	Selector  string
	Count     int
}

// JobCondition waits for one Job to succeed.
type JobCondition struct {
	Namespace string
	Name      string
}

// DeploymentRolloutCondition waits for one Deployment to roll out.
type DeploymentRolloutCondition struct {
	Namespace string
	Name      string
}

// terminalWaiting reasons: a container parked here with repeat restarts will
// not become ready by itself.
var terminalWaiting = map[string]bool{
	"CrashLoopBackOff": true,
	"ImagePullBackOff": true,
	"ErrImagePull":     true,
}

// poll runs cond every cfg.Interval until it reports done, errors, or the
// budget expires. Condition errors must be *Error (Terminal/API) to survive;
// anything else is reported as an API error.
//
//nolint:lll // single-line condition signature
func poll(
	ctx context.Context,
	cfg WaitConfig,
	cond func(ctx context.Context, elapsed time.Duration) (bool, error),
) error {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	start := time.Now()
	err := wait.PollUntilContextTimeout(ctx, interval, cfg.Timeout, true,
		func(pollCtx context.Context) (bool, error) {
			return cond(pollCtx, time.Since(start))
		})
	if err == nil {
		return nil
	}
	if we, ok := errors.AsType[*Error](err); ok {
		return we
	}
	//nolint:staticcheck // SA1019: ErrWaitTimeout still returned by this apimachinery version
	if errors.Is(err, wait.ErrWaitTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: KindTimeout, Msg: fmt.Sprintf("timed out after %s", cfg.Timeout)}
	}
	if ctx.Err() != nil {
		return &Error{Kind: KindAPI, Msg: fmt.Sprintf("wait cancelled: %v", ctx.Err())}
	}
	return &Error{Kind: KindAPI, Msg: fmt.Sprintf("wait failed: %v", err)}
}

// PodsReady polls until Count pods matching Selector are Running with every
// container ready. Fails fast on CrashLoopBackOff / ImagePullBackOff once a
// container has restarted at least twice (a single restart during startup is
// not a crash loop).
func PodsReady(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond PodReadyCondition,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return podsReady(ctx, clients, cfg, cond) })
}

func podsReady(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond PodReadyCondition,
) error {
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		pods, err := clients.Kube.CoreV1().Pods(cond.Namespace).List(pollCtx, metav1.ListOptions{
			LabelSelector: cond.Selector,
		})
		if err != nil {
			return false, apiErr(err, "list pods in %s (%s)", cond.Namespace, cond.Selector)
		}
		ready := 0
		for i := range pods.Items {
			if podReady(&pods.Items[i], cond.Count) {
				ready++
			}
		}
		if ready >= cond.Count {
			logf(cfg, "%d/%d pod(s) ready in %s (%s) after %s",
				ready, cond.Count, cond.Namespace, cond.Selector, elapsed)
			return true, nil
		}
		if terminal := findTerminalPod(&pods.Items); terminal != "" {
			return false, terminalErr("%s", terminal)
		}
		logf(cfg, "Waiting for %d pod(s) in %s (%s): %d/%d ready (%s elapsed)",
			cond.Count, cond.Namespace, cond.Selector, ready, cond.Count, elapsed)
		return false, nil
	})
}

// podReady reports whether one pod is Running with every container ready.
func podReady(pod *corev1.Pod, _ int) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	statuses := pod.Status.ContainerStatuses
	if len(statuses) == 0 {
		return false
	}
	for _, cs := range statuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

// findTerminalPod names the first pod parked in a terminal waiting state with
// at least two restarts; empty when none.
func findTerminalPod(pods *[]corev1.Pod) string {
	for i := range *pods {
		pod := &(*pods)[i]
		for _, cs := range pod.Status.ContainerStatuses {
			waiting := cs.State.Waiting
			if waiting == nil || !terminalWaiting[waiting.Reason] {
				continue
			}
			if int(cs.RestartCount) < 2 {
				continue // single restart during startup is not a crash loop
			}
			return fmt.Sprintf(
				"pod %s in %s: %s (restarts: %d, reason: %s, message: %s)",
				pod.Name, pod.Namespace, waiting.Reason, cs.RestartCount,
				waiting.Reason, waiting.Message,
			)
		}
	}
	return ""
}

// JobComplete polls until the Job succeeds. A Job seen once and then deleted
// counts as done (TTL cleanup). status.failed >= 1 is terminal: the job's pod
// logs (last 100 lines) are tailled best-effort into the error.
func JobComplete(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond JobCondition,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return jobComplete(ctx, clients, cfg, cond) })
}

func jobComplete(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond JobCondition,
) error {
	seen := false
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		job, err := clients.Kube.BatchV1().Jobs(cond.Namespace).Get(pollCtx, cond.Name, metav1.GetOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				if seen {
					logf(cfg, "Job %s is gone from %s — it finished and its TTL removed it.", cond.Name, cond.Namespace)
					return true, nil
				}
				logf(cfg, "Waiting for job %s to appear in %s (%s elapsed)...", cond.Name, cond.Namespace, elapsed)
				return false, nil
			}
			return false, apiErr(err, "get job %s/%s", cond.Namespace, cond.Name)
		}
		seen = true
		if job.Status.Succeeded >= 1 {
			logf(cfg, "Job %s completed in %s after %s.", cond.Name, cond.Namespace, elapsed)
			return true, nil
		}
		if job.Status.Failed >= 1 {
			logs := tailJobLogs(pollCtx, clients, cond.Namespace, cond.Name)
			return false, terminalErr("job %s in %s failed%s. Last 100 log lines:\n%s",
				cond.Name, cond.Namespace, elapsed, logs)
		}
		logf(cfg, "Waiting for job %s in %s (%s elapsed)...", cond.Name, cond.Namespace, elapsed)
		return false, nil
	})
}

func tailJobLogs(ctx context.Context, clients kube.Clients, ns, job string) string {
	pods, err := clients.Kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + job,
	})
	if err != nil || len(pods.Items) == 0 {
		return "(no pod logs available)"
	}
	tail := int64(100)
	req := clients.Kube.CoreV1().Pods(ns).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
		TailLines: &tail,
	})
	out, err := req.DoRaw(ctx)
	if err != nil {
		return "(no pod logs available)"
	}
	return string(out)
}

// ResourcesGone polls until no resources of kind remain in the namespace.
// Core kinds use the typed client; every other kind is resolved through
// discovery and listed with the dynamic client. A missing namespace, missing
// API type, or missing resource all count as gone.
func ResourcesGone(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	namespace string,
	kind string,
	selector string,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return resourcesGone(ctx, clients, cfg, namespace, kind, selector) })
}

func resourcesGone(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	namespace string,
	kind string,
	selector string,
) error {
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		count, err := countResources(pollCtx, clients, namespace, kind, selector)
		if err != nil {
			return false, err
		}
		if count == 0 {
			logf(cfg, "%s gone in %s after %s.", kind, namespace, elapsed)
			return true, nil
		}
		logf(cfg, "Waiting for %s in %s to go (%d left, %s elapsed)...", kind, namespace, count, elapsed)
		return false, nil
	})
}

// DeploymentRollout polls until the Deployment's observedGeneration matches
// metadata.generation and every replica is updated and available.
// ProgressDeadlineExceeded is terminal.
func DeploymentRollout(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond DeploymentRolloutCondition,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return deploymentRollout(ctx, clients, cfg, cond) })
}

func deploymentRollout(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond DeploymentRolloutCondition,
) error {
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		dep, err := clients.Kube.AppsV1().Deployments(cond.Namespace).Get(pollCtx, cond.Name, metav1.GetOptions{})
		if err != nil {
			return false, apiErr(err, "get deployment %s/%s", cond.Namespace, cond.Name)
		}
		want := int32(1)
		if dep.Spec.Replicas != nil {
			want = *dep.Spec.Replicas
		}
		for _, c := range dep.Status.Conditions {
			if c.Type == appsv1.DeploymentProgressing &&
				c.Status == corev1.ConditionFalse &&
				c.Reason == "ProgressDeadlineExceeded" {
				return false, terminalErr("deployment %s/%s rollout failed: ProgressDeadlineExceeded (%s)",
					cond.Namespace, cond.Name, c.Message)
			}
		}
		if dep.Status.ObservedGeneration >= dep.Generation &&
			dep.Status.UpdatedReplicas >= want &&
			dep.Status.AvailableReplicas >= want {
			logf(cfg, "deployment %s/%s rolled out after %s.", cond.Namespace, cond.Name, elapsed)
			return true, nil
		}
		logf(cfg, "Waiting for deployment %s/%s: %d/%d updated, %d/%d available, generation %d/%d (%s elapsed)",
			cond.Namespace, cond.Name, dep.Status.UpdatedReplicas, want,
			dep.Status.AvailableReplicas, want,
			dep.Status.ObservedGeneration, dep.Generation, elapsed)
		return false, nil
	})
}

// CRDEstablished polls until the CRD reports Established=True.
func CRDEstablished(
	ctx context.Context,
	client apiextv1client.ApiextensionsV1Interface,
	cfg WaitConfig,
	crdName string,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return crdEstablished(ctx, client, cfg, crdName) })
}

func crdEstablished(
	ctx context.Context,
	client apiextv1client.ApiextensionsV1Interface,
	cfg WaitConfig,
	crdName string,
) error {
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		crd, err := client.CustomResourceDefinitions().Get(pollCtx, crdName, metav1.GetOptions{})
		if err != nil {
			return false, apiErr(err, "get CRD %s", crdName)
		}
		for _, c := range crd.Status.Conditions {
			if c.Type == apiextv1.Established && c.Status == apiextv1.ConditionTrue {
				logf(cfg, "CRD %s established after %s.", crdName, elapsed)
				return true, nil
			}
		}
		logf(cfg, "Waiting for CRD %s to establish (%s elapsed)...", crdName, elapsed)
		return false, nil
	})
}

// asIOE lifts a blocking wait into IOEither.
func asIOE(run func() error) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) { return struct{}{}, run() })
}

func logf(cfg WaitConfig, format string, args ...any) {
	if cfg.Logger != nil {
		cfg.Logger.Info(fmt.Sprintf(format, args...))
	}
}
