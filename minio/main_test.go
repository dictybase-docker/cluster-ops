package main

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// ociVersion is the last published Bitnami minio 17.0.x chart version on
	// the OCI registry (oci://registry-1.docker.io/bitnamicharts).
	ociVersion = "17.0.21"
	// chartName is the helm release and chart name used by every stack.
	chartName = "minio"
)

func TestGetHelmValuesLabDefaultsUnchanged(t *testing.T) {
	m := NewMinio(&MinioConfig{
		Image: ImageConfig{Tag: "2024.8.3"},
	})
	values := m.getHelmValues()

	image, ok := values["image"].(pulumi.Map)
	require.True(t, ok)
	// Lab stacks predate the repository field — legacy default applies.
	assert.Equal(t, pulumi.String("bitnamilegacy/minio"), image["repository"])
	assert.Equal(t, pulumi.String("2024.8.3"), image["tag"])

	// No placement configured — no scheduling keys at all.
	assert.NotContains(t, values, "nodeSelector")
	assert.NotContains(t, values, "tolerations")

	// Chart 17 removed disableWebUI — it must not be emitted.
	assert.NotContains(t, values, "disableWebUI")
}

func TestGetHelmValuesProductionPlacement(t *testing.T) {
	m := NewMinio(&MinioConfig{
		Image:     ImageConfig{Repository: "bitnamilegacy/minio", Tag: "2025.7.23-debian-12-r3"},
		Placement: Placement{Pool: "database"},
		WebUI:     false,
	})
	values := m.getHelmValues()

	selector, ok := values["nodeSelector"].(pulumi.Map)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("database"), selector["pool"])

	tolerations, ok := values["tolerations"].(pulumi.Array)
	require.True(t, ok)
	require.Len(t, tolerations, 1)
	toleration, ok := tolerations[0].(pulumi.Map)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("dedicated"), toleration["key"])
	assert.Equal(t, pulumi.String("Equal"), toleration["operator"])
	assert.Equal(t, pulumi.String("database"), toleration["value"])
	assert.Equal(t, pulumi.String("NoSchedule"), toleration["effect"])

	console, ok := values["console"].(pulumi.Map)
	require.True(t, ok)
	assert.Equal(t, pulumi.Bool(false), console["enabled"])
}

func TestChartSourceHTTPSRepo(t *testing.T) {
	// Legacy stacks still carry the removed HTTPS repo; it must pass through
	// untouched so lab `pulumi up` behavior is unchanged.
	const legacyRepo = "https://charts.bitnami.com/bitnami"
	c := ChartConfig{Name: chartName, Repository: legacyRepo, Version: "17.0.23"}
	chart, repo := c.chartSource()
	assert.Equal(t, chartName, chart)
	assert.Equal(t, legacyRepo, repo)
}

func TestChartSourceOCI(t *testing.T) {
	const ociRepo = "oci://registry-1.docker.io/bitnamicharts"
	c := ChartConfig{Name: chartName, Repository: ociRepo, Version: ociVersion}
	chart, repo := c.chartSource()
	assert.Equal(t, ociRepo+"/minio", chart)
	// RepositoryOpts must be omitted for OCI charts.
	assert.Empty(t, repo)
}

func TestChartSourceOCITrailingSlash(t *testing.T) {
	const ociRepo = "oci://registry-1.docker.io/bitnamicharts"
	c := ChartConfig{Name: chartName, Repository: ociRepo + "/", Version: ociVersion}
	chart, repo := c.chartSource()
	assert.Equal(t, ociRepo+"/minio", chart)
	assert.Empty(t, repo)
}

func TestChartSourceEmptyRepo(t *testing.T) {
	c := ChartConfig{Name: chartName, Version: ociVersion}
	chart, repo := c.chartSource()
	assert.Equal(t, chartName, chart)
	assert.Empty(t, repo)
}
