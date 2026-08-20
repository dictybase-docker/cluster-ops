package kops

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"

	"github.com/dictybase-docker/cluster-ops/internal/util"
)

const (
	cniCilium   = "cilium"
	topoPublic  = "public"
	topoPrivate = "private"
)

// networkingKeyToFlag maps a kops NetworkingSpec CNI key onto the
// --networking flag value accepted by `kops create cluster`.
var networkingKeyToFlag = map[string]string{
	cniCilium:    cniCilium,
	"calico":     "calico",
	"canal":      "canal",
	"flannel":    "flannel",
	"kubenet":    "kubenet",
	"external":   "external",
	"cni":        "cni",
	"kubeRouter": "kube-router",
	"amazonVPC":  "amazonvpc",
	"kopeio":     "kopeio",
	"weave":      "weave",
}

// controlPlaneRoles are the InstanceGroup roles that count as masters.
var controlPlaneRoles = map[string]bool{
	"Master":       true, // historical v1alpha2 value
	"ControlPlane": true, // current internal/v1alpha3 value
}

const (
	igNodes        = "nodes"
	igStatelessWeb = "stateless-web"
	igStatefulDB   = "stateful-db"
	igBatchSpot    = "batch-spot"
)

// poolNames are the fixed InstanceGroup names produced by Phase 4d templates.
var poolNames = []string{igStatelessWeb, igStatefulDB, igBatchSpot}

// clusterDoc is the subset of a kops Cluster JSON object we need for sync.
type clusterDoc struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		KubernetesVersion string          `json:"kubernetesVersion"`
		Networking        json.RawMessage `json:"networking"`
		API               struct {
			Access []string `json:"access"`
		} `json:"api"`
	} `json:"spec"`
}

// networkingDoc holds the fields of spec.networking we need.
// Subnets live under networking in kops v1.29; CNI keys are detected via
// a second pass as map[string]json.RawMessage.
type networkingDoc struct {
	Subnets []subnetDoc `json:"subnets"`
}

type subnetDoc struct {
	Name string `json:"name"`
	Zone string `json:"zone"`
	Type string `json:"type"`
}

// igDoc is the subset of a kops InstanceGroup JSON object we need for sync.
type igDoc struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Role        string `json:"role"`
		Image       string `json:"image"`
		MinSize     *int32 `json:"minSize"`
		MaxSize     *int32 `json:"maxSize"`
		MachineType string `json:"machineType"`
		RootVolume  struct {
			Size *int32 `json:"size"`
		} `json:"rootVolume"`
		Zones []string `json:"zones"`
	} `json:"spec"`
}

// clusterState holds the parsed live kops objects used by the pure mapper.
type clusterState struct {
	Cluster clusterDoc
	IGs     []igDoc
}

// SyncEnv reads live kops state and rewrites the owned shape variables in
// envFile so the per-cluster env file matches the running cluster.
//
// Returns the applied util.Change list for reporting. Fails closed on
// ambiguous or lossy state (mixed master settings, non-round-trippable
// TOTAL_NODES, multiple API CIDRs, missing required IGs, unknown networking).
func SyncEnv(envFile string) ([]util.Change, error) {
	effect := F.Pipe1(
		fetchClusterState(),
		IOE.Chain(writeSyncUpdates(envFile)),
	)
	return E.UnwrapError(effect())
}

// fetchClusterState shells out to kops and parses the JSON into clusterState.
func fetchClusterState() IOE.IOEither[error, clusterState] {
	return IOE.TryCatchError(func() (clusterState, error) {
		clusterOut, err := runKopsCapture("get", "cluster", "-o", "json")
		if err != nil {
			return clusterState{}, err
		}
		cluster, err := parseClusterJSON([]byte(clusterOut))
		if err != nil {
			return clusterState{}, err
		}

		igOut, err := runKopsCapture("get", "instancegroups", "-o", "json")
		if err != nil {
			return clusterState{}, err
		}
		igs, err := parseInstanceGroupsJSON([]byte(igOut))
		if err != nil {
			return clusterState{}, err
		}

		return clusterState{Cluster: cluster, IGs: igs}, nil
	})
}

// writeSyncUpdates builds the env map from state and rewrites the env file.
func writeSyncUpdates(envFile string) func(clusterState) IOE.IOEither[error, []util.Change] {
	return func(st clusterState) IOE.IOEither[error, []util.Change] {
		return IOE.TryCatchError(func() ([]util.Change, error) {
			updates, err := buildEnvUpdates(st.Cluster, st.IGs)
			if err != nil {
				return nil, err
			}
			return util.UpdateEnvFile(envFile, updates)
		})
	}
}

