package custodian

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/urfave/cli/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Custodian represents the main structure for the custodian operations
type Custodian struct {
	clientset *kubernetes.Clientset
	namespace string
	label     string
	logger    *slog.Logger
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
	clientset, err := createKubernetesClient(config.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return &Custodian{
		clientset: clientset,
		namespace: config.Namespace,
		label:     config.Label,
		logger:    config.Logger,
	}, nil
}

// SearchAndExtractLogs is the main entry point for the custodian operations
func (cus *Custodian) SearchAndExtractLogs(ctx *cli.Context) error {
	jobs, err := cus.listJobs()
	if err != nil {
		return fmt.Errorf("failed to list jobs: %w", err)
	}

	if len(jobs.Items) == 0 {
		cus.logger.Info("No jobs found", "label", cus.label, "namespace", cus.namespace)
		return nil
	}

	return cus.processJobs(jobs)
}

func (cus *Custodian) listJobs() (*batchv1.JobList, error) {
	return cus.clientset.BatchV1().Jobs(cus.namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: cus.label,
	})
}

func (cus *Custodian) processJobs(jobs *batchv1.JobList) error {
	for _, job := range jobs.Items {
		cus.logger.Info("Found job", "name", job.Name)

		pods, err := cus.listPodsForJob(job.Name)
		if err != nil {
			return fmt.Errorf("error listing pods for job %s: %w", job.Name, err)
		}

		if err := cus.processPodsForJob(pods); err != nil {
			return err
		}
	}
	return nil
}

func (cus *Custodian) listPodsForJob(jobName string) (*corev1.PodList, error) {
	return cus.clientset.CoreV1().Pods(cus.namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
}

func (cus *Custodian) processPodsForJob(pods *corev1.PodList) error {
	for idx := range pods.Items {
		pod := &pods.Items[idx]
		logs, err := cus.getPodLogs(pod)
		if err != nil {
			cus.logger.Error("Error getting logs for pod", "pod", pod.Name, "error", err)
			continue
		}

		cus.logger.Info("Pod logs", "pod", pod.Name, "logs", logs)
	}
	return nil
}

func (cus *Custodian) getPodLogs(pod *corev1.Pod) (string, error) {
	req := cus.clientset.CoreV1().Pods(cus.namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return "", fmt.Errorf("error in opening stream: %w", err)
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", fmt.Errorf("error in copy information from podLogs to buf: %w", err)
	}

	return buf.String(), nil
}

func createKubernetesClient(kubeconfigPath string) (*kubernetes.Clientset, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("error building kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating Kubernetes client: %w", err)
	}

	return clientset, nil
}
