package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSampleSourceConfig() *SourceBackupSecretsConfig {
	cfg := &SourceBackupSecretsConfig{Namespace: "prod"}
	cfg.Secret.Name = "dictycr-source"
	cfg.Secret.ResticPass = "source-restic-password"
	cfg.Secret.GcsProject = "source-gcp-project"
	cfg.Secret.ServiceAccount.Filepath = "/tmp/arangodb-restic-reader.json"
	cfg.Secret.ServiceAccount.Keyname = "gcsCredentials"
	return cfg
}

func TestValidate_Success(t *testing.T) {
	sbs := NewSourceBackupSecrets(newSampleSourceConfig())
	require.NoError(t, sbs.Validate())
}

func TestValidate_NilConfig(t *testing.T) {
	sbs := NewSourceBackupSecrets(nil)
	assert.ErrorContains(t, sbs.Validate(), "config cannot be nil")
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SourceBackupSecretsConfig)
		wantErr string
	}{
		{
			name:    "empty namespace",
			mutate:  func(c *SourceBackupSecretsConfig) { c.Namespace = "" },
			wantErr: "namespace cannot be empty",
		},
		{
			name:    "empty secret name",
			mutate:  func(c *SourceBackupSecretsConfig) { c.Secret.Name = "" },
			wantErr: "secret.name cannot be empty",
		},
		{
			name:    "empty restic password",
			mutate:  func(c *SourceBackupSecretsConfig) { c.Secret.ResticPass = "" },
			wantErr: "secret.resticPass cannot be empty",
		},
		{
			name:    "empty gcs project",
			mutate:  func(c *SourceBackupSecretsConfig) { c.Secret.GcsProject = "" },
			wantErr: "secret.gcsProject cannot be empty",
		},
		{
			name: "empty service account filepath",
			mutate: func(c *SourceBackupSecretsConfig) {
				c.Secret.ServiceAccount.Filepath = ""
			},
			wantErr: "secret.serviceAccount.filepath cannot be empty",
		},
		{
			name: "empty service account keyname",
			mutate: func(c *SourceBackupSecretsConfig) {
				c.Secret.ServiceAccount.Keyname = ""
			},
			wantErr: "secret.serviceAccount.keyname cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newSampleSourceConfig()
			tt.mutate(cfg)
			sbs := NewSourceBackupSecrets(cfg)
			assert.ErrorContains(t, sbs.Validate(), tt.wantErr)
		})
	}
}

// The source Secret must carry the SAME key names as `dictycr` so the restore
// Job's env/volume wiring resolves; only the values differ. A rename here
// breaks the Job silently, so the default keyname is pinned by a test.
func TestSampleConfig_UsesDictycrCompatibleKeyname(t *testing.T) {
	cfg := newSampleSourceConfig()
	assert.Equal(t, "gcsCredentials", cfg.Secret.ServiceAccount.Keyname)
}
