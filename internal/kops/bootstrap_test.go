package kops

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type igExpected struct {
	machineType    string
	minSize        int
	maxSize        int
	rootVolumeSize int
	image          string
}

const (
	igMasterKey  = "control-plane-us-central1-c"
	igNodesKey   = "nodes-us-central1-c"
	masterType   = "n1-custom-4-8192"
	nodeType     = "n1-custom-2-4096"
	statelessWeb = "stateless-web"
	statefulDB   = "stateful-db"
	batchSpot    = "batch-spot"
	e2Std4       = "e2-standard-4"
	n2Std4       = "n2-standard-4"
	defaultImage = "ubuntu-os-cloud/ubuntu-2204-jammy-v20240829"
)

func TestStarterTemplates_ValidSchemaAndDefaults(t *testing.T) {
	clusterTmpl, err := os.ReadFile("../../config/kops/_starter/cluster.yaml.tmpl")
	if err != nil {
		t.Fatalf("read cluster.yaml.tmpl: %v", err)
	}

	igsTmpl, err := os.ReadFile("../../config/kops/_starter/instancegroups.yaml.tmpl")
	if err != nil {
		t.Fatalf("read instancegroups.yaml.tmpl: %v", err)
	}

	replacer := strings.NewReplacer(
		"${KOPS_CLUSTER_NAME}", "unit-test.k8s.local",
		"${PROJECT_ID}", "test-project",
		"${KOPS_STATE_STORE}", "gs://test-state",
		"${API_ACCESS_CIDR}", "203.0.113.1/32",
	)

	renderedCluster := replacer.Replace(string(clusterTmpl))
	renderedIGs := replacer.Replace(string(igsTmpl))

	assertClusterDefaults(t, renderedCluster)
	assertInstanceGroupDefaults(t, renderedIGs)
}

func assertClusterDefaults(t *testing.T, rendered string) {
	t.Helper()
	var clusterMap map[string]any
	clusterDec := yaml.NewDecoder(strings.NewReader(rendered))
	clusterDec.KnownFields(true)
	if err := clusterDec.Decode(&clusterMap); err != nil {
		t.Fatalf("decode cluster.yaml failed: %v", err)
	}

	spec, ok := clusterMap["spec"].(map[string]any)
	if !ok {
		t.Fatalf("cluster spec missing or invalid type")
	}

	if spec["kubernetesVersion"] != "1.28.8" || spec["cloudProvider"] != "gce" {
		t.Errorf("cluster core spec mismatch: %v", spec)
	}

	assertAddons(t, spec)
	assertAccessAndDNS(t, spec)
	assertEtcdStorage(t, spec)
	assertCiliumNetworking(t, spec)
}

func assertAddons(t *testing.T, spec map[string]any) {
	t.Helper()
	checkAddon(t, spec, "clusterAutoscaler")
	checkAddon(t, spec, "certManager")
	checkAddon(t, spec, "nodeProblemDetector")
	checkAddon(t, spec, "metricsServer")
}

func assertAccessAndDNS(t *testing.T, spec map[string]any) {
	t.Helper()
	kubeDNS, ok := spec["kubeDNS"].(map[string]any)
	if !ok {
		t.Fatalf("kubeDNS missing or invalid type")
	}
	nodeLocalDNS, ok := kubeDNS["nodeLocalDNS"].(map[string]any)
	if !ok || nodeLocalDNS["enabled"] != true {
		t.Errorf("expected kubeDNS.nodeLocalDNS.enabled true, got %v", nodeLocalDNS)
	}

	kubeProxy, ok := spec["kubeProxy"].(map[string]any)
	if !ok || kubeProxy["enabled"] != false {
		t.Errorf("expected kubeProxy.enabled false, got %v", kubeProxy)
	}

	apiAccess, ok := spec["kubernetesApiAccess"].([]any)
	if !ok || len(apiAccess) != 1 || apiAccess[0] != "203.0.113.1/32" {
		t.Errorf("expected kubernetesApiAccess [203.0.113.1/32], got %v", apiAccess)
	}
}

func checkAddon(t *testing.T, spec map[string]any, key string) {
	t.Helper()
	addon, ok := spec[key].(map[string]any)
	if !ok || addon["enabled"] != true {
		t.Errorf("expected %s.enabled true, got %v", key, addon)
	}
}

