package waitfor

import (
	"context"
	"fmt"
	"strings"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// coreKinds maps the shell recipe's `kubectl get` kind aliases to typed list
// functions. Anything else goes through discovery + the dynamic client.
//
//nolint:cyclop // one case per kubectl kind alias; linear dispatch
func countResources(
	ctx context.Context,
	clients kube.Clients,
	namespace, kind, selector string,
) (int, error) {
	listOpts := metav1.ListOptions{LabelSelector: selector}
	switch strings.ToLower(kind) {
	case "pods", "pod", "po":
		list, err := clients.Kube.CoreV1().Pods(namespace).List(ctx, listOpts)
		if isGone(err) {
			return 0, nil
		}
		return len(list.Items), apiWrap(err, "list pods")
	case "services", "service", "svc":
		list, err := clients.Kube.CoreV1().Services(namespace).List(ctx, listOpts)
		if isGone(err) {
			return 0, nil
		}
		return len(list.Items), apiWrap(err, "list services")
	case "persistentvolumeclaims", "persistentvolumeclaim", "pvc":
		list, err := clients.Kube.CoreV1().PersistentVolumeClaims(namespace).List(ctx, listOpts)
		if isGone(err) {
			return 0, nil
		}
		return len(list.Items), apiWrap(err, "list PVCs")
	case "jobs", "job":
		list, err := clients.Kube.BatchV1().Jobs(namespace).List(ctx, listOpts)
		if isGone(err) {
			return 0, nil
		}
		return len(list.Items), apiWrap(err, "list jobs")
	case "deployments", "deployment", "deploy":
		list, err := clients.Kube.AppsV1().Deployments(namespace).List(ctx, listOpts)
		if isGone(err) {
			return 0, nil
		}
		return len(list.Items), apiWrap(err, "list deployments")
	case "namespaces", "namespace", "ns":
		if selector != "" {
			list, err := clients.Kube.CoreV1().Namespaces().List(ctx, listOpts)
			if isGone(err) {
				return 0, nil
			}
			return len(list.Items), apiWrap(err, "list namespaces")
		}
		_, err := clients.Kube.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if isGone(err) {
			return 0, nil
		}
		return boolCount(err == nil), apiWrap(err, "get namespace")
	default:
		return countDynamic(ctx, clients, namespace, kind, selector)
	}
}

// countDynamic resolves kind through discovery (the shell passed kubectl
// aliases like `arangodeployment` or full plurals) and lists with the dynamic
// client. An unresolvable kind — the CRD does not exist — counts as gone.
func countDynamic(
	ctx context.Context,
	clients kube.Clients,
	namespace, kind, selector string,
) (int, error) {
	gvr, err := resolveGVR(ctx, clients, kind)
	if err != nil || gvr.Empty() {
		return 0, nil // unknown API type: gone (missing CRD counts as gone)
	}
	list, err := clients.Dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if isGone(err) {
		return 0, nil
	}
	if err != nil {
		return 0, apiErr(err, "list %s in %s", kind, namespace)
	}
	return len(list.Items), nil
}

// resolveGVR maps a user kind to a GroupVersionResource via discovery.
func resolveGVR(ctx context.Context, clients kube.Clients, kind string) (schema.GroupVersionResource, error) {
	if clients.Dynamic == nil {
		return schema.GroupVersionResource{}, fmt.Errorf("no dynamic client")
	}
	groups, err := clients.Kube.Discovery().ServerPreferredResources()
	if err != nil {
		// Partial lists are fine; discovery tolerates missing groups.
		if groups == nil {
			return schema.GroupVersionResource{}, nil
		}
	}
	wanted := strings.ToLower(kind)
	for _, gvList := range groups {
		for _, r := range gvList.APIResources {
			names := []string{
				strings.ToLower(r.Kind),
				r.Name, // plural
				strings.ToLower(r.Name),
			}
			if r.SingularName != "" {
				names = append(names, strings.ToLower(r.SingularName))
			}
			for _, n := range names {
				if n == wanted {
					gv, err := schema.ParseGroupVersion(gvList.GroupVersion)
					if err != nil {
						return schema.GroupVersionResource{}, nil
					}
					return gv.WithResource(r.Name), nil
				}
			}
		}
	}
	return schema.GroupVersionResource{}, nil
}

// isGone reports whether an API error means the resource namespace or API
// type no longer exists — gone, in the deletion-wait sense.
func isGone(err error) bool {
	if err == nil {
		return false
	}
	if kube.Classify(err) == kube.ReasonNotFound {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "doesn't have a resource type") ||
		strings.Contains(msg, "the server could not find the requested resource") ||
		strings.Contains(msg, "not found")
}

func apiWrap(err error, what string) error {
	if err == nil {
		return nil
	}
	return apiErr(err, "%s", what)
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}
