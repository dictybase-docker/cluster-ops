package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ArangoAppCredentialsConfig runs the ephemeral arangosh probe per database.
type ArangoAppCredentialsConfig struct {
	Clients    kube.Clients
	Namespace  string
	SecretName string
	UserKey    string
	PassKey    string
	Image      string
	Databases  []string
}

// VerifyArangoAppCredentials verifies the Secret keys exist, then connects to
// each configured database with the app credentials from an ephemeral pod.
func VerifyArangoAppCredentials(cfg ArangoAppCredentialsConfig) []IO.IO[preflight.Result] {
	return verifyAppCredentials(cfg)
}

// ArangoConfig selects the ArangoDB verify checks.
type ArangoConfig struct {
	Clients           kube.Clients
	Namespace         string
	OperatorNamespace string
	Members           int
	Pool              string
	WantNodes         int
}

const (
	detailNoDynamicClient = "no dynamic client configured"
	detailPresent         = "present"
)

// arangoGVR is the ArangoDeployment custom resource.
var arangoGVR = schema.GroupVersionResource{
	Group: "database.arangodb.com", Version: "v1", Resource: "arangodeployments",
}

// PVC expectations hardcoded by the stack (same values the shell recipe pins).
const (
	arangoAgentPVCClass    = "dictycr-ssd"
	arangoAgentPVCSize     = "20Gi"
	arangoAgentPVCCount    = 3
	arangoDBServerPVCClass = "dictycr-balanced"
	arangoDBServerPVCSize  = "150Gi"
	arangoDBServerPVCCount = 3
)

// VerifyArangoDB is the full post-install check: pool, operator, storage
// classes, ArangoDeployment state, member pods, PVCs, coordinator Service,
// setup Job and backup CronJob. Never short-circuits.
func VerifyArangoDB(cfg ArangoConfig) []IO.IO[preflight.Result] {
	memberSelector := "arango_deployment=arangodb"
	return append(CheckPool(PoolConfig{
		Clients: cfg.Clients, Pool: cfg.Pool, WantCount: cfg.WantNodes,
	}),
		OperatorRunning(cfg.Clients, cfg.OperatorNamespace, "kube-arangodb"),
		StorageClassesPresent(cfg.Clients, arangoAgentPVCClass, arangoDBServerPVCClass),
		arangoDeploymentState(cfg.Clients, cfg.Namespace),
		PodsReadyCount(cfg.Clients, cfg.Namespace, memberSelector, cfg.Members, "member pods"),
		arangoPVCCheck(cfg.Clients, cfg.Namespace, memberSelector, "agent",
			arangoAgentPVCClass, arangoAgentPVCSize, arangoAgentPVCCount),
		arangoPVCCheck(cfg.Clients, cfg.Namespace, memberSelector, "dbserver",
			arangoDBServerPVCClass, arangoDBServerPVCSize, arangoDBServerPVCCount),
		serviceExposesPort(cfg.Clients, cfg.Namespace, memberSelector, 8529, "coordinator Service"),
		arangoSetupJob(cfg.Clients, cfg.Namespace),
		arangoBackupCronJob(cfg.Clients, cfg.Namespace),
	)
}

