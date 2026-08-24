package custodian

import (
	"context"
	"fmt"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// HA check status values.
const (
	statusPass  = "pass"
	statusFail  = "fail"
	statusWarn  = "warn"
	statusError = "error"
	statusInfo  = "info"
)

// HA check names.
const (
	checkControlPlaneZones = "control-plane-zones"
	checkBatchTaints       = "batch-taints"
)

// HACheck represents a single validation check result.
type HACheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "pass", "fail", "warn", "error"
	Message string `json:"message"`
}

// ValidateHA runs the full HA topology validation suite.
func ValidateHA(kubeconfig string) ([]HACheck, error) {
	cs, _, err := createKubernetesClient(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	checks := []HACheck{
		checkClusterAutoscaler(ctx, cs),
		checkNodeProblemDetector(ctx, cs),
		checkMetricsServer(ctx, cs),
		checkCertManager(ctx, cs),
		checkNodeLocalDNS(ctx, cs),
		checkControlPlaneZoneSpread(ctx, cs),
		checkBatchNodeTaints(ctx, cs),
	}
	return checks, nil
}

// ── Autoscaler ──

func checkClusterAutoscaler(ctx context.Context, cs *kubernetes.Clientset) HACheck {
	return F.Pipe1(
		listPods(ctx, cs, "kube-system", "app=cluster-autoscaler"),
		E.Fold(
			func(err error) HACheck { return HACheck{"cluster-autoscaler", statusError, err.Error()} },
			func(pods []corev1.Pod) HACheck {
				return validatePodsRunning("cluster-autoscaler", pods)
			},
		),
	)
}

// ── Node Problem Detector ──

func checkNodeProblemDetector(ctx context.Context, cs *kubernetes.Clientset) HACheck {
	return F.Pipe1(
		getDaemonSet(ctx, cs, "kube-system", "node-problem-detector"),
		E.Fold(
			func(err error) HACheck {
				return HACheck{"node-problem-detector", statusFail, "daemonset not found"}
			},
			func(ds *appsv1.DaemonSet) HACheck {
				return validateDaemonSet("node-problem-detector", ds)
			},
		),
	)
}

// ── Metrics Server ──

func checkMetricsServer(ctx context.Context, cs *kubernetes.Clientset) HACheck {
	return F.Pipe1(
		listPods(ctx, cs, "kube-system", "k8s-app=metrics-server"),
		E.Fold(
			func(err error) HACheck { return HACheck{"metrics-server", statusError, err.Error()} },
			func(pods []corev1.Pod) HACheck {
				return validatePodsRunning("metrics-server", pods)
			},
		),
	)
}

// ── cert-manager ──

func checkCertManager(ctx context.Context, cs *kubernetes.Clientset) HACheck {
	ns := "cert-manager"
	return F.Pipe1(
		listPods(ctx, cs, ns, ""),
		E.Fold(
			func(err error) HACheck {
				return HACheck{"cert-manager", statusFail, "namespace cert-manager not found"}
			},
			func(pods []corev1.Pod) HACheck {
				return validatePodsRunning("cert-manager", pods)
			},
		),
	)
}

// ── Node Local DNS ──

func checkNodeLocalDNS(ctx context.Context, cs *kubernetes.Clientset) HACheck {
	return F.Pipe1(
		listPods(ctx, cs, "kube-system", "k8s-app=node-local-dns"),
		E.Fold(
			func(err error) HACheck { return HACheck{"node-local-dns", statusError, err.Error()} },
			func(pods []corev1.Pod) HACheck {
				return validatePodsRunning("node-local-dns", pods)
			},
		),
	)
}

// ── Control Plane Zone Spread ──

func checkControlPlaneZoneSpread(ctx context.Context, cs *kubernetes.Clientset) HACheck {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "node-role.kubernetes.io/control-plane",
	})
	if err != nil {
		return HACheck{checkControlPlaneZones, statusError, err.Error()}
	}

	zones := make(map[string]int)
	for _, n := range nodes.Items {
		zones[n.Labels["topology.kubernetes.io/zone"]]++
	}

	if len(zones) < 3 {
		return HACheck{checkControlPlaneZones, statusWarn,
			fmt.Sprintf("control-plane nodes spread across %d zones: %v", len(zones), zones)}
	}
	return HACheck{checkControlPlaneZones, statusPass,
		fmt.Sprintf("%d nodes across %d zones", len(nodes.Items), len(zones))}
}

// ── Batch Node Taints ──

func checkBatchNodeTaints(ctx context.Context, cs *kubernetes.Clientset) HACheck {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "pool=batch",
	})
	if err != nil {
		return HACheck{checkBatchTaints, statusError, err.Error()}
	}
	if len(nodes.Items) == 0 {
		return HACheck{checkBatchTaints, statusInfo, "no batch nodes (pool may be scaled to 0)"}
	}
	for _, n := range nodes.Items {
		hasTaint := false
		for _, t := range n.Spec.Taints {
			if t.Key == "spot" && t.Effect == "NoSchedule" {
				hasTaint = true
				break
			}
		}
		if !hasTaint {
			return HACheck{checkBatchTaints, statusFail,
				fmt.Sprintf("node %s missing spot=true:NoSchedule taint", n.Name)}
		}
	}
	return HACheck{checkBatchTaints, statusPass,
		fmt.Sprintf("%d batch nodes have spot taint", len(nodes.Items))}
}

// ── Shared validation helpers ──

// listPods returns an Either: Left error from the API call, Right list of pods.
func listPods(
	ctx context.Context,
	cs *kubernetes.Clientset,
	namespace, labelSelector string,
) E.Either[error, []corev1.Pod] {
	return IOE.TryCatchError(func() ([]corev1.Pod, error) {
		pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return nil, err
		}
		return pods.Items, nil
	})()
}

// getDaemonSet returns an Either: Left error, Right daemonset.
func getDaemonSet(
	ctx context.Context,
	cs *kubernetes.Clientset,
	namespace, name string,
) E.Either[error, *appsv1.DaemonSet] {
	return IOE.TryCatchError(func() (*appsv1.DaemonSet, error) {
		return cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	})()
}

// validatePodsRunning checks a pod list and returns the appropriate HACheck.
func validatePodsRunning(name string, pods []corev1.Pod) HACheck {
	if len(pods) == 0 {
		return HACheck{name, statusFail, "no pods found"}
	}
	for _, p := range pods {
		if p.Status.Phase != corev1.PodRunning {
			return HACheck{name, statusFail, fmt.Sprintf("pod %s is %s", p.Name, p.Status.Phase)}
		}
	}
	return HACheck{name, statusPass, fmt.Sprintf("%d pods running", len(pods))}
}

// validateDaemonSet checks a DaemonSet status and returns the appropriate HACheck.
func validateDaemonSet(name string, ds *appsv1.DaemonSet) HACheck {
	if ds.Status.DesiredNumberScheduled == 0 {
		return HACheck{name, statusFail, "no nodes scheduled"}
	}
	if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled {
		return HACheck{name, statusWarn,
			fmt.Sprintf("%d/%d ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)}
	}
	return HACheck{name, statusPass, fmt.Sprintf("%d pods ready", ds.Status.NumberReady)}
}
