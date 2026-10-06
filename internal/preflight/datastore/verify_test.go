package datastore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/datastore"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// readyPod builds a Running pod with one ready container.
func readyPod(ns, name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Ready: true}},
		},
	}
}

const (
	fixNS = "prod"
)

var (
	arangoGVR      = schema.GroupVersionResource{Group: "database.arangodb.com", Version: "v1", Resource: "arangodeployments"}
	cnpgGVR        = schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}
	schedBackupGVR = schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "scheduledbackups"}
)

func arangoCR(ns string, state string) *unstructured.Unstructured { //nolint:unparam // fixture symmetry //nolint:unparam // fixture symmetry //nolint:goconst,lll,unparam // fixture literals
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "database.arangodb.com/v1",
		"kind":       "ArangoDeployment", //nolint:goconst // fixture literal
		"metadata":   map[string]any{"namespace": ns, "name": "arangodb"},
		"status":     map[string]any{"state": state},
	}}
	obj.SetGroupVersionKind(arangoGVR.GroupVersion().WithKind("ArangoDeployment"))
	return obj
}

func scheduledBackupCR(ns, name string) *unstructured.Unstructured { //nolint:goconst,lll,unparam // fixture literals
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "ScheduledBackup",
		"metadata":   map[string]any{"namespace": ns, "name": name},
	}}
	obj.SetGroupVersionKind(schedBackupGVR.GroupVersion().WithKind("ScheduledBackup"))
	return obj
}

func cnpgCluster(ns, name, phase string) *unstructured.Unstructured { //nolint:unparam // fixture symmetry //nolint:goconst,lll,unparam // fixture literals
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster", //nolint:goconst // fixture literal
		"metadata":   map[string]any{"namespace": ns, "name": name},
		"status":     map[string]any{"phase": phase},
	}}
	obj.SetGroupVersionKind(cnpgGVR.GroupVersion().WithKind("Cluster"))
	return obj
}

// dynamicClients wires a fake dynamic client into kube.Clients.
func dynamicClients(objs ...runtime.Object) kube.Clients {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			arangoGVR:      "ArangoDeploymentList",
			cnpgGVR:        "ClusterList",
			schedBackupGVR: "ScheduledBackupList",
		}, objs...)
	return kube.Clients{Dynamic: dyn}
}

