package kops

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestBuildEnvUpdates_HappyPath(t *testing.T) {
	cluster, err := parseClusterJSON(loadFixture(t, "cluster.json"))
	if err != nil {
		t.Fatalf("parseClusterJSON: %v", err)
	}
	igs, err := parseInstanceGroupsJSON(loadFixture(t, "instancegroups.json"))
	if err != nil {
		t.Fatalf("parseInstanceGroupsJSON: %v", err)
	}

	got, err := buildEnvUpdates(cluster, igs)
	if err != nil {
		t.Fatalf("buildEnvUpdates: %v", err)
	}

	const (
		zoneA  = "us-central1-a"
		zoneB  = "us-central1-b"
		zoneC  = "us-central1-c"
		image  = "ubuntu-os-cloud/ubuntu-2204-jammy-v20240829"
		e2std4 = "e2-standard-4"
	)
	want := map[string]string{
		"KUBERNETES_VERSION":  "1.28.8",
		"TOPOLOGY":            topoPublic,
		"NETWORKING":          cniCilium,
		"API_ACCESS_CIDR":     "203.0.113.10/32",
		"TOTAL_MASTER":        "1",
		"MASTER_MACHINE":      "n1-custom-4-8192",
		"MASTER_DISK_SIZE":    "75",
		"CONTROL_PLANE_ZONES": zoneC,
		"TOTAL_NODES":         "4",
		"NODE_MACHINE":        "n1-custom-2-4096",
		"NODE_DISK_SIZE":      "100",
		"COMPUTE_IMAGE":       image,
		"NODE_ZONES":          zoneC,
		"STATELESS_MACHINE":   e2std4,
		"STATELESS_MIN":       "3",
		"STATELESS_MAX":       "6",
		"STATEFUL_MACHINE":    "n2-standard-4",
		"STATEFUL_MIN":        "3",
		"STATEFUL_MAX":        "3",
		"BATCH_MACHINE":       e2std4,
		"BATCH_MIN":           "0",
		"BATCH_MAX":           "4",
		"NODE_ZONE_A":         zoneA,
		"NODE_ZONE_B":         zoneB,
		"NODE_ZONE_C":         zoneC,
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d; full map: %#v", len(got), len(want), got)
	}
}

func TestBuildEnvUpdates_PrivateMultiMaster(t *testing.T) {
	cluster, err := parseClusterJSON(loadFixture(t, "cluster-private.json"))
	if err != nil {
		t.Fatalf("parseClusterJSON: %v", err)
	}
	igs, err := parseInstanceGroupsJSON(loadFixture(t, "instancegroups-multimaster.json"))
	if err != nil {
		t.Fatalf("parseInstanceGroupsJSON: %v", err)
	}

	got, err := buildEnvUpdates(cluster, igs)
	if err != nil {
		t.Fatalf("buildEnvUpdates: %v", err)
	}

	if got["TOPOLOGY"] != "private" {
		t.Errorf("TOPOLOGY: got %q, want private", got["TOPOLOGY"])
	}
	if got["TOTAL_MASTER"] != "3" {
		t.Errorf("TOTAL_MASTER: got %q, want 3", got["TOTAL_MASTER"])
	}
	if got["CONTROL_PLANE_ZONES"] != "us-central1-a,us-central1-b,us-central1-c" {
		t.Errorf("CONTROL_PLANE_ZONES: got %q", got["CONTROL_PLANE_ZONES"])
	}
	if got["TOTAL_NODES"] != "6" {
		t.Errorf("TOTAL_NODES: got %q, want 6", got["TOTAL_NODES"])
	}
	// open API (0.0.0.0/0) must leave API_ACCESS_CIDR unset
	if _, ok := got["API_ACCESS_CIDR"]; ok {
		t.Errorf("API_ACCESS_CIDR should be unset for open API, got %q", got["API_ACCESS_CIDR"])
	}
	// no Phase 4d pools in this fixture
	if _, ok := got["STATELESS_MACHINE"]; ok {
		t.Errorf("STATELESS_MACHINE should be absent without pools")
	}
}

func TestBuildEnvUpdates_NodesMinMaxMismatch(t *testing.T) {
	cluster, err := parseClusterJSON(loadFixture(t, "cluster.json"))
	if err != nil {
		t.Fatalf("parseClusterJSON: %v", err)
	}
	igs, err := parseInstanceGroupsJSON(loadFixture(t, "instancegroups.json"))
	if err != nil {
		t.Fatalf("parseInstanceGroupsJSON: %v", err)
	}
	// force min != max on the nodes IG
	for i := range igs {
		if igs[i].Metadata.Name == igNodes {
			min := int32(2)
			max := int32(8)
			igs[i].Spec.MinSize = &min
			igs[i].Spec.MaxSize = &max
		}
	}

	_, err = buildEnvUpdates(cluster, igs)
	if err == nil {
		t.Fatal("expected error for nodes minSize != maxSize, got nil")
	}
}

