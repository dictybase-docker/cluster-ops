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
	for name, script := range map[string]string{"copy": copyScript, "restic": resticScript(validConfig(t))} {
		out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput()
		require.NoError(t, err, "%s: %s", name, out)
	}
	r := resticScript(validConfig(t))
	require.Contains(t, r, "backup /backup/state --tag filestash-backup --host filestash")
	require.Contains(t, r, "forget --tag filestash-backup --host filestash --keep-last 42 --keep-daily 30 --prune")
}

// TestCopyScript runs the copy script with a sqlite3 stub. A missing
// config.json must fail the copy, so restic never uploads a partial state.
func TestCopyScript(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state, out, bin := filepath.Join(root, "state"), filepath.Join(root, "backup"), filepath.Join(root, "bin")
	for _, d := range []string{filepath.Join(state, "db"), filepath.Join(state, "config"), out, bin} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(state, "db", "share.sql"), []byte("x"), 0o600))
	stub := "#!/bin/sh\ncase \"$2\" in .backup*) cp \"$1\" \"$(echo \"$2\" | sed \"s/^.backup '//; s/'$//\")\";;" +
		" PRAGMA*) echo ok;; SELECT\\ name*) echo Share;; SELECT\\ count*) echo 3;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(stub), 0o700)) //nolint:gosec // test stub
	script := strings.NewReplacer("/app/data/state", state, "/backup", out).Replace(copyScript)
	run := func() ([]byte, error) {
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		return cmd.CombinedOutput()
	}
	_, err := run()
	require.Error(t, err, "no config.json must fail the copy")

	require.NoError(t, os.WriteFile(filepath.Join(state, "config", "config.json"),
		[]byte(`{"general":{"secret_key":"abc"}}`), 0o600))
	require.NoError(t, os.RemoveAll(filepath.Join(out, "state")))
	got, err := run()
	require.NoError(t, err, "%s", got)
	for _, f := range []string{"db/share.sql", "config/config.json", "SHA256SUMS", "manifest.json", "counts.txt"} {
		require.FileExists(t, filepath.Join(out, "state", f))
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
