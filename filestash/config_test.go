package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const (
	testDigest = "sha256:89e70f0fccc4dcce3f47e5d490ee961f912a5ffd4e30b98216e3e37e469035e2"
	testHost   = "files.dictybase.dev"
	testTLS    = "filestash-tls-secret"
	sizeField  = "storage.size"
	testHash   = "$2y$10$abcdefghijklmnopqrstuuvwxyzabcdefghijklmnopqrstuvwxyz12"
)

func validConfig() *FilestashConfig {
	cfg := &FilestashConfig{
		Namespace:      "dev",
		ApplicationURL: "https://" + testHost,
		Image:          ImageConfig{Tag: "latest", Digest: testDigest},
		Storage:        StorageConfig{Size: "2Gi"},
		S3:             S3Config{Endpoint: "http://minio.dev.svc.cluster.local:9000"},
		Backup:         BackupConfig{MaxAgeHours: 6},
		Secrets:        SecretsConfig{AdminPasswordHash: testHash, ConfigSecret: "c0nfigsecretc0nfigsecretc0nfigse"},
		Users: map[string]UserKeys{
			"intake":    {AccessKey: "filestash-intake", SecretKey: "s1"},
			"public":    {AccessKey: "filestash-public", SecretKey: "s2"},
			"operator":  {AccessKey: "filestash-operator", SecretKey: "s3"},
			"publisher": {AccessKey: "filestash-publisher", SecretKey: "s4"},
		},
		Plugins: PluginsConfig{Image: ImageConfig{
			Name: "dictybase/filestash-plugins", Tag: "2026.11.17", Digest: testDigest,
		}},
	}
	cfg.ApplyDefaults()
	return cfg
}

func TestApplyDefaults(t *testing.T) {
	t.Parallel()
	cfg := &FilestashConfig{}
	cfg.ApplyDefaults()
	require.Equal(t, "filestash", cfg.Name)
	require.Equal(t, 8334, cfg.Port)
	require.Equal(t, 1, cfg.Replicas)
	require.Equal(t, "machines/filestash", cfg.Image.Name)
	require.Equal(t, "dictycr-balanced", cfg.Storage.StorageClass)
	require.Equal(t, "2Gi", cfg.CacheSizeLimit)
	require.Equal(t, "filestash", cfg.SecretName)
	require.Equal(t, "us-east-1", cfg.S3.Region)
	require.Equal(t, "dictycr-minio", cfg.S3.Label)
	require.Equal(t, "nginx", cfg.Ingress.ClassName)
	require.Empty(t, cfg.Storage.Size, "storage.size has no code default")
	require.Zero(t, cfg.Backup.MaxAgeHours, "backup.maxAgeHours has no code default")
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	require.NoError(t, validConfig().Validate())
}

func TestValidateRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*FilestashConfig)
		field  string
	}{
		{"empty storage size", func(c *FilestashConfig) { c.Storage.Size = "" }, sizeField},
		{"bad storage size", func(c *FilestashConfig) { c.Storage.Size = "lots" }, sizeField},
		{"small storage size", func(c *FilestashConfig) { c.Storage.Size = "512Mi" }, sizeField},
		{"bad cache size", func(c *FilestashConfig) { c.CacheSizeLimit = "x" }, "cacheSizeLimit"},
		{"empty namespace", func(c *FilestashConfig) { c.Namespace = "" }, "namespace"},
		{"empty digest", func(c *FilestashConfig) { c.Image.Digest = "" }, "image.digest"},
		{"short digest", func(c *FilestashConfig) { c.Image.Digest = "sha256:abc" }, "image.digest"},
		{"empty tag", func(c *FilestashConfig) { c.Image.Tag = "" }, "image.tag"},
		{"two replicas", func(c *FilestashConfig) { c.Replicas = 2 }, "replicas"},
		{"placeholder url", func(c *FilestashConfig) { c.ApplicationURL = "https://<filestash-host>" }, "applicationURL"},
		{"http url", func(c *FilestashConfig) { c.ApplicationURL = "http://" + testHost }, "applicationURL"},
		{"other name", func(c *FilestashConfig) { c.Name = "files" }, "name"},
		{"other secret name", func(c *FilestashConfig) { c.SecretName = "other" }, "secretName"},
		{"no backup max age", func(c *FilestashConfig) { c.Backup.MaxAgeHours = 0 }, "backup.maxAgeHours"},
		{"no config secret", func(c *FilestashConfig) { c.Secrets.ConfigSecret = "" }, "secrets.configSecret"},
		{
			"plain admin password",
			func(c *FilestashConfig) { c.Secrets.AdminPasswordHash = "hunter2" },
			"secrets.adminPasswordHash",
		},
		{"missing publisher keys", func(c *FilestashConfig) { delete(c.Users, "publisher") }, "users.publisher"},
		{"empty plugins digest", func(c *FilestashConfig) { c.Plugins.Image.Digest = "" }, "plugins.image.digest"},
		{"empty plugins name", func(c *FilestashConfig) { c.Plugins.Image.Name = "" }, "plugins.image.name"},
		{"placeholder plugins tag", func(c *FilestashConfig) { c.Plugins.Image.Tag = "<plugins-tag>" }, "plugins.image.tag"},
		{"empty s3 endpoint", func(c *FilestashConfig) { c.S3.Endpoint = "" }, "s3.endpoint"},
		{"ingress without host", func(c *FilestashConfig) {
			c.Ingress.Enabled = true
			c.Ingress.TLSSecret = testTLS
		}, "ingress.hosts"},
		{"ingress placeholder host", func(c *FilestashConfig) {
			c.Ingress.Enabled = true
			c.Ingress.Hosts = []string{"<filestash-host>"}
			c.Ingress.TLSSecret = testTLS
		}, "ingress.hosts"},
		{"ingress without tls", func(c *FilestashConfig) {
			c.Ingress.Enabled = true
			c.Ingress.Hosts = []string{testHost}
		}, "ingress.tlsSecret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.field)
		})
	}
}

func TestImageRef(t *testing.T) {
	t.Parallel()
	require.Equal(t, "machines/filestash:latest@"+testDigest, validConfig().Image.Ref())
}

type stackFile struct {
	Config map[string]FilestashConfig `json:"config"`
}

func readStack(t *testing.T, path string) FilestashConfig {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf stackFile
	require.NoError(t, yaml.Unmarshal(raw, &sf))
	cfg, ok := sf.Config["filestash:properties"]
	require.True(t, ok, "%s has no filestash:properties", path)
	return cfg
}

func TestStackFilesValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file      string
		namespace string
		ingress   bool
	}{
		{"Pulumi.dcr-kube1.yaml", "prod", false},
		{"Pulumi.experiments.yaml", "dev", true},
	}
	for _, tc := range cases {
		cfg := readStack(t, tc.file)
		cfg.ApplyDefaults()
		require.Equal(t, tc.namespace, cfg.Namespace, tc.file)
		require.Equal(t, tc.ingress, cfg.Ingress.Enabled, tc.file)
		require.NotEmpty(t, cfg.Storage.Size, tc.file)
		err := cfg.Validate()
		if err != nil {
			require.Contains(t, err.Error(), "placeholder", "%s: only placeholder errors are allowed", tc.file)
		}
	}
}
