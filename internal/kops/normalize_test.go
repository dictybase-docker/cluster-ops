package kops

import (
	"bytes"
	"testing"
)

func TestNormalizeYAML_StripsMetadataAndSorts(t *testing.T) {
	input := []byte(`
apiVersion: kops.k8s.io/v1alpha2
kind: InstanceGroup
metadata:
  creationTimestamp: "2026-09-09T15:50:27Z"
  generation: 2
  labels:
    kops.k8s.io/cluster: test.k8s.local
  name: z-worker
spec:
  minSize: 1
---
apiVersion: kops.k8s.io/v1alpha2
kind: InstanceGroup
metadata:
  creationTimestamp: "2026-09-09T15:50:22Z"
  labels:
    kops.k8s.io/cluster: test.k8s.local
  name: a-master
spec:
  minSize: 1
`)

	normalized, err := NormalizeYAML(input)
	if err != nil {
		t.Fatalf("NormalizeYAML failed: %v", err)
	}

	if bytes.Contains(normalized, []byte("creationTimestamp")) {
		t.Errorf("normalized output contains creationTimestamp")
	}
	if bytes.Contains(normalized, []byte("generation")) {
		t.Errorf("normalized output contains generation")
	}

	idxA := bytes.Index(normalized, []byte("name: a-master"))
	idxZ := bytes.Index(normalized, []byte("name: z-worker"))
	if idxA == -1 || idxZ == -1 || idxA > idxZ {
		t.Errorf("expected a-master before z-worker, got idxA=%d idxZ=%d", idxA, idxZ)
	}
}