// parseClusterJSON accepts either a single Cluster object or a JSON array of
// clusters (kops get cluster -o json returns an array when no name is given).
// Exactly one cluster is required.
func parseClusterJSON(raw []byte) (clusterDoc, error) {
	var many []clusterDoc
	if err := json.Unmarshal(raw, &many); err == nil {
		switch len(many) {
		case 0:
			return clusterDoc{}, fmt.Errorf("no cluster found in state store")
		case 1:
			return many[0], nil
		default:
			return clusterDoc{}, fmt.Errorf(
				"multiple clusters found (%d); expected exactly one", len(many),
			)
		}
	}

	var one clusterDoc
	if err := json.Unmarshal(raw, &one); err != nil {
		return clusterDoc{}, fmt.Errorf("parse cluster JSON: %w", err)
	}
	if one.Metadata.Name == "" && one.Spec.KubernetesVersion == "" {
		return clusterDoc{}, fmt.Errorf("parse cluster JSON: empty object")
	}
	return one, nil
}

// parseInstanceGroupsJSON accepts a JSON array of InstanceGroups.
func parseInstanceGroupsJSON(raw []byte) ([]igDoc, error) {
	var igs []igDoc
	if err := json.Unmarshal(raw, &igs); err != nil {
		return nil, fmt.Errorf("parse instancegroups JSON: %w", err)
	}
	return igs, nil
}

