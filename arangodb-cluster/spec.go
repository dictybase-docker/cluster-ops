package main

import (
	"fmt"
)

// buildClusterSpec generates the Kubernetes CR spec map for kube-arangodb ArangoDeployment.
func buildClusterSpec(cfg *ArangoClusterConfig) map[string]any {
	deployName := cfg.Name
	if deployName == "" {
		deployName = defaultName
	}

	env := cfg.Environment
	if env == "" {
		env = defaultEnvironment
	}

	arch := cfg.Architecture
	if arch == "" {
		arch = defaultArchitecture
	}

	version := cfg.Version
	if version == "" {
		version = defaultVersion
	}

	caSecretName := cfg.TLS.CASecretName
	if caSecretName == "" {
		caSecretName = defaultTLSCASecret
	}

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
	group := map[string]any{
		"count": cfg.Count,
	}

	if len(cfg.NodeSelector) > 0 {
		group["nodeSelector"] = cfg.NodeSelector
	}

	if len(cfg.Tolerations) > 0 {
		tolerations := make([]map[string]any, 0, len(cfg.Tolerations))
		for _, t := range cfg.Tolerations {
			tolerations = append(tolerations, map[string]any{
				"key":      t.Key,
				"operator": t.Operator,
				"value":    t.Value,
				"effect":   t.Effect,
			})
		}
		group["tolerations"] = tolerations
	}

	group["affinity"] = buildAntiAffinity(deployName, role)

	resources := buildResources(cfg)
	if len(resources) > 0 {
		group["resources"] = resources
	}

	if withStorage && cfg.StorageSize != "" {
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
