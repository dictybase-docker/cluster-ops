package backend

import (
	"testing"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func labels() pulumi.StringMap {
	return pulumi.StringMap{"app": pulumi.String("order-api-server")}
}

// firstContainer extracts the single container from a rendered deployment spec.
func firstContainer(t *testing.T, bck *Backend) *corev1.ContainerArgs {
	t.Helper()
	spec := bck.createDeploymentSpec(labels())
	tmpl, ok := spec.Template.(*corev1.PodTemplateSpecArgs)
	if !ok {
		t.Fatalf("spec.Template is %T, want *PodTemplateSpecArgs", spec.Template)
	}
	pod, ok := tmpl.Spec.(*corev1.PodSpecArgs)
	if !ok {
		t.Fatalf("template spec is %T", tmpl.Spec)
	}
	arr, ok := pod.Containers.(corev1.ContainerArray)
	if !ok {
		t.Fatalf("containers is %T", pod.Containers)
	}
	if len(arr) != 1 {
		t.Fatalf("containers = %d, want 1", len(arr))
	}
	c, ok := arr[0].(*corev1.ContainerArgs)
	if !ok {
		t.Fatalf("container is %T", arr[0])
	}
	return c
}

func prodConfig() *BackendConfig {
	return &BackendConfig{
		AppName:  "order",
		Port:     9250,
		Replicas: new(2),
		Resources: &ResourceSpec{
			Requests: ResourceValues{CPU: "100m", Memory: "128Mi"},
			Limits:   ResourceValues{CPU: "500m", Memory: "512Mi"},
		},
		GRPCHealthProbe: new(true),
	}
}

// Lab stacks (nil production fields) must render the historical shape:
// 1 replica, no resource block, no probes.
func TestDefaultsPreserveLabBehavior(t *testing.T) {
	bck := NewBackend(&BackendConfig{AppName: "order"})

	if got := bck.replicasValue(); got != 1 {
		t.Fatalf("default replicas = %d, want 1", got)
	}
	if bck.containerResources() != nil {
		t.Fatal("default resources must be nil")
	}
	if r, l := bck.grpcProbes(); r != nil || l != nil {
		t.Fatal("default probes must be nil")
	}

	c := firstContainer(t, bck)
	if c.Resources != nil {
		t.Fatal("default container resources must be nil")
	}
	if c.ReadinessProbe != nil || c.LivenessProbe != nil {
		t.Fatal("default container probes must be nil")
	}
}

func TestProdFieldsRender(t *testing.T) {
	bck := NewBackend(prodConfig())

	if got := bck.replicasValue(); got != 2 {
		t.Fatalf("prod replicas = %d, want 2", got)
	}
	checkResources(t, bck)
	checkProbes(t, bck)
	checkContainerWiring(t, bck)
}

func checkResources(t *testing.T, bck *Backend) {
	t.Helper()
	res := bck.containerResources()
	if res == nil {
		t.Fatal("prod resources must be set")
	}
	req, okReq := res.Requests.(pulumi.StringMap)
	lim, okLim := res.Limits.(pulumi.StringMap)
	if !okReq || !okLim {
		t.Fatalf("resources maps are %T/%T", res.Requests, res.Limits)
	}
	if req["cpu"] != pulumi.String("100m") || lim["memory"] != pulumi.String("512Mi") {
		t.Fatalf("resources mismatch: req=%v lim=%v", req, lim)
	}
}

func checkProbes(t *testing.T, bck *Backend) {
	t.Helper()
	r, l := bck.grpcProbes()
	if r == nil || l == nil {
		t.Fatal("prod probes must be set")
	}
	if r.Grpc == nil {
		t.Fatal("readiness probe has no gRPC action")
	}
	action, ok := r.Grpc.(*corev1.GRPCActionArgs)
	if !ok {
		t.Fatalf("gRPC action is %T", r.Grpc)
	}
	port, ok := action.Port.(pulumi.Int)
	if !ok || port != pulumi.Int(9250) {
		t.Fatalf("readiness gRPC port = %v, want 9250", action.Port)
	}
}

func checkContainerWiring(t *testing.T, bck *Backend) {
	t.Helper()
	c := firstContainer(t, bck)
	if c.Resources == nil {
		t.Fatal("container resources must be set")
	}
	if c.ReadinessProbe == nil || c.LivenessProbe == nil {
		t.Fatal("container probes must be set")
	}
}

// Fields are independent: probe off keeps resources and replicas.
func TestProbeOptOut(t *testing.T) {
	cfg := prodConfig()
	cfg.GRPCHealthProbe = new(false)
	bck := NewBackend(cfg)

	if r, l := bck.grpcProbes(); r != nil || l != nil {
		t.Fatal("explicit false must disable probes")
	}
	c := firstContainer(t, bck)
	if c.ReadinessProbe != nil {
		t.Fatal("explicit false must leave readiness probe unset")
	}
	if c.Resources == nil {
		t.Fatal("resources stay set when probe is off")
	}
	if got := bck.replicasValue(); got != 2 {
		t.Fatalf("replicas stay set when probe is off, got %d", got)
	}
}