//nolint:funlen,cyclop // table fixtures are linear
func TestVerifyArangoDB(t *testing.T) { //nolint:funlen,cyclop,lll,goconst // fixture-heavy
	//nolint:lll,goconst // fixture literals
	baseObjs := func() []runtime.Object {
		objs := []runtime.Object{
			nodeByName("n1", withTaint), nodeByName("n2", withTaint), nodeByName("n3", withTaint),
		}
		for i := range 9 {
			objs = append(objs, readyPod(fixNS, fmt.Sprintf("member-%d", i),
				map[string]string{"arango_deployment": "arangodb"}))
		}
		objs = append(objs,
			readyPod(fixNS, "operator-0", map[string]string{"app.kubernetes.io/name": "kube-arangodb"}),
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "arangodb",
					Labels: map[string]string{"arango_deployment": "arangodb"}},
				Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8529}}},
			},
			&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "setup",
				Labels: map[string]string{"app": "arangodb-create-databases"}},
				Status: batchv1.JobStatus{Succeeded: 1}},
			&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "arangodb-backup-cronjob"}},
		)
		for i := range 3 {
			objs = append(objs, pvcObj(fixNS, fmt.Sprintf("agent-%d", i),
				"dictycr-ssd", "20Gi", "Bound", map[string]string{"arango_deployment": "arangodb"}))
		}
		for i := range 3 {
			objs = append(objs, pvcObj(fixNS, fmt.Sprintf("dbserver-%d", i),
				"dictycr-balanced", "150Gi", "Bound", map[string]string{"arango_deployment": "arangodb"}))
		}
		return objs
	}

	t.Run("full green deployment", func(t *testing.T) {
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(append(baseObjs(),
			scObj("dictycr-balanced"), scObj("dictycr-ssd"))...)}
		dyn := dynamicClients(arangoCR(fixNS, "Running"))
		results := preflight.Run(datastore.VerifyArangoDB(arangoCfg(clients, dyn)))

		for _, r := range results {
			require.NotEqual(t, preflight.StatusFail, r.Status, "unexpected FAIL: %s %s", r.Name, r.Detail)
		}
		require.False(t, preflight.Failed(results))
	})

	t.Run("8 of 9 member pods fails with the named ratio", func(t *testing.T) {
		objs := baseObjs()
		// member-8 becomes unready: replace the ready one (same name).
		objs[11] = &corev1.Pod{ // member-8: nodes(0-2)+pods(3-11)
			ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "member-8",
				Labels: map[string]string{"arango_deployment": "arangodb"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{Ready: false}}},
		}
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(append(objs,
			scObj("dictycr-balanced"), scObj("dictycr-ssd"))...)}
		dyn := dynamicClients(arangoCR(fixNS, "Running"))
		results := preflight.Run(datastore.VerifyArangoDB(arangoCfg(clients, dyn)))

		members := findDatastoreResult(results, "member pods")
		require.Equal(t, preflight.StatusFail, members.Status)
		require.Equal(t, "8/9 member pods Running and ready", members.Detail)
	})

	t.Run("CR state Failed is named", func(t *testing.T) {
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(append(baseObjs(),
			scObj("dictycr-balanced"), scObj("dictycr-ssd"))...)}
		dyn := dynamicClients(arangoCR(fixNS, "Failed"))
		results := preflight.Run(datastore.VerifyArangoDB(arangoCfg(clients, dyn)))

		cr := findDatastoreResult(results, "ArangoDeployment arangodb")
		require.Equal(t, preflight.StatusFail, cr.Status)
		require.Contains(t, cr.Detail, "state=Failed")
	})

	t.Run("agent PVCs bound at 20Gi on dictycr-ssd", func(t *testing.T) {
		objs := baseObjs() // base already carries the three agent PVCs
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(append(objs,
			scObj("dictycr-balanced"), scObj("dictycr-ssd"))...)}
		dyn := dynamicClients(arangoCR(fixNS, "Running"))
		results := preflight.Run(datastore.VerifyArangoDB(arangoCfg(clients, dyn)))

		agents := findDatastoreResult(results, "agent PVCs")
		require.Equal(t, preflight.StatusPass, agents.Status)
	})

	t.Run("dbserver PVC wrong size fails", func(t *testing.T) {
		objs := baseObjs()
		// drop the correct-size dbservers the base carries
		filtered := objs[:0]
		for _, o := range objs {
			if p, ok := o.(*corev1.PersistentVolumeClaim); ok && len(p.Name) > 8 && p.Name[:8] == "dbserver" {
				continue
			}
			filtered = append(filtered, o)
		}
		objs = filtered
		for i := range 3 {
			objs = append(objs, pvcObj(fixNS, fmt.Sprintf("dbserver-%d", i),
				"dictycr-balanced", "100Gi", "Bound", map[string]string{"arango_deployment": "arangodb"}))
		}
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(append(objs,
			scObj("dictycr-balanced"), scObj("dictycr-ssd"))...)}
		dyn := dynamicClients(arangoCR(fixNS, "Running"))
		results := preflight.Run(datastore.VerifyArangoDB(arangoCfg(clients, dyn)))

		dbs := findDatastoreResult(results, "dbserver PVCs")
		require.Equal(t, preflight.StatusFail, dbs.Status)
	})

	t.Run("setup job not succeeded fails", func(t *testing.T) {
		objs := baseObjs()
		for i, o := range objs {
			if j, ok := o.(*batchv1.Job); ok && j.Name == "setup" {
				objs[i] = &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "setup",
					Labels: map[string]string{"app": "arangodb-create-databases"}},
					Status: batchv1.JobStatus{Failed: 1}}
			}
		}
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(append(objs,
			scObj("dictycr-balanced"), scObj("dictycr-ssd"))...)}
		dyn := dynamicClients(arangoCR(fixNS, "Running"))
		results := preflight.Run(datastore.VerifyArangoDB(arangoCfg(clients, dyn)))

		job := findDatastoreResult(results, "setup Job")
		require.Equal(t, preflight.StatusFail, job.Status)
	})

	t.Run("missing backup cronjob is a warning", func(t *testing.T) {
		objs := baseObjs()
		filtered := objs[:0]
		for _, o := range objs {
			if c, ok := o.(*batchv1.CronJob); ok && c.Name == "arangodb-backup-cronjob" {
				continue
			}
			filtered = append(filtered, o)
		}
		objs = filtered
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(append(objs,
			scObj("dictycr-balanced"), scObj("dictycr-ssd"))...)}
		dyn := dynamicClients(arangoCR(fixNS, "Running"))
		results := preflight.Run(datastore.VerifyArangoDB(arangoCfg(clients, dyn)))

		backup := findDatastoreResult(results, "backup CronJob")
		require.Equal(t, preflight.StatusWarn, backup.Status)
		require.False(t, preflight.Failed(results))
	})
}

