package main

import (
	"fmt"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	O "github.com/IBM/fp-go/v2/option"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
)

func defaultStr(fallback string) func(string) string {
	return F.Flow2(
		O.FromPredicate(S.IsNonEmpty),
		O.GetOrElse(F.Constant(fallback)),
	)
}

func defaultInt(fallback int) func(int) int {
	return F.Flow2(
		O.FromPredicate(P.IsNonZero[int]()),
		O.GetOrElse(F.Constant(fallback)),
	)
}

func tolerationToMap(t Toleration) map[string]any {
	return map[string]any{
		"key":      t.Key,
		"operator": t.Operator,
		"value":    t.Value,
		"effect":   t.Effect,
	}
}

// buildClusterSpec generates the Kubernetes CR spec map for kube-arangodb ArangoDeployment.
func buildClusterSpec(cfg *ArangoClusterConfig) map[string]any {
	deployName := F.Pipe1(cfg.Name, defaultStr(defaultName))
	env := F.Pipe1(cfg.Environment, defaultStr(defaultEnvironment))
	arch := F.Pipe1(cfg.Architecture, defaultStr(defaultArchitecture))
	version := F.Pipe1(cfg.Version, defaultStr(defaultVersion))
	caSecretName := F.Pipe1(cfg.TLS.CASecretName, defaultStr(defaultTLSCASecret))

	spec := map[string]any{
		"mode":            defaultMode,
		"environment":     env,
		"image":           fmt.Sprintf("arangodb:%s", version),
		"imagePullPolicy": "IfNotPresent",
		"architecture":    []string{arch},
		"externalAccess": map[string]any{
			"type": "None",
		},
		"tls": map[string]any{
			"caSecretName": caSecretName,
		},
		"bootstrap": map[string]any{
			"passwordSecretNames": map[string]any{
				"root": cfg.Secret.Name,
			},
		},
		"agents":       buildMemberGroup(cfg.Agents, deployName, "agent", true),
		"dbservers":    buildMemberGroup(cfg.DBServers, deployName, "dbserver", true),
		"coordinators": buildMemberGroup(cfg.Coordinators, deployName, "coordinator", false),
	}

	return spec
}

// buildMemberGroup builds the configuration map for a member group with placement and anti-affinity.
func buildMemberGroup(cfg MemberConfig, deployName, role string, withStorage bool) map[string]any {
	gracePeriod := F.Pipe1(
		cfg.TerminationGracePeriodSeconds,
		defaultInt(defaultTerminationGracePeriodSeconds),
	)
	group := map[string]any{
		"count":                         cfg.Count,
		"terminationGracePeriodSeconds": gracePeriod,
	}

	if A.IsNonEmpty(cfg.Args) {
		group["args"] = cfg.Args
	}

	if len(cfg.NodeSelector) > 0 {
		group["nodeSelector"] = cfg.NodeSelector
	}

	if A.IsNonEmpty(cfg.Tolerations) {
		group["tolerations"] = F.Pipe1(
			cfg.Tolerations,
			A.Map(tolerationToMap),
		)
	}

	group["affinity"] = buildAntiAffinity(deployName, role)

	resources := buildResources(cfg)
	if len(resources) > 0 {
		group["resources"] = resources
	}

	if withStorage && S.IsNonEmpty(cfg.StorageSize) {
		group["volumeClaimTemplate"] = buildVolumeClaimTemplate(cfg.StorageClass, cfg.StorageSize)
	}

	return group
}

// buildAntiAffinity constructs PodAntiAffinity rules to ensure failure domain spread.
func buildAntiAffinity(deployName, role string) map[string]any {
	return map[string]any{
		"podAntiAffinity": map[string]any{
			"preferredDuringSchedulingIgnoredDuringExecution": []map[string]any{
				{
					"weight": 100,
					"podAffinityTerm": map[string]any{
						"labelSelector": map[string]any{
							"matchLabels": map[string]any{
								arangoDeploymentKey: deployName,
								"role":              role,
							},
						},
						"topologyKey": "kubernetes.io/hostname",
					},
				},
				{
					"weight": 50,
					"podAffinityTerm": map[string]any{
						"labelSelector": map[string]any{
							"matchLabels": map[string]any{
								arangoDeploymentKey: deployName,
								"role":              role,
							},
						},
						"topologyKey": "topology.kubernetes.io/zone",
					},
				},
			},
		},
	}
}

// buildResources builds the resources map for requests and limits.
func buildResources(cfg MemberConfig) map[string]any {
	resources := make(map[string]any)
	requests := make(map[string]any)
	limits := make(map[string]any)

	if cfg.CPURequest != "" {
		requests["cpu"] = cfg.CPURequest
	}
	if cfg.MemoryRequest != "" {
		requests["memory"] = cfg.MemoryRequest
	}
	if len(requests) > 0 {
		resources["requests"] = requests
	}

	if cfg.CPULimit != "" {
		limits["cpu"] = cfg.CPULimit
	}
	if cfg.MemoryLimit != "" {
		limits["memory"] = cfg.MemoryLimit
	}
	if len(limits) > 0 {
		resources["limits"] = limits
	}

	return resources
}

// buildVolumeClaimTemplate builds the volumeClaimTemplate spec for persistent storage.
func buildVolumeClaimTemplate(storageClass, size string) map[string]any {
	spec := map[string]any{
		"volumeMode":  "Filesystem",
		"accessModes": []string{"ReadWriteOnce"},
		"resources": map[string]any{
			"requests": map[string]any{
				"storage": size,
			},
		},
	}

	if storageClass != "" {
		spec["storageClassName"] = storageClass
	}

	return map[string]any{
		"spec": spec,
	}
}
