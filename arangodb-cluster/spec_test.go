package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSampleClusterConfig() *ArangoClusterConfig {
	toleration := Toleration{
		Key:      tolerationKey,
		Operator: tolerationOpEqual,
		Value:    nodePoolValue,
		Effect:   tolerationEffect,
	}

	return &ArangoClusterConfig{
		Name:         defaultName,
		Namespace:    "prod",
		Version:      defaultVersion,
		Environment:  defaultEnvironment,
		Architecture: defaultArchitecture,
		TLS: TLSConfig{
			CASecretName: defaultTLSCASecret,
		},
		Secret: SecretConfig{
			Name:     "arangodb-pass",
			Password: "testpassword",
		},
		Agents: MemberConfig{
			Count:         3,
			StorageClass:  "dictycr-ssd",
			StorageSize:   "20Gi",
			CPURequest:    "250m",
			MemoryRequest: "1Gi",
			NodeSelector: map[string]string{
				nodePoolKey: nodePoolValue,
			},
			Tolerations: []Toleration{toleration},
		},
		DBServers: MemberConfig{
			Count:         3,
			StorageClass:  "dictycr-balanced",
			StorageSize:   "150Gi",
			CPURequest:    "2",
			MemoryRequest: "12Gi",
			NodeSelector: map[string]string{
				nodePoolKey: nodePoolValue,
			},
			Tolerations: []Toleration{toleration},
		},
		Coordinators: MemberConfig{
			Count:         3,
			CPURequest:    "500m",
			MemoryRequest: "2Gi",
			NodeSelector: map[string]string{
				nodePoolKey: nodePoolValue,
			},
			Tolerations: []Toleration{toleration},
		},
	}
}

func verifyTopLevelSpec(t *testing.T, spec map[string]interface{}) {
	t.Helper()
	assert.Equal(t, defaultMode, spec["mode"])
	assert.Equal(t, defaultEnvironment, spec["environment"])
	assert.Equal(t, "arangodb:3.12.10.1", spec["image"])
	assert.Equal(t, "IfNotPresent", spec["imagePullPolicy"])
	assert.Equal(t, []string{defaultArchitecture}, spec["architecture"])

	tls, ok := spec["tls"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, defaultTLSCASecret, tls["caSecretName"])

	bootstrap, ok := spec["bootstrap"].(map[string]interface{})
	require.True(t, ok)
	passwordSecretNames, ok := bootstrap["passwordSecretNames"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "arangodb-pass", passwordSecretNames["root"])

	externalAccess, ok := spec["externalAccess"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "None", externalAccess["type"])
}

func verifyAgentSpec(t *testing.T, spec map[string]interface{}) {
	t.Helper()
	agents, ok := spec["agents"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 3, agents["count"])
	assert.Equal(t, map[string]string{nodePoolKey: nodePoolValue}, agents["nodeSelector"])
	assert.NotNil(t, agents["affinity"])

	tolerations, ok := agents["tolerations"].([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, tolerations, 1)
	assert.Equal(t, tolerationKey, tolerations[0]["key"])

	resources, ok := agents["resources"].(map[string]interface{})
	require.True(t, ok)
	requests, ok := resources["requests"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "250m", requests["cpu"])
	assert.Equal(t, "1Gi", requests["memory"])

	vct, ok := agents["volumeClaimTemplate"].(map[string]interface{})
	require.True(t, ok)
	vctSpec, ok := vct["spec"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "dictycr-ssd", vctSpec["storageClassName"])
}

func verifyDBServerSpec(t *testing.T, spec map[string]interface{}) {
	t.Helper()
	dbservers, ok := spec["dbservers"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 3, dbservers["count"])
	assert.Equal(t, map[string]string{nodePoolKey: nodePoolValue}, dbservers["nodeSelector"])
	assert.NotNil(t, dbservers["affinity"])

	resources, ok := dbservers["resources"].(map[string]interface{})
	require.True(t, ok)
	requests, ok := resources["requests"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "2", requests["cpu"])
	assert.Equal(t, "12Gi", requests["memory"])

	vct, ok := dbservers["volumeClaimTemplate"].(map[string]interface{})
	require.True(t, ok)
	vctSpec, ok := vct["spec"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "dictycr-balanced", vctSpec["storageClassName"])
}

func verifyCoordinatorSpec(t *testing.T, spec map[string]interface{}) {
	t.Helper()
	coordinators, ok := spec["coordinators"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 3, coordinators["count"])
	assert.Nil(t, coordinators["volumeClaimTemplate"])
	assert.NotNil(t, coordinators["affinity"])

	resources, ok := coordinators["resources"].(map[string]interface{})
	require.True(t, ok)
	requests, ok := resources["requests"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "500m", requests["cpu"])
	assert.Equal(t, "2Gi", requests["memory"])
}

func TestBuildClusterSpec_ProductionDefaults(t *testing.T) {
	cfg := newSampleClusterConfig()
	require.NoError(t, cfg.Validate())

	spec := buildClusterSpec(cfg)

	verifyTopLevelSpec(t, spec)
	verifyAgentSpec(t, spec)
	verifyDBServerSpec(t, spec)
	verifyCoordinatorSpec(t, spec)

	// Validate JSON serialization
	rawJSON, err := json.Marshal(spec)
	require.NoError(t, err)
	assert.NotEmpty(t, rawJSON)
}

func TestClusterConfig_ValidationErrors(t *testing.T) {
	cfg := newSampleClusterConfig()
	cfg.Namespace = ""
	assert.ErrorContains(t, cfg.Validate(), "namespace cannot be empty")

	cfg = newSampleClusterConfig()
	cfg.Secret.Password = ""
	assert.ErrorContains(t, cfg.Validate(), "secret password cannot be empty")

	cfg = newSampleClusterConfig()
	cfg.Agents.Count = 1
	assert.ErrorContains(t, cfg.Validate(), "agents count must be at least 3")

	cfg = newSampleClusterConfig()
	cfg.DBServers.StorageSize = ""
	assert.ErrorContains(t, cfg.Validate(), "dbservers storageSize is required")
}