func TestVerifyPostgres(t *testing.T) { //nolint:funlen,cyclop,lll,goconst // fixture-heavy
	//nolint:lll,goconst // fixture literals
	pgObjs := func(healthy bool) []runtime.Object {
		objs := []runtime.Object{
			nodeByName("n1", withTaint), nodeByName("n2", withTaint), nodeByName("n3", withTaint),
			readyPod("operators", "operator-0", map[string]string{"app.kubernetes.io/name": "cloudnative-pg"}),
			scObj("dictycr-balanced"), scObj("dictycr-ssd"),
			readyPod(fixNS, "logto-1", map[string]string{"cnpg.io/cluster": "logto"}),
			pvcObj(fixNS, "logto-1", "dictycr-balanced", "1Gi", "Bound", map[string]string{"cnpg.io/cluster": "logto"}),
		}
		for _, suffix := range []string{"rw", "ro", "r"} {
			objs = append(objs, &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "logto-" + suffix},
				Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 5432}}},
			})
		}
		phase := "Cluster in healthy state"
		if !healthy {
			phase = "Cluster in failed state"
		}
		objs = append(objs, cnpgCluster(fixNS, "logto", phase))
		if healthy {
			objs = append(objs, scheduledBackupCR(fixNS, "logto-daily"))
		}
		return objs
	}

	t.Run("healthy cluster passes", func(t *testing.T) {
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(pgObjs(true)...)}
		dyn := dynamicClients(
			cnpgCluster(fixNS, "logto", "Cluster in healthy state"),
			scheduledBackupCR(fixNS, "logto-daily"),
		)
		results := preflight.Run(datastore.VerifyPostgres(pgCfg(clients, dyn)))
		for _, r := range results {
			require.NotEqual(t, preflight.StatusFail, r.Status, "unexpected FAIL: %s %s", r.Name, r.Detail)
		}
		require.False(t, preflight.Failed(results))
	})

	t.Run("unhealthy phase fails with the observed value", func(t *testing.T) {
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(pgObjs(false)...)}
		dyn := dynamicClients(cnpgCluster(fixNS, "logto", "Cluster in failed state"))
		results := preflight.Run(datastore.VerifyPostgres(pgCfg(clients, dyn)))
		phase := findDatastoreResult(results, "Cluster logto phase")
		require.Equal(t, preflight.StatusFail, phase.Status)
		require.Contains(t, phase.Detail, "Cluster in failed state")
	})

	t.Run("zero ScheduledBackup fails", func(t *testing.T) {
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(pgObjs(true)...)}
		dyn := dynamicClients(cnpgCluster(fixNS, "logto", "Cluster in healthy state"))
		results := preflight.Run(datastore.VerifyPostgres(pgCfg(clients, dyn)))
		sb := findDatastoreResult(results, "ScheduledBackup")
		require.Equal(t, preflight.StatusFail, sb.Status)
	})
}

