package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSecretName = "dictycr"
	wantDNSErr     = "DNS-1123 label"
)

func newSampleRestoreConfig() *RestoreConfig {
	cfg := &RestoreConfig{
		Namespace:     "prod-clone",
		Bucket:        "restic-arangodb-backup-prod",
		RestoreID:     "drill-2026-01",
		ConfirmTarget: "prod-clone/arangodb/drill-2026-01",
		Storage: StorageConfig{
			Class: "dictycr-balanced",
			Size:  "150Gi",
		},
		ArangodbSecret: SecretKeyPair{Name: "arangodb-pass", Key: "password"},
		ResticSecret:   SecretKeyPair{Name: testSecretName, Key: "resticPass"},
		BucketSecret:   SecretKeyPair{Name: testSecretName, Key: "gcsCredentials"},
		ProjectSecret:  SecretKeyPair{Name: testSecretName, Key: "gcsProject"},
	}
	cfg.applyDefaults()
	return cfg
}

func TestApplyDefaults(t *testing.T) {
	cfg := &RestoreConfig{}
	cfg.applyDefaults()

	assert.Equal(t, defaultServer, cfg.Server)
	assert.Equal(t, defaultPort, cfg.Port)
	assert.Equal(t, defaultSnapshot, cfg.Snapshot)
	assert.Equal(t, defaultStorageName, cfg.Storage.Name)
	assert.Equal(t, defaultResticImage, cfg.ResticImage.Name)
	assert.Equal(t, defaultResticImageTag, cfg.ResticImage.Tag)
	assert.Equal(t, defaultArangoImage, cfg.ArangoImage.Name)
	assert.Equal(t, defaultArangoImageTag, cfg.ArangoImage.Tag)
}

func TestApplyDefaults_DoesNotOverrideExplicitValues(t *testing.T) {
	cfg := &RestoreConfig{
		Server:   "custom-coordinator",
		Port:     9529,
		Snapshot: "abc123",
	}
	cfg.applyDefaults()

	assert.Equal(t, "custom-coordinator", cfg.Server)
	assert.Equal(t, 9529, cfg.Port)
	assert.Equal(t, "abc123", cfg.Snapshot)
}

func TestTargetIdentityAndJobName(t *testing.T) {
	cfg := newSampleRestoreConfig()

	assert.Equal(t, "prod-clone/arangodb/drill-2026-01", cfg.targetIdentity())
	assert.Equal(t, "arangodb-restore-drill-2026-01", cfg.jobName())
}

func TestValidate_Success(t *testing.T) {
	cfg := newSampleRestoreConfig()
	require.NoError(t, cfg.Validate())
}

type validateErrCase struct {
	name    string
	mutate  func(*RestoreConfig)
	wantErr string
}

func runValidateErrCases(t *testing.T, tests []validateErrCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newSampleRestoreConfig()
			tt.mutate(cfg)
			assert.ErrorContains(t, cfg.Validate(), tt.wantErr)
		})
	}
}

func TestValidate_Errors_RequiredFields(t *testing.T) {
	runValidateErrCases(t, []validateErrCase{
		{
			name:    "empty namespace",
			mutate:  func(c *RestoreConfig) { c.Namespace = "" },
			wantErr: "namespace cannot be empty",
		},
		{
			name:    "empty bucket",
			mutate:  func(c *RestoreConfig) { c.Bucket = "" },
			wantErr: "bucket cannot be empty",
		},
		{
			name:    "empty storage class",
			mutate:  func(c *RestoreConfig) { c.Storage.Class = "" },
			wantErr: "storage.class cannot be empty",
		},
		{
			name:    "empty storage size",
			mutate:  func(c *RestoreConfig) { c.Storage.Size = "" },
			wantErr: "storage.size cannot be empty",
		},
	})
}

