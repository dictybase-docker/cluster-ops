package main

import (
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
	cfg.Auth.SecretName = "redis-auth"
	cfg.Auth.Key = "password"
	cfg.Auth.Password = "test-pw"
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

func TestContainerLabHasNoAuthOrAOF(t *testing.T) {
	rds := NewRedisStandalone(&RedisStandaloneConfig{Name: testRedisName})
	container := rds.createRedisContainer()

	assert.Nil(t, container.Args)
	assert.Nil(t, container.Env)
}

func TestContainerProductionAuthAOFAndProbes(t *testing.T) {
	rds := NewRedisStandalone(prodRedisConfig())
	container := rds.createRedisContainer()

	args, ok := container.Args.(pulumi.StringArray)
	require.True(t, ok)
	assert.Contains(t, args, pulumi.String("--appendonly"))
	assert.Contains(t, args, pulumi.String("everysec"))
	assert.Contains(t, args, pulumi.String("--requirepass"))
	assert.Contains(t, args, pulumi.String("$(REDIS_PASSWORD)"))

	env, ok := container.Env.(corev1.EnvVarArray)
	require.True(t, ok)
	require.Len(t, env, 1)
	envVar, ok := env[0].(*corev1.EnvVarArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("REDIS_PASSWORD"), envVar.Name)
	require.NotNil(t, envVar.ValueFrom)
	source, ok := envVar.ValueFrom.(*corev1.EnvVarSourceArgs)
	require.True(t, ok)
	secretRef, ok := source.SecretKeyRef.(*corev1.SecretKeySelectorArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("redis-auth"), secretRef.Name)
	assert.Equal(t, pulumi.String("password"), secretRef.Key)

	require.NotNil(t, container.ReadinessProbe)
	require.NotNil(t, container.LivenessProbe)
}