func TestVerifyRedis(t *testing.T) { //nolint:funlen,cyclop,lll,goconst // fixture-heavy
	//nolint:lll,goconst // fixture literals
	redisObjs := func() []runtime.Object {
		return []runtime.Object{
			readyPod(fixNS, "redis-0", map[string]string{"app": "redis"}),
			&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "redis-data"},
				Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "redis"},
				Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 6379}}}},
		}
	}
	t.Run("green except the exec PING (fake cluster)", func(t *testing.T) {
		objs := append(redisObjs(),
			nodeByName("n1", withTaint), nodeByName("n2", withTaint), nodeByName("n3", withTaint))
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(objs...)}
		results := preflight.Run(datastore.VerifyRedis(redisCfg(clients)))
		require.Equal(t, preflight.StatusPass, findDatastoreResult(results, "Redis pod").Status)
		require.Equal(t, preflight.StatusPass, findDatastoreResult(results, "PVC redis-data").Status)
		require.Equal(t, preflight.StatusPass, findDatastoreResult(results, "Service redis").Status)
		// PING needs a real pod exec — must fail, not pass, on a fake clientset.
		require.Equal(t, preflight.StatusFail, findDatastoreResult(results, "PING").Status)
	})
}

func TestVerifyMinio(t *testing.T) { //nolint:funlen,cyclop,lll,goconst // fixture-heavy
	//nolint:lll,goconst // fixture literals
	minioObjs := func() []runtime.Object {
		return []runtime.Object{
			readyPod(fixNS, "minio-0", map[string]string{"app.kubernetes.io/name": "minio"}),
			pvcObj(fixNS, "minio-data-0", "dictycr-balanced", "1Gi", "Bound",
				map[string]string{"app.kubernetes.io/name": "minio"}),
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "minio"},
				Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 9000}}}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "minio-root"}},
		}
	}
	t.Run("all green", func(t *testing.T) {
		objs := append(minioObjs(),
			nodeByName("n1", withTaint), nodeByName("n2", withTaint), nodeByName("n3", withTaint))
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(objs...)}
		results := preflight.Run(datastore.VerifyMinio(minioCfg(clients)))
		require.False(t, preflight.Failed(results), "%v", results)
	})
	t.Run("missing secret fails", func(t *testing.T) {
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(minioObjs()[0:3]...)}
		results := preflight.Run(datastore.VerifyMinio(minioCfg(clients)))
		require.Equal(t, preflight.StatusFail, findDatastoreResult(results, "Secret minio-root").Status)
	})
}

func TestVerifyNats(t *testing.T) { //nolint:funlen,cyclop,lll,goconst // fixture-heavy
	//nolint:lll,goconst // fixture literals
	natsObjs := func() []runtime.Object {
		return []runtime.Object{
			&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "nats"},
				Status: appsv1.StatefulSetStatus{ReadyReplicas: 1}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "nats"},
				Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "nats", Port: 4222}}}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "nats-box"}},
		}
	}
	t.Run("server ready; rtt fails on fake exec", func(t *testing.T) {
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(natsObjs()...)}
		results := preflight.Run(datastore.VerifyNats(natsCfg(clients)))
		require.Equal(t, preflight.StatusPass, findDatastoreResult(results, "StatefulSet nats").Status)
		require.Equal(t, preflight.StatusPass, findDatastoreResult(results, "Service nats").Status)
		rtt := findDatastoreResult(results, "client rtt")
		require.Equal(t, preflight.StatusFail, rtt.Status, "fake clientset cannot exec — must fail, not pass")
	})
}

// ---- fixtures shared by the verify tests ----

