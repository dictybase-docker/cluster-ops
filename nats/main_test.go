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
	return cfg
}

func TestHelmValuesCorePubSubUnauthenticated(t *testing.T) {
	values := NewNats(prodNatsConfig()).getHelmValues()

	container, ok := values["container"].(pulumi.Map)
	require.True(t, ok)
	image, ok := imageOf(container)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("2.15.0-alpine"), image["tag"])

	// Unauthenticated core pub/sub: no config block (no authorization, no
	// resolver, no JetStream), no env injection, no podTemplate patch.
	assert.NotContains(t, values, "config")
	assert.NotContains(t, container, "env")
	assert.NotContains(t, values, "podTemplate")
}

func imageOf(container pulumi.Map) (pulumi.Map, bool) {
	image, ok := container["image"].(pulumi.Map)
	return image, ok
}

func TestHelmValuesPoolPlacement(t *testing.T) {
	cfg := prodNatsConfig()
	cfg.Placement.Pool = "database"
	values := NewNats(cfg).getHelmValues()

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
