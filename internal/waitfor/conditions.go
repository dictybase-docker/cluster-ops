package waitfor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CustomResourceCondition waits for one custom resource's status field to
// reach a value (e.g. ArangoDeployment status.phase=Running, CNPG Cluster
// status.phase="Cluster in healthy state").
type CustomResourceCondition struct {
	Namespace     string
	Name          string
	Group         string
	Version       string
	Kind          string
	StatusPath    string   // dot path under .status, e.g. "state" or "phase"
	ExpectedValue string   // success value
	FailedValues  []string // same path values that fail fast (e.g. "Failed")
}

// StorageClassCondition waits for named StorageClasses with a provisioner.
type StorageClassCondition struct {
	Classes     []string
	Provisioner string
}

// NodeCondition waits for N Ready nodes behind a label selector.
type NodeCondition struct {
	Selector string
	Count    int
}

// CustomResourceStatus polls the CR's status field until it equals
// ExpectedValue; any FailedValues match is terminal. The GVK is validated by
// discovery on the first poll — an unknown kind fails fast.
func CustomResourceStatus(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond CustomResourceCondition,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return customResourceStatus(ctx, clients, cfg, cond) })
}

func customResourceStatus(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond CustomResourceCondition,
) error {
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		gvr, err := resolveGVR(clients, cond.Kind)
		if err != nil {
			return false, apiErr(err, "resolve kind %s", cond.Kind)
		}
		if gvr.Empty() {
			//nolint:lll // one message, one line
			return false, apiErr(fmt.Errorf("kind %s not found in the API server (CRD not installed?)", cond.Kind), "resolve kind")
		}
		obj, err := clients.Dynamic.Resource(gvr).Namespace(cond.Namespace).Get(pollCtx, cond.Name, metav1.GetOptions{})
		if err != nil {
			return false, apiErr(err, "get %s %s/%s", cond.Kind, cond.Namespace, cond.Name)
		}
		path := append([]string{"status"}, strings.Split(cond.StatusPath, ".")...)
		value, found, err := unstructured.NestedString(obj.Object, path...)
		if err != nil || !found {
			logf(cfg, "Waiting for %s %s/%s: status.%s not set (%s elapsed)...",
				cond.Kind, cond.Namespace, cond.Name, cond.StatusPath, elapsed)
			return false, nil
		}
		if value == cond.ExpectedValue {
			logf(cfg, "%s %s/%s reached status.%s=%q after %s.",
				cond.Kind, cond.Namespace, cond.Name, cond.StatusPath, value, elapsed)
			return true, nil
		}
		if slices.Contains(cond.FailedValues, value) {
			return false, terminalErr("%s %s/%s: status.%s=%q",
				cond.Kind, cond.Namespace, cond.Name, cond.StatusPath, value)
		}
		logf(cfg, "Waiting for %s %s/%s: status.%s=%q, want %q (%s elapsed)...",
			cond.Kind, cond.Namespace, cond.Name, cond.StatusPath, value, cond.ExpectedValue, elapsed)
		return false, nil
	})
}

// StorageClassesReady polls until every named StorageClass exists with the
// expected provisioner.
func StorageClassesReady(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond StorageClassCondition,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return storageClassesReady(ctx, clients, cfg, cond) })
}

func storageClassesReady(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond StorageClassCondition,
) error {
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		for _, name := range cond.Classes {
			sc, err := clients.Kube.StorageV1().StorageClasses().Get(pollCtx, name, metav1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				logf(cfg, "Waiting for StorageClass %s (%s elapsed)...", name, elapsed)
				return false, nil
			}
			if err != nil {
				return false, apiErr(err, "get StorageClass %s", name)
			}
			if sc.Provisioner != cond.Provisioner {
				return false, terminalErr("StorageClass %s has provisioner %q, want %q",
					name, sc.Provisioner, cond.Provisioner)
			}
		}
		logf(cfg, "StorageClasses %s ready (provisioner %s) after %s.",
			strings.Join(cond.Classes, ","), cond.Provisioner, elapsed)
		return true, nil
	})
}

// NodesReady polls until Count nodes matching Selector report Ready=True.
func NodesReady(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond NodeCondition,
) IOE.IOEither[error, struct{}] {
	return asIOE(func() error { return nodesReady(ctx, clients, cfg, cond) })
}

func nodesReady(
	ctx context.Context,
	clients kube.Clients,
	cfg WaitConfig,
	cond NodeCondition,
) error {
	return poll(ctx, cfg, func(pollCtx context.Context, elapsed time.Duration) (bool, error) {
		nodes, err := clients.Kube.CoreV1().Nodes().List(pollCtx, metav1.ListOptions{
			LabelSelector: cond.Selector,
		})
		if err != nil {
			return false, apiErr(err, "list nodes (%s)", cond.Selector)
		}
		ready := 0
		for i := range nodes.Items {
			for _, c := range nodes.Items[i].Status.Conditions {
				if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
					ready++
					break
				}
			}
		}
		if ready >= cond.Count {
			logf(cfg, "%d/%d node(s) ready in %s after %s.", ready, cond.Count, cond.Selector, elapsed)
			return true, nil
		}
		logf(cfg, "Waiting for %d node(s) (%s): %d/%d ready (%s elapsed)",
			cond.Count, cond.Selector, ready, cond.Count, elapsed)
		return false, nil
	})
}
