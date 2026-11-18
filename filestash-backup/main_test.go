package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

type mocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
	secrets   map[string]bool
}

func (m *mocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *mocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	m.secrets[args.Name] = args.Inputs.ContainsSecrets()
	return args.Name, args.Inputs, nil
}

func validConfig(t *testing.T) *BackupConfig {
	t.Helper()
	key := filepath.Join(t.TempDir(), "sa.json")
	require.NoError(t, os.WriteFile(key, []byte(`{"type":"service_account"}`), 0o600))
	cfg := &BackupConfig{
		Namespace: "prod",
		Bucket:    "restic-filestash-backup-dcr-kube1",
		Secret: SecretConfig{
			ResticPass:         "pw",
			GcsProject:         "dcr-kube1",
			ServiceAccountFile: key,
		},
	}
	cfg.ApplyDefaults()
	return cfg
}

func install(t *testing.T, cfg *BackupConfig) *mocks {
	t.Helper()
	m := &mocks{resources: map[string]pulumi.MockResourceArgs{}, secrets: map[string]bool{}}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewBackup(cfg).Install(ctx)
	}, pulumi.WithMocks("filestash-backup", "test", m))
	require.NoError(t, err)
	return m
}

func get(t *testing.T, m *mocks, name string) map[string]any {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.resources[name]
	require.True(t, ok, "resource %s missing", name)
	return res.Inputs.Mappable()
}

func dig(t *testing.T, v any, path ...any) any {
	t.Helper()
	cur := v
	for _, p := range path {
		switch key := p.(type) {
		case string:
			obj, ok := cur.(map[string]any)
			require.True(t, ok, "not an object at %v", key)
			cur = obj[key]
		case int:
			arr, ok := cur.([]any)
			require.True(t, ok, "not an array at %d", key)
			require.Greater(t, len(arr), key)
			cur = arr[key]
		}
	}
	return cur
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	cfg := &BackupConfig{}
	cfg.ApplyDefaults()
	require.Equal(t, "30 */4 * * *", cfg.Schedule)
	require.Equal(t, 42, cfg.KeepLast)
	require.Equal(t, 30, cfg.KeepDaily)
	require.Equal(t, "filestash-backup-auth", cfg.Secret.Name)
	require.Contains(t, cfg.ResticImage, "restic/restic:0.17.0@sha256:")
	require.Contains(t, cfg.SqliteImage, "keinos/sqlite3:3.54.1@sha256:")
}

func TestValidateRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		mutate func(*BackupConfig)
		field  string
	}{
		"namespace":   {func(c *BackupConfig) { c.Namespace = "" }, "namespace"},
		"bucket":      {func(c *BackupConfig) { c.Bucket = "minio-bucket" }, "bucket"},
		"restic pass": {func(c *BackupConfig) { c.Secret.ResticPass = "" }, "secret.resticPass"},
		"project":     {func(c *BackupConfig) { c.Secret.GcsProject = "" }, "secret.gcsProject"},
		"key file":    {func(c *BackupConfig) { c.Secret.ServiceAccountFile = "" }, "secret.serviceAccountFile"},
		"keep":        {func(c *BackupConfig) { c.KeepLast = -1 }, "keepLast"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig(t)
			tc.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.field)
		})
	}
	require.NoError(t, validConfig(t).Validate())
}

func TestBucket(t *testing.T) {
	t.Parallel()
	b := get(t, install(t, validConfig(t)), "restic-filestash-backup-dcr-kube1")
	require.Equal(t, "restic-filestash-backup-dcr-kube1", b["name"])
	require.Equal(t, true, dig(t, b, "versioning", "enabled"))
}