func assertEtcdStorage(t *testing.T, spec map[string]any) {
	t.Helper()
	etcdClusters, ok := spec["etcdClusters"].([]any)
	if !ok || len(etcdClusters) != 3 {
		t.Fatalf("expected 3 etcdClusters, got %v", etcdClusters)
	}

	for _, raw := range etcdClusters {
		c, cOk := raw.(map[string]any)
		if !cOk {
			continue
		}
		name, _ := c["name"].(string)
		if name == "main" || name == "events" {
			members, mOk := c["etcdMembers"].([]any)
			if !mOk || len(members) == 0 {
				t.Errorf("etcd %s missing members", name)
				continue
			}
			m0, _ := members[0].(map[string]any)
			if m0["volumeType"] != "pd-ssd" {
				t.Errorf("etcd %s expected volumeType pd-ssd, got %v", name, m0["volumeType"])
			}
		}
	}
}

func assertCiliumNetworking(t *testing.T, spec map[string]any) {
	t.Helper()
	net, ok := spec["networking"].(map[string]any)
	if !ok {
		t.Fatalf("networking block missing")
	}
	cilium, ok := net["cilium"].(map[string]any)
	if !ok || cilium["enableNodePort"] != true || cilium["etcdManaged"] != true {
		t.Errorf("cilium networking config mismatch: %v", cilium)
	}
}

func assertInstanceGroupDefaults(t *testing.T, rendered string) {
	t.Helper()
	igNames := decodeIGs(t, rendered)

	expectedMap := map[string]igExpected{
		igMasterKey:  {masterType, 1, 1, 75, defaultImage},
		igNodesKey:   {nodeType, 4, 4, 100, defaultImage},
		statelessWeb: {e2Std4, 3, 6, 100, defaultImage},
		statefulDB:   {n2Std4, 3, 3, 100, defaultImage},
		batchSpot:    {e2Std4, 0, 4, 100, defaultImage},
	}

	if len(igNames) != len(expectedMap) {
		t.Fatalf("expected %d instancegroups, got %d", len(expectedMap), len(igNames))
	}

	for name, exp := range expectedMap {
		igSpec, ok := igNames[name]
		if !ok {
			t.Errorf("expected instancegroup %q not found", name)
			continue
		}
		if igSpec["machineType"] != exp.machineType ||
			igSpec["minSize"] != exp.minSize ||
			igSpec["maxSize"] != exp.maxSize ||
			igSpec["rootVolumeSize"] != exp.rootVolumeSize ||
			igSpec["image"] != exp.image {
			t.Errorf("%s defaults mismatch: expected %+v, got %+v", name, exp, igSpec)
		}
	}

	assertBatchSpotTaint(t, igNames[batchSpot])
}

func assertBatchSpotTaint(t *testing.T, spec map[string]any) {
	t.Helper()
	taints, ok := spec["taints"].([]any)
	if !ok || len(taints) != 1 || taints[0] != "spot=true:NoSchedule" {
		t.Errorf("expected batch-spot taint [spot=true:NoSchedule], got %v", taints)
	}
}

func decodeIGs(t *testing.T, rendered string) map[string]map[string]any {
	t.Helper()
	igsDec := yaml.NewDecoder(strings.NewReader(rendered))
	igsDec.KnownFields(true)

	igNames := make(map[string]map[string]any)
	for {
		var igMap map[string]any
		err := igsDec.Decode(&igMap)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode instancegroup doc failed: %v", err)
		}
		meta, metaOk := igMap["metadata"].(map[string]any)
		if !metaOk {
			t.Fatalf("instancegroup metadata missing")
		}
		name, nameOk := meta["name"].(string)
		if !nameOk {
			t.Fatalf("instancegroup metadata.name missing")
		}
		spec, specOk := igMap["spec"].(map[string]any)
		if !specOk {
			t.Fatalf("instancegroup %s spec missing", name)
		}
		igNames[name] = spec
	}
	return igNames
}

func TestStarterTemplates_NoUnresolvedPlaceholders(t *testing.T) {
	for _, path := range []string{
		"../../config/kops/_starter/cluster.yaml.tmpl",
		"../../config/kops/_starter/instancegroups.yaml.tmpl",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		known := map[string]bool{
			"${KOPS_CLUSTER_NAME}": true,
			"${PROJECT_ID}":        true,
			"${KOPS_STATE_STORE}":  true,
			"${API_ACCESS_CIDR}":   true,
		}
		lines := bytes.Split(raw, []byte("\n"))
		for i, line := range lines {
			s := string(line)
			if strings.Contains(s, "${") {
				start := strings.Index(s, "${")
				end := strings.Index(s[start:], "}")
				if end == -1 {
					t.Errorf("%s:%d malformed variable placeholder: %s", path, i+1, s)
					continue
				}
				varName := s[start : start+end+1]
				if !known[varName] {
					t.Errorf("%s:%d unknown template placeholder: %s", path, i+1, varName)
				}
			}
		}
	}
}