func TestBuildEnvUpdates_MixedMasterMachine(t *testing.T) {
	cluster, err := parseClusterJSON(loadFixture(t, "cluster-private.json"))
	if err != nil {
		t.Fatalf("parseClusterJSON: %v", err)
	}
	igs, err := parseInstanceGroupsJSON(loadFixture(t, "instancegroups-multimaster.json"))
	if err != nil {
		t.Fatalf("parseInstanceGroupsJSON: %v", err)
	}
	// force mixed machine types across masters
	for i := range igs {
		if igs[i].Metadata.Name == "master-us-central1-b" {
			igs[i].Spec.MachineType = "n1-custom-8-16384"
		}
	}

	_, err = buildEnvUpdates(cluster, igs)
	if err == nil {
		t.Fatal("expected error for mixed master machineType, got nil")
	}
}

func TestBuildEnvUpdates_MissingNodesIG(t *testing.T) {
	cluster, err := parseClusterJSON(loadFixture(t, "cluster.json"))
	if err != nil {
		t.Fatalf("parseClusterJSON: %v", err)
	}
	igs, err := parseInstanceGroupsJSON(loadFixture(t, "instancegroups.json"))
	if err != nil {
		t.Fatalf("parseInstanceGroupsJSON: %v", err)
	}
	// drop the nodes IG
	var filtered []igDoc
	for _, ig := range igs {
		if ig.Metadata.Name != igNodes {
			filtered = append(filtered, ig)
		}
	}

	_, err = buildEnvUpdates(cluster, filtered)
	if err == nil {
		t.Fatal("expected error for missing nodes IG, got nil")
	}
}

func TestBuildEnvUpdates_MultipleAPIAccessCIDRs(t *testing.T) {
	cluster, err := parseClusterJSON(loadFixture(t, "cluster.json"))
	if err != nil {
		t.Fatalf("parseClusterJSON: %v", err)
	}
	cluster.Spec.API.Access = []string{"203.0.113.10/32", "198.51.100.0/24"}
	igs, err := parseInstanceGroupsJSON(loadFixture(t, "instancegroups.json"))
	if err != nil {
		t.Fatalf("parseInstanceGroupsJSON: %v", err)
	}

	_, err = buildEnvUpdates(cluster, igs)
	if err == nil {
		t.Fatal("expected error for multiple API CIDRs, got nil")
	}
}

func TestBuildEnvUpdates_PoolZoneMismatch(t *testing.T) {
	cluster, err := parseClusterJSON(loadFixture(t, "cluster.json"))
	if err != nil {
		t.Fatalf("parseClusterJSON: %v", err)
	}
	igs, err := parseInstanceGroupsJSON(loadFixture(t, "instancegroups.json"))
	if err != nil {
		t.Fatalf("parseInstanceGroupsJSON: %v", err)
	}
	// make stateful-db disagree with NODE_ZONE_* from stateless-web
	for i := range igs {
		if igs[i].Metadata.Name == igStatefulDB {
			igs[i].Spec.Zones = []string{"us-east1-a", "us-east1-b", "us-east1-c"}
		}
	}

	_, err = buildEnvUpdates(cluster, igs)
	if err == nil {
		t.Fatal("expected error for pool zone mismatch, got nil")
	}
}

func TestParseClusterJSON_SingleObject(t *testing.T) {
	raw := []byte(`{
		"apiVersion": "kops.k8s.io/v1alpha2",
		"kind": "Cluster",
		"metadata": {"name": "solo.k8s.local"},
		"spec": {
			"kubernetesVersion": "1.28.8",
			"networking": {
				"cilium": {},
				"subnets": [{"name": "z", "zone": "us-central1-c", "type": "Public"}]
			}
		}
	}`)
	c, err := parseClusterJSON(raw)
	if err != nil {
		t.Fatalf("parseClusterJSON single: %v", err)
	}
	if c.Metadata.Name != "solo.k8s.local" {
		t.Errorf("name: got %q", c.Metadata.Name)
	}
}

func TestParseClusterJSON_MultipleFails(t *testing.T) {
	raw := []byte(`[{
		"metadata": {"name": "a"},
		"spec": {
			"kubernetesVersion": "1.28.8",
			"networking": {"cilium": {}, "subnets": [{"type": "Public"}]}
		}
	}, {
		"metadata": {"name": "b"},
		"spec": {
			"kubernetesVersion": "1.28.8",
			"networking": {"cilium": {}, "subnets": [{"type": "Public"}]}
		}
	}]`)
	_, err := parseClusterJSON(raw)
	if err == nil {
		t.Fatal("expected error for multiple clusters, got nil")
	}
}