func TestAuthSecretIsSecret(t *testing.T) {
	t.Parallel()
	m := install(t, validConfig(t))
	sec := get(t, m, "filestash-backup-auth")
	require.Equal(t, "prod", dig(t, sec, "metadata", "namespace"))
	data := fmt.Sprint(dig(t, sec, "stringData"))
	for _, k := range []string{"resticPass", "gcsProject", "gcsCredentials"} {
		require.Contains(t, data, k)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	require.True(t, m.secrets["filestash-backup-auth"])
}

func cronPod(t *testing.T, m *mocks) map[string]any {
	t.Helper()
	cj := get(t, m, "filestash-state-backup")
	require.Equal(t, "Forbid", dig(t, cj, "spec", "concurrencyPolicy"))
	require.Equal(t, "30 */4 * * *", dig(t, cj, "spec", "schedule"))
	require.Equal(t, "state-backup",
		dig(t, cj, "spec", "jobTemplate", "metadata", "labels", "app.kubernetes.io/component"))
	pod, ok := dig(t, cj, "spec", "jobTemplate", "spec", "template", "spec").(map[string]any)
	require.True(t, ok)
	return pod
}

func TestCronJobIsOnline(t *testing.T) {
	t.Parallel()
	pod := cronPod(t, install(t, validConfig(t)))
	all := fmt.Sprint(pod)
	require.NotContains(t, all, "scale", "the backup must not stop Filestash")
	require.NotContains(t, all, "serviceAccountName")
	aff := dig(t, pod, "affinity", "podAffinity", "requiredDuringSchedulingIgnoredDuringExecution", 0)
	require.Equal(t, "kubernetes.io/hostname", dig(t, aff, "topologyKey"))
	require.Equal(t, "filestash", dig(t, aff, "labelSelector", "matchLabels", "app.kubernetes.io/name"))
	require.Equal(t, "copy", dig(t, pod, "initContainers", 0, "name"))
	require.Equal(t, "restic", dig(t, pod, "containers", 0, "name"))
	require.Contains(t, fmt.Sprint(pod["volumes"]), "claimName:filestash-state")
	env := fmt.Sprint(dig(t, pod, "containers", 0, "env"))
	require.Contains(t, env, "gs:restic-filestash-backup-dcr-kube1:/")
	require.Contains(t, env, "name:filestash-backup-auth")
}

func TestScriptsParse(t *testing.T) {
	t.Parallel()
	for name, script := range map[string]string{"copy": copyScript, "restic": resticScript(validConfig(t))} { //nolint:lll // table
		out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput()
		require.NoError(t, err, "%s: %s", name, out)
	}
	r := resticScript(validConfig(t))
	require.Contains(t, r, "backup /backup/state --tag filestash-backup --host filestash")
	require.Contains(t, r, "forget --tag filestash-backup --host filestash --keep-last 42 --keep-daily 30 --prune")
}

// copyEnv prepares a state folder and runs the copy script with the real
// sqlite3 CLI. It returns the output folder and the script result.
type copyCase struct {
	share  bool
	config string
	secret string
}

func runCopy(t *testing.T, c copyCase) (string, []byte, error) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not installed")
	}
	root := t.TempDir()
	state, out := filepath.Join(root, "state"), filepath.Join(root, "backup")
	for _, d := range []string{filepath.Join(state, "db"), filepath.Join(state, "config"), out} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	if c.share {
		db := filepath.Join(state, "db", "share.sql")
		create := exec.Command("sqlite3", db, "CREATE TABLE Share(id TEXT); INSERT INTO Share VALUES('a'),('b');")
		require.NoError(t, create.Run())
	}
	if c.config != "" {
		require.NoError(t, os.WriteFile(filepath.Join(state, "config", "config.json"), []byte(c.config), 0o600))
	}
	script := strings.NewReplacer("/app/data/state", state, "/backup", out).Replace(copyScript)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "CONFIG_SECRET="+c.secret)
	got, err := cmd.CombinedOutput()
	return filepath.Join(out, "state"), got, err
}

const goodConfig = `{"general":{"secret_key":"abc"}}`

