package main

import (
	"fmt"
)

// buildClusterSpec generates the Kubernetes CR spec map for kube-arangodb ArangoDeployment.
func buildClusterSpec(cfg *ArangoClusterConfig) map[string]interface{} {
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

	spec := map[string]interface{}{
		"mode":            defaultMode,
		"environment":     env,
		"image":           fmt.Sprintf("arangodb:%s", version),
		"imagePullPolicy": "IfNotPresent",
		"architecture":    []string{arch},
		"externalAccess": map[string]interface{}{
			"type": "None",
		},
		"tls": map[string]interface{}{
			"caSecretName": caSecretName,
		},
		"bootstrap": map[string]interface{}{
			"passwordSecretNames": map[string]interface{}{
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
func buildMemberGroup(cfg MemberConfig, deployName, role string, withStorage bool) map[string]interface{} {
	group := map[string]interface{}{
		"count": cfg.Count,
	}

	if len(cfg.NodeSelector) > 0 {
		group["nodeSelector"] = cfg.NodeSelector
	}

	if len(cfg.Tolerations) > 0 {
		tolerations := make([]map[string]interface{}, 0, len(cfg.Tolerations))
		for _, t := range cfg.Tolerations {
			tolerations = append(tolerations, map[string]interface{}{
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
func buildAntiAffinity(deployName, role string) map[string]interface{} {
	return map[string]interface{}{
		"podAntiAffinity": map[string]interface{}{
			"preferredDuringSchedulingIgnoredDuringExecution": []map[string]interface{}{
				{
					"weight": 100,
					"podAffinityTerm": map[string]interface{}{
						"labelSelector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								arangoDeploymentKey: deployName,
								"role":              role,
							},
						},
						"topologyKey": "kubernetes.io/hostname",
					},
				},
				{
					"weight": 50,
					"podAffinityTerm": map[string]interface{}{
						"labelSelector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
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
func buildResources(cfg MemberConfig) map[string]interface{} {
	resources := make(map[string]interface{})
	requests := make(map[string]interface{})
	limits := make(map[string]interface{})

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
func buildVolumeClaimTemplate(storageClass, size string) map[string]interface{} {
	spec := map[string]interface{}{
		"volumeMode":  "Filesystem",
		"accessModes": []string{"ReadWriteOnce"},
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"storage": size,
			},
		},
	}

	if storageClass != "" {
		spec["storageClassName"] = storageClass
	}

	return map[string]interface{}{
		"spec": spec,
	}
}
