package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSecretName      = "dictycr"
	testSourceIdentity  = "dictycr-source"
	testSourceBucket    = "source-restic-bucket"
	testBootstrapID     = "bootstrap-20260827-120000"
	testBootstrapSnap   = "a1b2c3"
	testDatabaseName    = "mydb"
	testProdRepository  = "gs:restic-arangodb-backup-prod:/"
	testEndpoint        = "http+tcp://arangodb:8529"
	testScratchRoot     = "/restore"
	testDumpDirectory   = "/restore/arangodump"
	testDumpDatabaseDir = "/restore/arangodump/mydb"
	testSnapshotInclude = "/arangodump/mydb"

	wantDNSErr       = "DNS-1123 label"
	wantDatabaseErr  = "database"
	wantOverwriteErr = "overwrite"
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

// newSampleBootstrapConfig is the cross-project first-load shape from
// docs/arangodb-deploy.md §4: the SOURCE project's bucket, the read-only
// `dictycr-source` identity, a pinned restic snapshot (never "latest"), a
// bootstrap-* restoreId, and --no-lock because the source SA cannot write a
// restic lock file.
func newSampleBootstrapConfig() *RestoreConfig {
	cfg := &RestoreConfig{
		Namespace:     "prod",
		Bucket:        testSourceBucket,
		Snapshot:      testBootstrapSnap,
		RestoreID:     testBootstrapID,
		ConfirmTarget: "prod/arangodb/" + testBootstrapID,
		NoLock:        true,
		Storage: StorageConfig{
			Class: "dictycr-balanced",
			Size:  "150Gi",
		},
		ArangodbSecret: SecretKeyPair{Name: "arangodb-pass", Key: "password"},
		ResticSecret:   SecretKeyPair{Name: testSourceIdentity, Key: "resticPass"},
		BucketSecret:   SecretKeyPair{Name: testSourceIdentity, Key: "gcsCredentials"},
		ProjectSecret:  SecretKeyPair{Name: testSourceIdentity, Key: "gcsProject"},
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

func TestValidate_Success_DatabaseFilter(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Database = testDatabaseName

	require.NoError(t, cfg.Validate())
}

func TestValidate_Success_SystemDatabase(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Database = "_system"

	require.NoError(t, cfg.Validate())
}

func TestValidate_Success_LongestDatabaseName(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Database = "a" + strings.Repeat("b", 63)

	require.NoError(t, cfg.Validate())
}

func TestValidate_Errors_DatabaseName(t *testing.T) {
	runValidateErrCases(t, []validateErrCase{
		{
			name:    "starts with a digit",
			mutate:  func(c *RestoreConfig) { c.Database = "1db" },
			wantErr: wantDatabaseErr,
		},
		{
			name:    "contains a space",
			mutate:  func(c *RestoreConfig) { c.Database = "db name" },
			wantErr: wantDatabaseErr,
		},
		{
			name:    "contains a slash",
			mutate:  func(c *RestoreConfig) { c.Database = "db/name" },
			wantErr: wantDatabaseErr,
		},
		{
			name: "65 chars",
			mutate: func(c *RestoreConfig) {
				c.Database = "a" + strings.Repeat("b", 64)
			},
			wantErr: wantDatabaseErr,
		},
	})
}

func TestValidate_Errors_OverwriteWithoutDatabase(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Overwrite = true

	assert.ErrorContains(t, cfg.Validate(), wantOverwriteErr)
}

func TestBuildResticRestoreArgs(t *testing.T) {
	cfg := newSampleRestoreConfig()
	args := buildResticRestoreArgs(cfg)

	assert.Equal(t, []string{
		argRepository, testProdRepository,
		argRestore, defaultSnapshot,
		argTarget, testScratchRoot,
	}, args)
}

func TestBuildResticRestoreArgs_ExplicitSnapshot(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Snapshot = "a1b2c3d4"
	args := buildResticRestoreArgs(cfg)

	require.Len(t, args, 6)
	assert.Equal(t, "a1b2c3d4", args[3])
}

func TestApplyDefaults_NoLockDefaultsFalse(t *testing.T) {
	cfg := &RestoreConfig{}
	cfg.applyDefaults()

	assert.False(t, cfg.NoLock, "noLock must default to false so DR drills keep locking")
}

func TestBootstrapConfig_Validate(t *testing.T) {
	cfg := newSampleBootstrapConfig()

	require.NoError(t, cfg.Validate())
	assert.Equal(t, "prod/arangodb/"+testBootstrapID, cfg.targetIdentity())
	assert.Equal(t, "arangodb-restore-"+testBootstrapID, cfg.jobName())
	assert.LessOrEqual(t, len(cfg.jobName()), 63)
}

func TestBootstrapConfig_ConfirmTargetMismatchStillFails(t *testing.T) {
	cfg := newSampleBootstrapConfig()
	cfg.ConfirmTarget = "prod/arangodb/some-other-run"

	assert.ErrorContains(t, cfg.Validate(), "confirmTarget must exactly equal")
}

func TestBuildResticRestoreArgs_Bootstrap_NoLock(t *testing.T) {
	cfg := newSampleBootstrapConfig()
	args := buildResticRestoreArgs(cfg)

	assert.Equal(t, []string{
		argNoLock,
		argRepository, "gs:source-restic-bucket:/",
		argRestore, testBootstrapSnap,
		argTarget, testScratchRoot,
	}, args)
}

func TestBuildResticRestoreArgs_DefaultOmitsNoLock(t *testing.T) {
	cfg := newSampleRestoreConfig()
	args := buildResticRestoreArgs(cfg)

	assert.NotContains(t, args, argNoLock)
	assert.Equal(t, argRepository, args[0])
}

func TestScratchInputDirectory(t *testing.T) {
	cfg := newSampleRestoreConfig()

	assert.Equal(t, testDumpDirectory, scratchInputDirectory(cfg))
}

func TestScratchInputDirectory_DatabaseFilter(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Database = testDatabaseName

	assert.Equal(t, testDumpDatabaseDir, scratchInputDirectory(cfg))
}

func TestBuildResticRestoreArgs_DatabaseFilter(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Database = testDatabaseName
	args := buildResticRestoreArgs(cfg)

	assert.Equal(t, []string{
		argRepository, testProdRepository,
		argRestore, defaultSnapshot,
		argTarget, testScratchRoot,
		argInclude, testSnapshotInclude,
	}, args)
}

func TestBuildArangorestoreArgs_DatabaseFilter(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Database = testDatabaseName
	args := buildArangorestoreArgs(cfg)

	assert.Equal(t, []string{
		argServerEndpoint, testEndpoint,
		argServerUsername, argRootUser,
		argServerPassword, argPasswordFromEnv,
		argInputDirectory, testDumpDatabaseDir,
		argServerDatabase, testDatabaseName,
		argSystemColls,
		argCreateDatabase, boolTrue,
	}, args)
	assert.NotContains(t, args, argAllDatabases)
}

func TestBuildArangorestoreArgs_DatabaseFilterOverwrite(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Database = testDatabaseName
	cfg.Overwrite = true
	args := buildArangorestoreArgs(cfg)

	assert.Equal(t, []string{
		argServerEndpoint, testEndpoint,
		argServerUsername, argRootUser,
		argServerPassword, argPasswordFromEnv,
		argInputDirectory, testDumpDatabaseDir,
		argServerDatabase, testDatabaseName,
		argSystemColls,
		argCreateDatabase, boolTrue,
		argOverwrite, boolTrue,
	}, args)
}

func TestBuildArangorestoreArgs(t *testing.T) {
	cfg := newSampleRestoreConfig()
	args := buildArangorestoreArgs(cfg)

	assert.Equal(t, []string{
		argServerEndpoint, testEndpoint,
		argServerUsername, argRootUser,
		argServerPassword, argPasswordFromEnv,
		argInputDirectory, testDumpDirectory,
		argAllDatabases, boolTrue,
		argSystemColls,
		argCreateDatabase, boolTrue,
	}, args)
}

func TestBuildArangorestoreArgs_CustomServerAndPort(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.Server = "arangodb-clone"
	cfg.Port = 9529
	args := buildArangorestoreArgs(cfg)

	assert.Equal(t, argServerEndpoint, args[0])
	assert.Equal(t, "http+tcp://arangodb-clone:9529", args[1])
}

// Whole-instance restores must stay byte-identical to the pre-database-filter
// behavior: an empty Database/Overwrite may never leak --include,
// --server.database, or --overwrite into the DR drill's argument lists.
func TestBuildArgs_WholeInstanceRegression(t *testing.T) {
	cfg := newSampleRestoreConfig()
	assert.Empty(t, cfg.Database)
	assert.False(t, cfg.Overwrite)

	assert.Equal(t, []string{
		argRepository, testProdRepository,
		argRestore, defaultSnapshot,
		argTarget, testScratchRoot,
	}, buildResticRestoreArgs(cfg))
	assert.Equal(t, []string{
		argServerEndpoint, testEndpoint,
		argServerUsername, argRootUser,
		argServerPassword, argPasswordFromEnv,
		argInputDirectory, testDumpDirectory,
		argAllDatabases, boolTrue,
		argSystemColls,
		argCreateDatabase, boolTrue,
	}, buildArangorestoreArgs(cfg))
}

// Every flag the two argument builders emit comes from a named constant, so a
// typo in one is invisible to the arg-list tests above — they assert the same
// constants. This is the single place that pins each constant to the spelling
// restic and arangorestore actually accept, and that `boolTrue` stays the
// string form their boolean flags expect.
func TestArgConstantSpellings(t *testing.T) {
	cases := []struct {
		what string
		want string
		got  string
	}{
		{"repository", "-r", argRepository},
		{"no-lock", "--no-lock", argNoLock},
		{"restore subcommand", "restore", argRestore},
		{"target", "--target", argTarget},
		{"include", "--include", argInclude},
		{"server.endpoint", "--server.endpoint", argServerEndpoint},
		{"server.username", "--server.username", argServerUsername},
		{"server.password", "--server.password", argServerPassword},
		{"server.database", "--server.database", argServerDatabase},
		{"input-directory", "--input-directory", argInputDirectory},
		{"all-databases", "--all-databases", argAllDatabases},
		{"include-system-collections", "--include-system-collections", argSystemColls},
		{"create-database", "--create-database", argCreateDatabase},
		{"overwrite", "--overwrite", argOverwrite},
		{"root user", "root", argRootUser},
		{"password from env", "$(ARANGO_PASSWORD)", argPasswordFromEnv},
		{"boolean true", "true", boolTrue},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.got, "%s spelling drifted from what the binary accepts", tc.what)
	}
}

func TestImageRefs(t *testing.T) {
	cfg := newSampleRestoreConfig()

	assert.Equal(t, "restic/restic:0.17.0", resticImageRef(cfg))
	assert.Equal(t, "arangodb:3.12.11", arangoImageRef(cfg))
}

func TestImageRefs_ExplicitOverride(t *testing.T) {
	cfg := newSampleRestoreConfig()
	cfg.ResticImage = ImageConfig{Name: "myregistry/restic", Tag: "0.18.0"}

	assert.Equal(t, "myregistry/restic:0.18.0", resticImageRef(cfg))
}
