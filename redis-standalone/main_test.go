package main

import (
	"fmt"
	"testing"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRedisName = "redis"

func prodRedisConfig() *RedisStandaloneConfig {
	cfg := &RedisStandaloneConfig{
		Name:      testRedisName,
		Namespace: "prod",
		AOF:       true,
	}
	cfg.Image.Name = "redis"
	cfg.Image.Tag = "8.4.6"
	cfg.Storage.Class = "dictycr-balanced"
	cfg.Storage.Size = "50Gi"
	cfg.Placement.Pool = "database"
	return cfg
}

func TestPodSpecLabDefaultsUnchanged(t *testing.T) {
	rds := NewRedisStandalone(&RedisStandaloneConfig{Name: testRedisName})
	spec := rds.createPodSpec(nil)

	assert.Nil(t, spec.NodeSelector)
	assert.Nil(t, spec.Tolerations)
}

func TestPodSpecProductionPlacement(t *testing.T) {
	rds := NewRedisStandalone(prodRedisConfig())
	spec := rds.createPodSpec(nil)

	selector, ok := spec.NodeSelector.(pulumi.StringMap)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("database"), selector["pool"])

	tolerations, ok := spec.Tolerations.(corev1.TolerationArray)
	require.True(t, ok)
	require.Len(t, tolerations, 1)
	toleration, ok := tolerations[0].(*corev1.TolerationArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("dedicated"), toleration.Key)
	assert.Equal(t, pulumi.String("Equal"), toleration.Operator)
	assert.Equal(t, pulumi.String("database"), toleration.Value)
	assert.Equal(t, pulumi.String("NoSchedule"), toleration.Effect)
}

func TestContainerLabHasNoAOF(t *testing.T) {
	rds := NewRedisStandalone(&RedisStandaloneConfig{Name: testRedisName})
	container := rds.createRedisContainer()

	assert.Nil(t, container.Args)
	assert.Nil(t, container.Env)
}

func TestContainerProductionAOFNoAuthAndProbes(t *testing.T) {
	rds := NewRedisStandalone(prodRedisConfig())
	container := rds.createRedisContainer()

	args, ok := container.Args.(pulumi.StringArray)
	require.True(t, ok)
	assert.Contains(t, args, pulumi.String("--appendonly"))
	assert.Contains(t, args, pulumi.String("everysec"))
	// Unauthenticated: no --requirepass, no env injection.
	assert.NotContains(t, args, pulumi.String("--requirepass"))
	assert.Nil(t, container.Env)

	require.NotNil(t, container.ReadinessProbe)
	require.NotNil(t, container.LivenessProbe)
}

func TestDataPVCNameMatchesVolumeClaimName(t *testing.T) {
	// Regression: the PVC used to be created with the bare config name
	// (redis) while the pod volume referenced <name>-data, leaving the
	// pod Pending forever. Both must derive from one helper.
	rds := NewRedisStandalone(prodRedisConfig())
	assert.Equal(t, "redis-data", rds.dataPVCName())
	assert.Equal(
		t,
		rds.dataPVCName(),
		fmt.Sprintf("%s-data", rds.Config.Name),
	)
}

func TestValidateFailsFastOnMissingFields(t *testing.T) {
	// Regression: a missing properties.name used to surface as an API
	// server rejection of a PVC named "-data" instead of a config error.
	require.NoError(t, prodRedisConfig().validate())

	for _, field := range prodRedisConfig().requiredFields() {
		blank := prodRedisConfig()
		switch field.path {
		case "properties.name":
			blank.Name = ""
		case "properties.image.name":
			blank.Image.Name = ""
		case "properties.image.tag":
			blank.Image.Tag = ""
		case "properties.namespace":
			blank.Namespace = ""
		case "properties.storage.class":
			blank.Storage.Class = ""
		case "properties.storage.size":
			blank.Storage.Size = ""
		case "properties.placement.pool":
			blank.Placement.Pool = ""
		}
		err := blank.validate()
		require.Error(t, err, "expected error for missing %s", field.path)
		assert.Contains(t, err.Error(), field.path)
	}
}