func TestCopyScriptFailsClosed(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		c    copyCase
		want string
	}{
		"no share.sql":       {copyCase{share: false, config: goodConfig, secret: "s"}, "share.sql"},
		"no config.json":     {copyCase{share: true, secret: "s"}, "config.json"},
		"corrupt config":     {copyCase{share: true, config: `{"general":`, secret: "s"}, "malformed JSON"},
		"empty secret_key":   {copyCase{share: true, config: `{"general":{"secret_key":""}}`, secret: "s"}, "secret_key"},
		"missing secret_key": {copyCase{share: true, config: `{"general":{}}`, secret: "s"}, "secret_key"},
		"no CONFIG_SECRET":   {copyCase{share: true, config: goodConfig}, "CONFIG_SECRET is not set"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, out, err := runCopy(t, tc.c)
			require.Error(t, err, "%s", out)
			require.Contains(t, string(out), tc.want)
		})
	}
}

func TestCopyScriptWritesCheckedCopy(t *testing.T) {
	t.Parallel()
	dir, out, err := runCopy(t, copyCase{share: true, config: goodConfig, secret: "s3cret"})
	require.NoError(t, err, "%s", out)
	for _, f := range []string{"db/share.sql", "config/config.json", "SHA256SUMS", "manifest.json", "counts.txt"} {
		require.FileExists(t, filepath.Join(dir, f))
	}
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	require.NoError(t, err)
	require.Contains(t, string(sums), "counts.txt", "counts.txt must be under the checksums")
	counts, err := os.ReadFile(filepath.Join(dir, "counts.txt"))
	require.NoError(t, err)
	require.Equal(t, "share.sql Share 2\n", string(counts))
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	require.NoError(t, err)
	// sha256("s3cret")
	require.Contains(t, string(manifest),
		`"configSecretSha256":"1ec1c26b50d5d3c58d9583181af8076655fe00756bf7285940ba3670f99fcba0"`)
}

func TestCronJobMountsConfigSecret(t *testing.T) {
	t.Parallel()
	pod := cronPod(t, install(t, validConfig(t)))
	env := fmt.Sprint(dig(t, pod, "initContainers", 0, "env"))
	require.Contains(t, env, "name:CONFIG_SECRET")
	require.Contains(t, env, "key:CONFIG_SECRET")
	require.Contains(t, env, "name:filestash")
}

// TestResticInitOnlyWhenAbsent runs the restic script with a restic stub.
// Exit code 10 means "repository does not exist" (restic 0.17). Any other
// failure must stop the Job without `restic init`.
func TestResticInitOnlyWhenAbsent(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		catExit  int
		wantErr  bool
		wantInit bool
	}{
		"present":      {0, false, false},
		"absent":       {10, false, true},
		"bad password": {12, true, false},
		"network":      {1, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bin := t.TempDir()
			log := filepath.Join(bin, "log")
			stub := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %s\n[ \"$1\" = cat ] && exit %d\nexit 0\n", log, tc.catExit)
			require.NoError(t, os.WriteFile(filepath.Join(bin, "restic"), []byte(stub), 0o700)) //nolint:gosec // test stub
			script := resticScript(validConfig(t))
			cmd := exec.Command("sh", "-c", script) //nolint:gosec // script under test
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if tc.wantErr {
				require.Error(t, err, "%s", out)
			} else {
				require.NoError(t, err, "%s", out)
			}
			calls, _ := os.ReadFile(log)
			require.Equal(t, tc.wantInit, strings.Contains(string(calls), "init\n"), "%s", calls)
			require.Equal(t, !tc.wantErr, strings.Contains(string(calls), "backup "), "%s", calls)
		})
	}
}

type stackFile struct {
	Config map[string]any `json:"config"`
}

func TestStackFiles(t *testing.T) {
	t.Parallel()
	for file, want := range map[string][2]string{
		"Pulumi.dcr-kube1.yaml":   {"prod", "restic-filestash-backup-dcr-kube1"},
		"Pulumi.experiments.yaml": {"dev", "restic-filestash-backup-dcr-experiments"},
	} {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		var sf stackFile
		require.NoError(t, yaml.Unmarshal(raw, &sf))
		props, ok := sf.Config["filestash-backup:properties"].(map[string]any)
		require.True(t, ok, file)
		require.Equal(t, want[0], props["namespace"], file)
		require.Equal(t, want[1], props["bucket"], file)
	}
}
