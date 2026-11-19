package main

import (
	"os"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const (
	testDigest = "sha256:89e70f0fccc4dcce3f47e5d490ee961f912a5ffd4e30b98216e3e37e469035e2"
	testRepo   = "us-central1-docker.pkg.dev/dcr-kube1/falcon/falcon-sensor"
	testCID    = "0123456789ABCDEF0123456789ABCDEF-A1"
	testToken  = "tok"
)

func validConfig() *FalconConfig {
	return &FalconConfig{
		Namespace:   "falcon-system",
		ClusterName: "dcr-kube1",
		Cloud:       "us-1",
		Chart: ChartConfig{
			Name:       "falcon-sensor",
			Repository: "https://crowdstrike.github.io/falcon-helm",
			Version:    "1.37.0",
		},
		Image:    ImageConfig{Repository: testRepo, Tag: "7.40.0-18806-1", Digest: testDigest},
		Registry: RegistryConfig{Location: "us-central1", Repository: "falcon"},
		CID:      testCID,
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	require.NoError(t, validConfig().Validate())
}

func TestValidateRejectsInvalidFields(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*FalconConfig){
		"namespace":           func(c *FalconConfig) { c.Namespace = "" },
		"clusterName":         func(c *FalconConfig) { c.ClusterName = "" },
		cloudKey:              func(c *FalconConfig) { c.Cloud = "us-central1" },
		"chart.name":          func(c *FalconConfig) { c.Chart.Name = "falcon-operator" },
		"chart.repository":    func(c *FalconConfig) { c.Chart.Repository = "http://example.test" },
		"chart.version":       func(c *FalconConfig) { c.Chart.Version = "" },
		"registry.location":   func(c *FalconConfig) { c.Registry.Location = "" },
		"registry.repository": func(c *FalconConfig) { c.Registry.Repository = "" },
		"image.tag":           func(c *FalconConfig) { c.Image.Tag = "latest; rm" },
		"image.digest":        func(c *FalconConfig) { c.Image.Digest = "sha256:abc" },
		"cid":                 func(c *FalconConfig) { c.CID = "0123456789abcdef0123456789abcdef" },
	}
	for field, mutate := range cases {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), "is invalid in")
			require.Contains(t, err.Error(), field)
		})
	}
}

func TestValidateRejectsForeignRepository(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{
		"registry.crowdstrike.com/falcon-sensor/release/falcon-sensor",
		"us-east1-docker.pkg.dev/dcr-kube1/falcon/falcon-sensor",
		"us-central1-docker.pkg.dev/dcr-kube1/other/falcon-sensor",
		"us-central1-docker.pkg.dev/dcr-kube1/falcon/falcon-container",
		"us-central1-docker.pkg.dev/a/b/falcon/falcon-sensor",
		"",
	} {
		cfg := validConfig()
		cfg.Image.Repository = repo
		err := cfg.Validate()
		require.Error(t, err, repo)
		require.Contains(t, err.Error(), "image.repository", repo)
	}
}

func TestValidateRejectsPlaceholders(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.Cloud = "<falcon-cloud>"
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "contains placeholder in cloud")
}

func TestValidateNeverPrintsSecrets(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.CID = "deadbeef-secret-cid"
	cfg.ProvisioningToken = "<tok-secret>"
	err := cfg.Validate()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "deadbeef-secret-cid")
	require.NotContains(t, err.Error(), "tok-secret")
}

func TestSecretData(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	require.Equal(t, map[string]string{cidKey: testCID}, SecretData(cfg))
	cfg.ProvisioningToken = testToken
	require.Equal(t, map[string]string{cidKey: testCID, tokenKey: testToken}, SecretData(cfg))
}

func TestHelmValues(t *testing.T) {
	t.Parallel()
	vals := HelmValues(validConfig())
	require.Equal(t, pulumi.String(releaseName), vals["fullnameOverride"])
	require.Equal(t, pulumi.Map{
		enabledKey:   pulumi.Bool(true),
		"secretName": pulumi.String(secretName),
	}, vals["falconSecret"])
	require.Equal(t, pulumi.Map{cloudKey: pulumi.String("us-1")}, vals["falcon"])
	node, ok := vals["node"].(pulumi.Map)
	require.True(t, ok)
	require.Equal(t, pulumi.String("dcr-kube1"), node["clusterName"])
	require.NotContains(t, node, "backend", "node.backend is deprecated in chart 1.37")
	require.Equal(t, pulumi.Map{
		"repository": pulumi.String(testRepo),
		"digest":     pulumi.String(testDigest),
		"pullPolicy": pulumi.String("IfNotPresent"),
	}, node["image"])
	// One wildcard toleration covers control-plane, spot, and dedicated taints.
	require.Equal(t, pulumi.Map{"tolerations": pulumi.Array{
		pulumi.Map{"operator": pulumi.String("Exists")},
	}}, node["daemonset"])
	require.Equal(t, pulumi.Map{enabledKey: pulumi.Bool(false)}, vals["container"])
}

func TestHelmValuesTags(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.Tags = "dcr-kube1,prod"
	require.Equal(t, pulumi.Map{
		cloudKey: pulumi.String("us-1"),
		"tags":   pulumi.String("dcr-kube1,prod"),
	}, HelmValues(cfg)["falcon"])
}

func TestPodSecurityLabels(t *testing.T) {
	t.Parallel()
	require.Len(t, PodSecurityLabels(), 3)
	for _, v := range PodSecurityLabels() {
		require.Equal(t, pulumi.String("privileged"), v)
	}
}

// TestStackFilesMatchProgram keeps every committed stack file on the fixed
// chart name and an https chart repository, and keeps operator values out
// of the plain-text file.
func TestStackFilesMatchProgram(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("Pulumi.dcr-kube1.yaml")
	require.NoError(t, err)
	var doc struct {
		Config map[string]map[string]any `json:"config"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	props := doc.Config["falcon-sensor:properties"]
	for _, key := range []string{"cid", "provisioningToken"} {
		_, plain := props[key].(string)
		require.False(t, plain, "%s is a KMS secret, never plain text", key)
	}
	chart, ok := props["chart"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, chartName, chart["name"])
	require.NotEmpty(t, chart["version"])
	require.Equal(t, "falcon-system", props["namespace"])
	require.Equal(t, "dcr-kube1", props["clusterName"])
}
