package main

import (
	"testing"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prodNatsConfig() *NatsConfig {
	cfg := &NatsConfig{
		Namespace: "prod",
	}
	cfg.Chart.Name = "nats"
	cfg.Chart.Repository = "https://nats-io.github.io/k8s/helm/charts"
	cfg.Chart.Version = "2.14.6"
	cfg.Image.Tag = "2.15.0-alpine"
	cfg.Placement.Pool = "database"
	cfg.Auth.SecretName = "nats-auth"
	cfg.Auth.Key = "token"
	return cfg
}

func TestHelmValuesAuthEnabled(t *testing.T) {
	values := NewNats(prodNatsConfig()).getHelmValues()

	container, ok := values["container"].(pulumi.Map)
	require.True(t, ok)
	env, ok := container["env"].(pulumi.Map)
	require.True(t, ok)
	token, ok := env["TOKEN"].(pulumi.Map)
	require.True(t, ok)
	valueFrom, ok := token["valueFrom"].(pulumi.Map)
	require.True(t, ok)
	secretRef, ok := valueFrom["secretKeyRef"].(pulumi.Map)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("nats-auth"), secretRef["name"])
	assert.Equal(t, pulumi.String("token"), secretRef["key"])

	config, ok := values["config"].(pulumi.Map)
	require.True(t, ok)
	merge, ok := config["merge"].(pulumi.Map)
	require.True(t, ok)
	auth, ok := merge["authorization"].(pulumi.Map)
	require.True(t, ok)
	// The chart's << $TOKEN >> syntax resolves the env variable at config
	// render time via the config reloader.
	assert.Equal(t, pulumi.String("<< $TOKEN >>"), auth["token"])
}

func TestHelmValuesNoAuthLeavesDefaults(t *testing.T) {
	cfg := prodNatsConfig()
	cfg.Auth = AuthConfig{}
	values := NewNats(cfg).getHelmValues()

	container, ok := values["container"].(pulumi.Map)
	require.True(t, ok)
	assert.NotContains(t, container, "env")

	// Core pub/sub: no config block at all — no resolver, no JetStream.
	assert.NotContains(t, values, "config")
}

func TestHelmValuesPoolPlacement(t *testing.T) {
	values := NewNats(prodNatsConfig()).getHelmValues()

	podTemplate, ok := values["podTemplate"].(pulumi.Map)
	require.True(t, ok)
	merge, ok := podTemplate["merge"].(pulumi.Map)
	require.True(t, ok)
	spec, ok := merge["spec"].(pulumi.Map)
	require.True(t, ok)
	assert.Equal(t, pulumi.StringMap{"pool": pulumi.String("database")}, spec["nodeSelector"])

	tolerations, ok := spec["tolerations"].(corev1.TolerationArray)
	require.True(t, ok)
	require.Len(t, tolerations, 1)
	tol, ok := tolerations[0].(*corev1.TolerationArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("dedicated"), tol.Key)
	assert.Equal(t, pulumi.String("database"), tol.Value)
	assert.Equal(t, pulumi.String("NoSchedule"), tol.Effect)
}

func TestHelmValuesNoPlacementOmitsPodTemplate(t *testing.T) {
	cfg := prodNatsConfig()
	cfg.Placement = PlacementConfig{}
	values := NewNats(cfg).getHelmValues()
	assert.NotContains(t, values, "podTemplate")
}

func TestHelmValuesCorePubSubHasNoPersistence(t *testing.T) {
	values := NewNats(prodNatsConfig()).getHelmValues()

	config, ok := values["config"].(pulumi.Map)
	require.True(t, ok)
	// Auth may set a merge block; resolver and JetStream must stay at
	// chart defaults (disabled), so the chart creates no PVCs.
	assert.NotContains(t, config, "resolver")
	assert.NotContains(t, config, "jetstream")
}