func scObj(name string) runtime.Object {
	return &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func pvcObj(ns, name, class, size, phase string, labels map[string]string) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &class,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.PersistentVolumeClaimPhase(phase)},
	}
	return pvc
}

func arangoCfg(clients kube.Clients, dyn kube.Clients) datastore.ArangoConfig { //nolint:goconst,lll,unparam // fixture literals
	clients.Dynamic = dyn.Dynamic
	return datastore.ArangoConfig{
		Clients: clients, Namespace: fixNS, OperatorNamespace: fixNS,
		Members: 9, Pool: "database", WantNodes: 3,
	}
}

func pgCfg(clients kube.Clients, dyn kube.Clients) datastore.PostgresConfig { //nolint:goconst,lll,unparam // fixture literals
	clients.Dynamic = dyn.Dynamic
	return datastore.PostgresConfig{
		Clients: clients, Namespace: fixNS, OperatorNamespace: "operators",
		Cluster: "logto", Instances: 1, Pool: "database", WantNodes: 3,
	}
}

func redisCfg(clients kube.Clients) datastore.RedisConfig {
	return datastore.RedisConfig{Clients: clients, Namespace: fixNS, Name: "redis",
		Pool: testPool, WantNodes: 3}
}

func minioCfg(clients kube.Clients) datastore.MinioConfig {
	return datastore.MinioConfig{Clients: clients, Namespace: fixNS, Secret: "minio-root",
		Pool: testPool, WantNodes: 3}
}

func natsCfg(clients kube.Clients) datastore.NatsConfig {
	return datastore.NatsConfig{Clients: clients, Namespace: fixNS}
}

func findDatastoreResult(results []preflight.Result, needle string) preflight.Result {
	for _, r := range results {
		if strings.Contains(r.Name, needle) {
			return r
		}
	}
	return preflight.Result{}
}

func TestVerifyArangoAppCredentials(t *testing.T) { //nolint:funlen,cyclop,lll,goconst // fixture-heavy
	clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "backend"}},
	)}
	fakeC := clients.Kube.(*k8sfake.Clientset) //nolint:errcheck // type assertion cannot fail
	// Complete the probe pod shortly after creation so the wait returns.
	go func() {
		time.Sleep(300 * time.Millisecond)
		pods, err := fakeC.CoreV1().Pods(fixNS).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return
		}
		for i := range pods.Items {
			if !strings.HasPrefix(pods.Items[i].Name, "arango-app-check-") {
				continue
			}
			pods.Items[i].Status.Phase = corev1.PodSucceeded
			_, _ = fakeC.CoreV1().Pods(fixNS).Update(context.Background(),
				&pods.Items[i], metav1.UpdateOptions{})
		}
	}()

	cfg := datastore.ArangoAppCredentialsConfig{
		Clients: clients, Namespace: fixNS, SecretName: "backend",
		UserKey: "user", PassKey: "password", Image: "arangodb:3.12", Databases: []string{"order"},
	}
	results := preflight.Run(datastore.VerifyArangoAppCredentials(cfg))
	require.False(t, preflight.Failed(results), "%v", results)

	// The overrides structure: credentials come from secretKeyRef env, and
	// argv only carries the $(VAR) indirection — never the plaintext.
	pods, err := fakeC.CoreV1().Pods(fixNS).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, pods.Items, 1)
	container := pods.Items[0].Spec.Containers[0]
	require.Equal(t, "$(APP_USER)", container.Args[3])
	require.Equal(t, "$(APP_PASSWORD)", container.Args[5])
	require.NotNil(t, container.Env[0].ValueFrom.SecretKeyRef)
	require.Equal(t, "backend", container.Env[0].ValueFrom.SecretKeyRef.Name)
	require.Equal(t, "password", container.Env[1].ValueFrom.SecretKeyRef.Key)
	require.NotContains(t, fmt.Sprint(container.Args), "bhola") // TEST CREDENTIAL, not real
}
