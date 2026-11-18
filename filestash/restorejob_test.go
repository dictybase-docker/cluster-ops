package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

type jobManifest struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Name    string   `json:"name"`
					Command []string `json:"command"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func applyScript(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("manifests/restore-job.yaml")
	require.NoError(t, err)
	var m jobManifest
	require.NoError(t, yaml.Unmarshal(raw, &m))
	c := m.Spec.Template.Spec.Containers[0]
	require.Equal(t, "apply", c.Name)
	return c.Command[2]
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// snapshot writes a restored snapshot tree under root/restore/backup/state,
// as the backup copy script makes it.
func snapshot(t *testing.T, root, secret string) string {
	t.Helper()
	dir := filepath.Join(root, "restore", "backup", "state")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "db"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "config"), 0o755))
	db := filepath.Join(dir, "db", "share.sql")
	sql := "CREATE TABLE Share(id TEXT); INSERT INTO Share VALUES('a');"
	create := exec.Command("sqlite3", db, sql) //nolint:gosec // fixture
	require.NoError(t, create.Run())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config", "config.json"),
		[]byte(`{"general":{"secret_key":"abc"}}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "counts.txt"), []byte("share.sql Share 1\n"), 0o600))
	sumCmd := exec.Command("sh", "-c", "{ find db config -type f; echo counts.txt; } | sort | xargs shasum -a 256")
	sumCmd.Dir = dir
	sums, err := sumCmd.Output()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums, 0o600))
	manifest := fmt.Sprintf(`{"timestamp":"t","files":3,"configSecretSha256":"%s"}`, sha(secret))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))
	return dir
}

func runApply(t *testing.T, root, mode, secret string) ([]byte, error) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	// The image has sha256sum; macOS has shasum.
	stub := "#!/bin/sh\nexec shasum -a 256 \"$@\"\n"
	if _, err := exec.LookPath("sha256sum"); err != nil {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "sha256sum"), []byte(stub), 0o700)) //nolint:gosec // test stub
	}
	script := strings.NewReplacer("/restore", filepath.Join(root, "restore"),
		"/app/data/state", filepath.Join(root, "state")).Replace(applyScript(t))
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "MODE="+mode, "CONFIG_SECRET="+secret,
		"PATH="+bin+":"+os.Getenv("PATH"))
	return cmd.CombinedOutput()
}

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"sqlite3", "shasum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
}

func TestApplyScriptChecksAndRestores(t *testing.T) {
	t.Parallel()
	needTools(t)
	root := t.TempDir()
	snapshot(t, root, "k1")
	out, err := runApply(t, root, "check", "k1")
	require.NoError(t, err, "%s", out)
	require.NoDirExists(t, filepath.Join(root, "state"), "check mode must not touch the state")
	out, err = runApply(t, root, "restore", "k1")
	require.NoError(t, err, "%s", out)
	require.FileExists(t, filepath.Join(root, "state", "db", "share.sql"))
	require.FileExists(t, filepath.Join(root, "state", "config", "config.json"))
}

func TestApplyScriptRejects(t *testing.T) {
	t.Parallel()
	needTools(t)
	cases := map[string]struct {
		mutate func(dir string)
		secret string
		want   string
	}{
		"other CONFIG_SECRET": {func(string) {}, "k2", "does not match the snapshot"},
		"edited counts.txt": {func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "counts.txt"), []byte("share.sql Share 9\n"), 0o600)
		}, "k1", "FAILED"},
		"no share.sql": {func(dir string) {
			_ = os.Remove(filepath.Join(dir, "db", "share.sql"))
		}, "k1", "share.sql"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := snapshot(t, root, "k1")
			tc.mutate(dir)
			out, err := runApply(t, root, "restore", tc.secret)
			require.Error(t, err, "%s", out)
			require.Contains(t, string(out), tc.want)
			require.NoDirExists(t, filepath.Join(root, "state"), "a rejected snapshot must not touch the state")
		})
	}
}