func TestValidate_Errors_RestoreID(t *testing.T) {
	runValidateErrCases(t, []validateErrCase{
		{
			name:    "empty restoreId",
			mutate:  func(c *RestoreConfig) { c.RestoreID = "" },
			wantErr: "restoreId cannot be empty",
		},
		{
			name: "restoreId too long",
			mutate: func(c *RestoreConfig) {
				c.RestoreID = "a123456789012345678901234567890123456789012345678"
				c.ConfirmTarget = c.targetIdentity()
			},
			wantErr: "must be at most",
		},
		{
			name: "restoreId with uppercase",
			mutate: func(c *RestoreConfig) {
				c.RestoreID = "Drill-2026-01"
				c.ConfirmTarget = c.targetIdentity()
			},
			wantErr: wantDNSErr,
		},
		{
			name: "restoreId with underscore",
			mutate: func(c *RestoreConfig) {
				c.RestoreID = "drill_2026_01"
				c.ConfirmTarget = c.targetIdentity()
			},
			wantErr: wantDNSErr,
		},
		{
			name: "restoreId starting with hyphen",
			mutate: func(c *RestoreConfig) {
				c.RestoreID = "-drill"
				c.ConfirmTarget = c.targetIdentity()
			},
			wantErr: wantDNSErr,
		},
	})
}

func TestValidate_Errors_Secrets(t *testing.T) {
	runValidateErrCases(t, []validateErrCase{
		{
			name:    "missing arangodb secret key",
			mutate:  func(c *RestoreConfig) { c.ArangodbSecret.Key = "" },
			wantErr: "arangodbSecret name and key are required",
		},
		{
			name:    "missing restic secret name",
			mutate:  func(c *RestoreConfig) { c.ResticSecret.Name = "" },
			wantErr: "resticSecret name and key are required",
		},
		{
			name:    "missing bucket secret",
			mutate:  func(c *RestoreConfig) { c.BucketSecret.Key = "" },
			wantErr: "bucketSecret name and key are required",
		},
		{
			name:    "missing project secret",
			mutate:  func(c *RestoreConfig) { c.ProjectSecret.Name = "" },
			wantErr: "projectSecret name and key are required",
		},
	})
}

func TestValidate_Errors_ConfirmTarget(t *testing.T) {
	runValidateErrCases(t, []validateErrCase{
		{
			name:    "confirmTarget mismatch",
			mutate:  func(c *RestoreConfig) { c.ConfirmTarget = "wrong/target/here" },
			wantErr: "confirmTarget must exactly equal",
		},
		{
			name: "confirmTarget matches a different restoreId (stale copy-paste)",
			mutate: func(c *RestoreConfig) {
				c.ConfirmTarget = "prod-clone/arangodb/some-other-run"
			},
			wantErr: "confirmTarget must exactly equal",
		},
	})
}

func TestBuildResticRestoreArgs(t *testing.T) {
	cfg := newSampleRestoreConfig()
	args := buildResticRestoreArgs(cfg)

	assert.Equal(t, []string{
		"-r", "gs:restic-arangodb-backup-prod:/",
		"restore", "latest",
		"--target", "/restore",
	}, args)
}

func TestBuildResticRestoreArgs_ExplicitSnapshot(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Snapshot = "a1b2c3d4"
	args := buildResticRestoreArgs(cfg)

	require.Len(t, args, 6)
	assert.Equal(t, "a1b2c3d4", args[3])
}

func TestScratchInputDirectory(t *testing.T) {
	assert.Equal(t, "/restore/arangodump", scratchInputDirectory())
}

func TestBuildArangorestoreArgs(t *testing.T) {
	cfg := newSampleRestoreConfig()
	args := buildArangorestoreArgs(cfg)

	assert.Equal(t, []string{
		"--server.endpoint", "http+tcp://arangodb:8529",
		"--server.username", "root",
		"--server.password", "$(ARANGO_PASSWORD)",
		"--input-directory", "/restore/arangodump",
		"--all-databases", boolTrue,
		"--include-system-collections",
		"--create-database", boolTrue,
	}, args)
}

func TestBuildArangorestoreArgs_CustomServerAndPort(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Server = "arangodb-clone"
	cfg.Port = 9529
	args := buildArangorestoreArgs(cfg)

	assert.Equal(t, "--server.endpoint", args[0])
	assert.Equal(t, "http+tcp://arangodb-clone:9529", args[1])
}

func TestImageRefs(t *testing.T) {
	cfg := newSampleRestoreConfig()

	assert.Equal(t, "restic/restic:0.17.0", resticImageRef(cfg))
	assert.Equal(t, "arangodb/arangodb:3.12.10.1", arangoImageRef(cfg))
}

func TestImageRefs_ExplicitOverride(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.ResticImage = ImageConfig{Name: "myregistry/restic", Tag: "0.18.0"}

	assert.Equal(t, "myregistry/restic:0.18.0", resticImageRef(cfg))
}