// arangoDeploymentState verifies the CR exists and status.phase is Running.
func arangoDeploymentState(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	name := "ArangoDeployment arangodb"
	return func() preflight.Result {
		if clients.Dynamic == nil {
			return preflight.Result{Name: name, Detail: detailNoDynamicClient, Status: preflight.StatusFail}
		}
		obj, err := clients.Dynamic.Resource(arangoGVR).Namespace(ns).Get(
			context.Background(), "arangodb", metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		phase, found, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if !found {
			return preflight.Result{Name: name, Detail: "status.phase not set yet", Status: preflight.StatusFail}
		}
		if phase != "Running" {
			return preflight.Result{
				Name: name, Detail: fmt.Sprintf("phase=%s, want Running", phase),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: name, Detail: "phase=Running", Status: preflight.StatusPass}
	}
}

// arangoPVCCheck counts Bound PVCs of one member role at the expected size
// and storage class.
func arangoPVCCheck(
	clients kube.Clients,
	ns, selector, role, class, size string,
	want int,
) IO.IO[preflight.Result] {
	name := fmt.Sprintf("%s PVCs on %s", role, class)
	return func() preflight.Result {
		wantQty := resource.MustParse(size)
		pvcs, err := clients.Kube.CoreV1().PersistentVolumeClaims(ns).List(
			context.Background(), metav1.ListOptions{LabelSelector: selector},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		count := 0
		for i := range pvcs.Items {
			pvc := &pvcs.Items[i]
			if pvc.Status.Phase != corev1.ClaimBound {
				continue
			}
			if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != class {
				continue
			}
			if !pvc.Spec.Resources.Requests.Storage().Equal(wantQty) {
				continue
			}
			count++
		}
		detail := fmt.Sprintf("%d Bound %s PVC(s) at %s on %s", count, role, size, class)
		if count == want {
			return preflight.Result{Name: name, Detail: detail, Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name: name, Detail: fmt.Sprintf("%s, expected %d", detail, want), Status: preflight.StatusFail,
		}
	}
}

// serviceExposesPort verifies at least one Service behind the selector exposes
// the port.
func serviceExposesPort(
	clients kube.Clients,
	ns, selector string,
	port int32,
	label string,
) IO.IO[preflight.Result] {
	name := label + " port " + fmt.Sprint(port)
	return func() preflight.Result {
		svcs, err := clients.Kube.CoreV1().Services(ns).List(
			context.Background(), metav1.ListOptions{LabelSelector: selector},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		count := 0
		for i := range svcs.Items {
			for _, p := range svcs.Items[i].Spec.Ports {
				if p.Port == port {
					count++
					break
				}
			}
		}
		if count >= 1 {
			return preflight.Result{
				Name: name, Detail: fmt.Sprintf("%d Service(s) expose %d in %s", count, port, ns),
				Status: preflight.StatusPass,
			}
		}
		return preflight.Result{
			Name: name, Detail: fmt.Sprintf("no Service exposing %d in %s", port, ns),
			Status: preflight.StatusFail,
		}
	}
}

// arangoSetupJob verifies the application setup Job succeeded; absent jobs
// warn (import may not be part of this cluster's flow).
func arangoSetupJob(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	const selector = "app=arangodb-create-databases"
	name := "setup Job"
	return func() preflight.Result {
		jobs, err := clients.Kube.BatchV1().Jobs(ns).List(
			context.Background(), metav1.ListOptions{LabelSelector: selector},
		)
		if err != nil {
			return lookupFailure(name, err)
		}
		if len(jobs.Items) == 0 {
			return preflight.Result{
				Name:   name,
				Detail: "none found — confirm import and Secret 'backend' state",
				Status: preflight.StatusWarn,
			}
		}
		for i := range jobs.Items {
			if jobs.Items[i].Status.Succeeded >= 1 {
				return preflight.Result{Name: name, Detail: "succeeded", Status: preflight.StatusPass}
			}
		}
		return preflight.Result{Name: name, Detail: "present but not succeeded", Status: preflight.StatusFail}
	}
}

// arangoBackupCronJob warns (does not fail) when the backup CronJob is absent.
func arangoBackupCronJob(clients kube.Clients, ns string) IO.IO[preflight.Result] {
	name := "backup CronJob arangodb-backup-cronjob"
	return func() preflight.Result {
		_, err := clients.Kube.BatchV1().CronJobs(ns).Get(
			context.Background(), "arangodb-backup-cronjob", metav1.GetOptions{},
		)
		if err == nil {
			return preflight.Result{Name: name, Detail: detailPresent, Status: preflight.StatusPass}
		}
		return preflight.Result{
			Name:   name,
			Detail: "absent — expected only if the backup stack was applied",
			Status: preflight.StatusWarn,
		}
	}
}

// verifyAppCredentials runs one ephemeral arangosh pod per database with the
// app Secret mounted via secretKeyRef env (never argv) and reports each.
func verifyAppCredentials(cfg ArangoAppCredentialsConfig) []IO.IO[preflight.Result] {
	checks := make([]IO.IO[preflight.Result], 0, len(cfg.Databases)+1)
	secretName := fmt.Sprintf("secret %s keys %s/%s", cfg.SecretName, cfg.UserKey, cfg.PassKey)
	checks = append(checks, func() preflight.Result {
		_, err := cfg.Clients.Kube.CoreV1().Secrets(cfg.Namespace).Get(
			context.Background(), cfg.SecretName, metav1.GetOptions{},
		)
		if err != nil {
			return lookupFailure(secretName, err)
		}
		return preflight.Result{Name: secretName, Detail: detailPresent, Status: preflight.StatusPass}
	})
	for _, db := range cfg.Databases {
		checks = append(checks, arangoAppProbe(cfg, db))
	}
	return checks
}

// arangoAppProbe runs one arangosh probe pod and waits for it to finish.
func arangoAppProbe(cfg ArangoAppCredentialsConfig, db string) IO.IO[preflight.Result] {
	name := fmt.Sprintf("app access %s/%s as %s", cfg.Namespace, db, cfg.SecretName)
	return func() preflight.Result {
		podName := fmt.Sprintf("arango-app-check-%s-%d", strings.ReplaceAll(db, "_", "-"), time.Now().Unix())
		container := buildArangoProbeContainer(podName, cfg, db)
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: cfg.Namespace, Name: podName},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers:    []corev1.Container{container},
			},
		}
		if _, err := cfg.Clients.Kube.CoreV1().Pods(cfg.Namespace).Create(
			context.Background(), pod, metav1.CreateOptions{},
		); err != nil {
			return lookupFailure(name, err)
		}
		phase, err := waitForPodCompletion(cfg.Clients, cfg.Namespace, podName)
		if err != nil {
			return preflight.Result{Name: name, Detail: err.Error(), Status: preflight.StatusFail}
		}
		if phase != corev1.PodSucceeded {
			return preflight.Result{
				Name: name,
				Detail: fmt.Sprintf(
					"probe pod finished with phase %s — application credentials rejected or database missing", phase,
				),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: name, Detail: "application access verified", Status: preflight.StatusPass}
	}
}

func buildArangoProbeContainer(name string, cfg ArangoAppCredentialsConfig, db string) corev1.Container {
	return corev1.Container{
		Name:    name,
		Image:   cfg.Image,
		Command: []string{"arangosh"},
		Args: []string{
			"--server.endpoint", "http+tcp://arangodb:8529",
			// $(VAR) is expanded by Kubernetes from the pod's own env — the
			// plaintext never reaches argv.
			"--server.username", "$(APP_USER)",
			"--server.password", "$(APP_PASSWORD)",
			"--server.database", db,
			"--javascript.execute-string", `db._query("RETURN 1").toArray(); print("application access verified");`,
		},
		Env: []corev1.EnvVar{
			{Name: "APP_USER", ValueFrom: secretKeyRef(cfg.SecretName, cfg.UserKey)},
			{Name: "APP_PASSWORD", ValueFrom: secretKeyRef(cfg.SecretName, cfg.PassKey)},
		},
	}
}

func secretKeyRef(secret, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key,
		},
	}
}

// waitForPodCompletion polls a pod until Succeeded/Failed (bounded).
func waitForPodCompletion(clients kube.Clients, ns, name string) (corev1.PodPhase, error) {
	deadline := time.Now().Add(300 * time.Second)
	for {
		pod, err := clients.Kube.CoreV1().Pods(ns).Get(
			context.Background(), name, metav1.GetOptions{},
		)
		if err != nil {
			return "", fmt.Errorf("probe pod %s: %w", name, err)
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			return pod.Status.Phase, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("probe pod %s did not finish in 300s", name)
		}
		time.Sleep(2 * time.Second)
	}
}
