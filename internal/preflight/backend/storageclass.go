package backend

import (
	"context"
	"strings"
	"sync"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultClassAnnotation marks the cluster-default StorageClass.
const defaultClassAnnotation = "storageclass.kubernetes.io/is-default-class"

const (
	detailCannotReach = "cannot reach the cluster — check KUBECONFIG"
	detailNotChecked  = "not checked — cannot reach the cluster"
)

// StorageClassConfig selects the storageclass check (migrated from the
// check-storageclass recipe).
type StorageClassConfig struct {
	Clients     kube.Clients
	Classes     []string // already split; wrappers split the --classes CSV
	Provisioner string
}

type classFetch func() ([]storagev1.StorageClass, error)

// CheckStorageClass returns one check per requested class plus one trailing
// Info check for the default class. The class list is fetched once and shared;
// an unreachable cluster is reported as a single FAIL (the remaining checks
// WARN "not checked"), never once per check.
func CheckStorageClass(cfg StorageClassConfig) []IO.IO[preflight.Result] {
	fetch := sync.OnceValues(func() ([]storagev1.StorageClass, error) {
		list, err := cfg.Clients.Kube.StorageV1().StorageClasses().List(
			context.Background(), metav1.ListOptions{},
		)
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	})
	checks := make([]IO.IO[preflight.Result], 0, len(cfg.Classes)+1)
	for i, class := range cfg.Classes {
		checks = append(checks, checkClass(fetch, class, cfg.Provisioner, i == 0))
	}
	return append(checks, checkDefaultClass(fetch, len(cfg.Classes) == 0))
}

func checkClass(
	fetch classFetch, class, provisioner string, first bool,
) IO.IO[preflight.Result] {
	return func() preflight.Result {
		items, err := fetch()
		if err != nil {
			return outageResult(first)
		}
		name := "StorageClass " + class
		for _, sc := range items {
			if sc.Name != class {
				continue
			}
			if sc.Provisioner == provisioner {
				return preflight.Result{
					Name: name, Detail: "uses " + sc.Provisioner, Status: preflight.StatusPass,
				}
			}
			return preflight.Result{
				Name:   name,
				Detail: "uses " + sc.Provisioner + ", expected " + provisioner,
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name:   name,
			Detail: "missing — is it declared in storage_class/Pulumi.<stack>.yaml?",
			Status: preflight.StatusFail,
		}
	}
}

func checkDefaultClass(fetch classFetch, first bool) IO.IO[preflight.Result] {
	return func() preflight.Result {
		items, err := fetch()
		if err != nil {
			return outageResult(first)
		}
		names := []string{}
		for _, sc := range items {
			if sc.Annotations[defaultClassAnnotation] == "true" {
				names = append(names, sc.Name)
			}
		}
		if len(names) == 0 {
			return preflight.Result{
				Name:   "default StorageClass",
				Detail: "no default StorageClass set — every PVC must name one explicitly",
				Status: preflight.StatusInfo,
			}
		}
		return preflight.Result{
			Name:   "default StorageClass",
			Detail: strings.Join(names, ","),
			Status: preflight.StatusInfo,
		}
	}
}

// outageResult reports the unreachable cluster exactly once (the first check)
// and marks the remaining checks as not run — never short-circuiting, never
// repeating the outage per check.
func outageResult(first bool) preflight.Result {
	if first {
		return preflight.Result{Name: "storageclass", Detail: detailCannotReach, Status: preflight.StatusFail}
	}
	return preflight.Result{Name: "storageclass", Detail: detailNotChecked, Status: preflight.StatusWarn}
}