// buildEnvUpdates is the pure mapper from live kops objects to env-file
// KEY=value pairs. It is the unit under test for the sync logic.
func buildEnvUpdates(cluster clusterDoc, igs []igDoc) (map[string]string, error) {
	updates := make(map[string]string)

	if err := mapClusterFields(cluster, updates); err != nil {
		return nil, err
	}
	if err := mapInstanceGroups(igs, updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// mapClusterFields fills cluster-spec vars into updates.
func mapClusterFields(cluster clusterDoc, updates map[string]string) error {
	if v := strings.TrimSpace(cluster.Spec.KubernetesVersion); v != "" {
		// kops may store "v1.29.2"; create-cluster accepts either form.
		// Prefer the un-prefixed form used by the repo's env files.
		updates["KUBERNETES_VERSION"] = strings.TrimPrefix(v, "v")
	}

	subnets, netMap, err := parseNetworking(cluster.Spec.Networking)
	if err != nil {
		return err
	}
	topology, err := deriveTopology(subnets)
	if err != nil {
		return err
	}
	updates["TOPOLOGY"] = topology

	networking, err := deriveNetworking(netMap)
	if err != nil {
		return err
	}
	updates["NETWORKING"] = networking

	return mapAPIAccess(cluster.Spec.API.Access, updates)
}

// parseNetworking decodes the networking block once into both the typed
// subnet list and the raw key map used for CNI detection.
func parseNetworking(raw json.RawMessage) ([]subnetDoc, map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var netDoc networkingDoc
	if err := json.Unmarshal(raw, &netDoc); err != nil {
		return nil, nil, fmt.Errorf("parse networking block: %w", err)
	}
	var netMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &netMap); err != nil {
		return nil, nil, fmt.Errorf("parse networking keys: %w", err)
	}
	return netDoc.Subnets, netMap, nil
}

// mapAPIAccess writes API_ACCESS_CIDR when a single non-open CIDR is set.
func mapAPIAccess(access []string, updates map[string]string) error {
	switch {
	case len(access) == 0:
		return nil // open API — leave unset
	case len(access) == 1:
		if access[0] != "0.0.0.0/0" {
			updates["API_ACCESS_CIDR"] = access[0]
		}
		return nil
	default:
		return fmt.Errorf(
			"multiple API access CIDRs %v cannot round-trip to one API_ACCESS_CIDR",
			access,
		)
	}
}

// deriveTopology maps subnet types onto the --topology flag value.
// Private topology creates Private node subnets (+ Utility/Public for masters);
// public topology uses only Public subnets.
func deriveTopology(subnets []subnetDoc) (string, error) {
	if len(subnets) == 0 {
		return "", fmt.Errorf("cluster has no subnets; cannot derive TOPOLOGY")
	}
	for _, s := range subnets {
		if strings.EqualFold(s.Type, "Private") {
			return topoPrivate, nil
		}
	}
	return topoPublic, nil
}

// deriveNetworking finds the single non-nil CNI key under spec.networking.
func deriveNetworking(net map[string]json.RawMessage) (string, error) {
	if net == nil {
		return "", fmt.Errorf("cluster has no networking block; cannot derive NETWORKING")
	}

	var found []string
	for key, raw := range net {
		flag, ok := networkingKeyToFlag[key]
		if !ok {
			continue // skip non-CNI keys (subnets, topology, networkID, gcp, …)
		}
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		found = append(found, flag)
	}
	sort.Strings(found)

	switch len(found) {
	case 0:
		return "", fmt.Errorf("no supported CNI found under spec.networking")
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf(
			"multiple CNIs under spec.networking %v; cannot choose NETWORKING",
			found,
		)
	}
}

// mapInstanceGroups fills IG-derived vars into updates.
func mapInstanceGroups(igs []igDoc, updates map[string]string) error {
	byName, masters := indexIGs(igs)

	if err := mapMasterIGs(masters, updates); err != nil {
		return err
	}
	nodes, ok := byName[igNodes]
	if !ok {
		return fmt.Errorf("required InstanceGroup %q not found", igNodes)
	}
	if err := mapNodesIG(nodes, updates); err != nil {
		return err
	}
	if err := mapOptionalPools(byName, updates); err != nil {
		return err
	}
	return checkPoolZoneConsistency(byName, updates)
}

// indexIGs builds a name→IG map and the control-plane IG list.
func indexIGs(igs []igDoc) (map[string]igDoc, []igDoc) {
	byName := make(map[string]igDoc, len(igs))
	var masters []igDoc
	for _, ig := range igs {
		byName[ig.Metadata.Name] = ig
		if controlPlaneRoles[ig.Spec.Role] {
			masters = append(masters, ig)
		}
	}
	return byName, masters
}

// mapOptionalPools syncs Phase 4d pools when present (dev may skip them).
func mapOptionalPools(byName map[string]igDoc, updates map[string]string) error {
	type pool struct {
		name   string
		prefix string
		zones  bool
	}
	for _, p := range []pool{
		{igStatelessWeb, "STATELESS", true},
		{igStatefulDB, "STATEFUL", false},
		{igBatchSpot, "BATCH", false},
	} {
		ig, ok := byName[p.name]
		if !ok {
			continue
		}
		if err := mapPoolIG(ig, p.prefix, updates); err != nil {
			return err
		}
		if p.zones {
			if err := mapPoolZones(ig, updates); err != nil {
				return err
			}
		}
	}
	return nil
}

// mapMasterIGs fills TOTAL_MASTER / MASTER_* / CONTROL_PLANE_ZONES.
func mapMasterIGs(masters []igDoc, updates map[string]string) error {
	if len(masters) == 0 {
		return fmt.Errorf("no control-plane InstanceGroup found")
	}
	machine, disk, zones, err := consolidateMasters(masters)
	if err != nil {
		return err
	}
	updates["TOTAL_MASTER"] = strconv.Itoa(len(masters))
	if machine != "" {
		updates["MASTER_MACHINE"] = machine
	}
	if disk != nil {
		updates["MASTER_DISK_SIZE"] = strconv.Itoa(int(*disk))
	}
	if len(zones) > 0 {
		updates["CONTROL_PLANE_ZONES"] = strings.Join(zones, ",")
	}
	return nil
}

// consolidateMasters checks master IGs share machine/disk settings and
// returns the shared values plus the sorted zone union.
func consolidateMasters(masters []igDoc) (string, *int32, []string, error) {
	machine := masters[0].Spec.MachineType
	disk := masters[0].Spec.RootVolume.Size
	zoneSet := map[string]struct{}{}
	for _, m := range masters {
		if m.Spec.MachineType != machine {
			return "", nil, nil, fmt.Errorf(
				"control-plane IGs have mixed machineType (%q vs %q); cannot round-trip MASTER_MACHINE",
				machine, m.Spec.MachineType,
			)
		}
		if err := checkDiskMatch(disk, m.Spec.RootVolume.Size); err != nil {
			return "", nil, nil, err
		}
		if disk == nil {
			disk = m.Spec.RootVolume.Size
		}
		for _, z := range m.Spec.Zones {
			zoneSet[z] = struct{}{}
		}
	}
	return machine, disk, sortedKeys(zoneSet), nil
}

// checkDiskMatch fails when two non-nil root-volume sizes disagree.
func checkDiskMatch(base, other *int32) error {
	if base == nil || other == nil {
		return nil
	}
	if *base != *other {
		return fmt.Errorf(
			"control-plane IGs have mixed rootVolume.size (%d vs %d); cannot round-trip MASTER_DISK_SIZE",
			*base, *other,
		)
	}
	return nil
}

// mapNodesIG fills TOTAL_NODES / NODE_* / COMPUTE_IMAGE / NODE_ZONES from the
// default "nodes" InstanceGroup produced by create-cluster-config.
func mapNodesIG(nodes igDoc, updates map[string]string) error {
	if nodes.Spec.MinSize == nil || nodes.Spec.MaxSize == nil {
		return fmt.Errorf("InstanceGroup %q missing minSize/maxSize", igNodes)
	}
	if *nodes.Spec.MinSize != *nodes.Spec.MaxSize {
		return fmt.Errorf(
			"InstanceGroup %q minSize=%d maxSize=%d; cannot round-trip to one TOTAL_NODES",
			igNodes, *nodes.Spec.MinSize, *nodes.Spec.MaxSize,
		)
	}
	updates["TOTAL_NODES"] = strconv.Itoa(int(*nodes.Spec.MaxSize))

	if nodes.Spec.MachineType != "" {
		updates["NODE_MACHINE"] = nodes.Spec.MachineType
	}
	if nodes.Spec.RootVolume.Size != nil {
		updates["NODE_DISK_SIZE"] = strconv.Itoa(int(*nodes.Spec.RootVolume.Size))
	}
	if nodes.Spec.Image != "" {
		updates["COMPUTE_IMAGE"] = nodes.Spec.Image
	}
	if len(nodes.Spec.Zones) > 0 {
		updates["NODE_ZONES"] = strings.Join(nodes.Spec.Zones, ",")
	}
	return nil
}

// mapPoolIG fills PREFIX_MACHINE / PREFIX_MIN / PREFIX_MAX for a Phase 4d pool.
func mapPoolIG(ig igDoc, prefix string, updates map[string]string) error {
	if ig.Spec.MachineType != "" {
		updates[prefix+"_MACHINE"] = ig.Spec.MachineType
	}
	if ig.Spec.MinSize == nil || ig.Spec.MaxSize == nil {
		return fmt.Errorf(
			`InstanceGroup %q missing minSize/maxSize`, ig.Metadata.Name,
		)
	}
	updates[prefix+"_MIN"] = strconv.Itoa(int(*ig.Spec.MinSize))
	updates[prefix+"_MAX"] = strconv.Itoa(int(*ig.Spec.MaxSize))
	return nil
}

// mapPoolZones fills NODE_ZONE_A/B/C from a pool's zones list.
// Templates pin three zones; anything else is ambiguous.
func mapPoolZones(ig igDoc, updates map[string]string) error {
	zones := ig.Spec.Zones
	switch len(zones) {
	case 0:
		return nil // leave unset
	case 1:
		updates["NODE_ZONE_A"] = zones[0]
	case 2:
		updates["NODE_ZONE_A"] = zones[0]
		updates["NODE_ZONE_B"] = zones[1]
	case 3:
		updates["NODE_ZONE_A"] = zones[0]
		updates["NODE_ZONE_B"] = zones[1]
		updates["NODE_ZONE_C"] = zones[2]
	default:
		return fmt.Errorf(
			`InstanceGroup %q has %d zones %v; cannot map to NODE_ZONE_A/B/C`,
			ig.Metadata.Name, len(zones), zones,
		)
	}
	return nil
}

// checkPoolZoneConsistency ensures every present Phase 4d pool shares the same
// zone list that we already wrote into NODE_ZONE_*.
func checkPoolZoneConsistency(byName map[string]igDoc, updates map[string]string) error {
	expected := []string{
		updates["NODE_ZONE_A"],
		updates["NODE_ZONE_B"],
		updates["NODE_ZONE_C"],
	}
	// trim trailing empties so a 1-zone pool doesn't fail against "" slots
	for len(expected) > 0 && expected[len(expected)-1] == "" {
		expected = expected[:len(expected)-1]
	}
	if len(expected) == 0 {
		return nil
	}

	for _, name := range poolNames {
		ig, ok := byName[name]
		if !ok {
			continue
		}
		if !stringSlicesEqual(ig.Spec.Zones, expected) {
			return fmt.Errorf(
				"InstanceGroup %q zones %v disagree with NODE_ZONE_* %v",
				name, ig.Spec.Zones, expected,
			)
		}
	}
	return nil
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
