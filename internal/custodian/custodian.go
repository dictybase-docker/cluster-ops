package custodian

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"

	"github.com/urfave/cli/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Custodian represents the main structure for the custodian operations
type Custodian struct {
	clientset       *kubernetes.Clientset
	dynamicClient   dynamic.Interface
	discoveryClient *discovery.DiscoveryClient
	namespace       string
	label           string
	logger          *slog.Logger
}

// CustodianConfig holds the configuration for creating a new Custodian
type CustodianConfig struct {
	KubeconfigPath string
	Namespace      string
	Label          string
	Logger         *slog.Logger
}

// NewCustodian creates a new Custodian instance
func NewCustodian(config CustodianConfig) (*Custodian, error) {
	clientset, cfg, err := createKubernetesClient(config.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	return &Custodian{
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
		namespace:       config.Namespace,
		label:           config.Label,
		logger:          config.Logger,
	}, nil
}

// SearchAndExtractLogs is the main entry point for the custodian operations
func (cus *Custodian) SearchAndExtractLogs(ctx *cli.Context) error {
	jobs, err := cus.listJobs()
	if err != nil {
		return fmt.Errorf("failed to list jobs: %w", err)
	}

	if len(jobs.Items) == 0 {
		cus.logger.Info(
			"No jobs found",
			"label",
			cus.label,
			"namespace",
			cus.namespace,
		)
		return nil
	}

	return cus.processJobs(jobs)
}

func (cus *Custodian) listJobs() (*batchv1.JobList, error) {
	return cus.clientset.BatchV1().
		Jobs(cus.namespace).
		List(context.TODO(), metav1.ListOptions{
			LabelSelector: cus.label,
		})
}

func (cus *Custodian) processJobs(jobs *batchv1.JobList) error {
	for _, job := range jobs.Items {
		cus.logger.Info("Found job", "name", job.Name)

		pods, err := cus.listPodsForJob(job.Name)
		if err != nil {
			return fmt.Errorf(
				"error listing pods for job %s: %w",
				job.Name,
				err,
			)
		}

		if err := cus.processPodsForJob(pods); err != nil {
			return err
		}
	}
	return nil
}

func (cus *Custodian) listPodsForJob(jobName string) (*corev1.PodList, error) {
	return cus.clientset.CoreV1().
		Pods(cus.namespace).
		List(context.TODO(), metav1.ListOptions{
			LabelSelector: fmt.Sprintf("job-name=%s", jobName),
		})
}

func (cus *Custodian) processPodsForJob(pods *corev1.PodList) error {
	for idx := range pods.Items {
		pod := &pods.Items[idx]
		logs, err := cus.getPodLogs(pod)
		if err != nil {
			cus.logger.Error(
				"Error getting logs for pod",
				"pod",
				pod.Name,
				"error",
				err,
			)
			continue
		}

		cus.logger.Info("Pod logs", "pod", pod.Name, "logs", logs)
	}
	return nil
}

func (cus *Custodian) getPodLogs(pod *corev1.Pod) (string, error) {
	req := cus.clientset.CoreV1().
		Pods(cus.namespace).
		GetLogs(pod.Name, &corev1.PodLogOptions{})
	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return "", fmt.Errorf("error in opening stream: %w", err)
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", fmt.Errorf(
			"error in copy information from podLogs to buf: %w",
			err,
		)
	}

	return buf.String(), nil
}

func createKubernetesClient(
	kubeconfigPath string,
) (*kubernetes.Clientset, *rest.Config, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("error building kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating Kubernetes client: %w", err)
	}

	return clientset, cfg, nil
}

// ExcludeFromBackup adds the 'velero.io/exclude-from-backup=true' label to resources
// labeled with 'app.kubernetes.io/name=kube-arangodb'
func (cus *Custodian) ExcludeFromBackup() error {
	resources, err := cus.discoveryClient.ServerPreferredResources()
	if err != nil {
		return fmt.Errorf("failed to get server resources: %w", err)
	}

	for _, list := range resources {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			return fmt.Errorf(
				"Skipping invalid groupVersion %s with error %w",
				list.GroupVersion,
				err,
			)
		}

		for _, resource := range list.APIResources {
			err := cus.processAPIResource(gv, resource)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (cus *Custodian) processAPIResource(
	gv schema.GroupVersion,
	resource metav1.APIResource,
) error {
	if !cus.hasVerbs(resource, "list", "update") {
		return nil // No error; resource doesn't have required verbs
	}

	gvr := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resource.Name,
	}

	unstructuredList, err := cus.dynamicClient.
		Resource(gvr).Namespace(cus.namespace).
		List(context.TODO(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=kube-arangodb",
		})
	if err != nil {
		// Log a warning if the resource is not found and return
		if apierrors.IsNotFound(err) {
			cus.logger.Warn(
				"Resource not found",
				"resource", resource.Name,
				"group", gv.Group,
				"version", gv.Version,
			)
			return nil
		}
		return fmt.Errorf(
			"failed to list resources for %s: %w",
			resource.Name,
			err,
		)
	}

	for idx := range unstructuredList.Items {
		item := &unstructuredList.Items[idx]
		err := cus.updateResourceLabel(gvr, item, resource.Name)
		if err != nil {
			return fmt.Errorf(
				"failed to update resource %s: %w",
				item.GetName(),
				err,
			)
		}
	}
	return nil
}

func (cus *Custodian) updateResourceLabel(
	gvr schema.GroupVersionResource,
	item *unstructured.Unstructured,
	resourceName string,
) error {
	labels := item.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["velero.io/exclude-from-backup"] = "true"
	item.SetLabels(labels)

	_, err := cus.dynamicClient.Resource(gvr).
		Namespace(cus.namespace).
		Update(context.TODO(), item, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf(
			"failed to update resource %s/%s: %w",
			resourceName,
			item.GetName(),
			err,
		)
	}
	cus.logger.Info(
		"Updated resource",
		"resource",
		resourceName,
		"name",
		item.GetName(),
	)
	return nil
}

// hasVerbs checks if the given resource has all the specified verbs
func (cus *Custodian) hasVerbs(
	resource metav1.APIResource,
	verbs ...string,
) bool {
	for _, verb := range verbs {
		if !slices.Contains(resource.Verbs, verb) {
			return false
		}
	}
	return true
}
