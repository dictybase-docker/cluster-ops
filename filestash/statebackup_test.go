package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func renderedBackupScripts() map[string]string {
	return map[string]string{
		"scale-down": fmt.Sprintf(scaleDownScript, "prod", nameLabel, scaleTimeout, workPath),
		"copy":       fmt.Sprintf(copyScript, workPath, statePath, defaultBackupKeep),
		"scale-up":   fmt.Sprintf(scaleUpScript, "prod", workPath),
	}
}

func TestBackupScriptsParse(t *testing.T) {
	t.Parallel()
	for name, script := range renderedBackupScripts() {
		out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput()
		require.NoError(t, err, "%s: %s", name, out)
	}
}

// TestCopyScriptFailsWhenAStepFails runs the copy script with no
// config.json, so the plain cp step fails. The copy must be "fail" and no
// completed copy may exist.
func TestCopyScriptFailsWhenAStepFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work, state, bin := filepath.Join(root, "work"), filepath.Join(root, "state"), filepath.Join(root, "bin")
	for _, d := range []string{work, filepath.Join(state, "db"), filepath.Join(state, "config"), bin} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(work, "scale.status"), []byte("ok\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(state, "db", "share.sql"), []byte("x"), 0o600))
	stub := "#!/bin/sh\ncase \"$2\" in .backup*) cp \"$1\" \"$(echo \"$2\" | sed \"s/^.backup '//; s/'$//\")\";;" +
		" PRAGMA*) echo ok;; *) ;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(stub), 0o700)) //nolint:gosec // test stub
	script := fmt.Sprintf(copyScript, work, state, defaultBackupKeep)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the copy step never exits non-zero: %s", out)
	status, err := os.ReadFile(filepath.Join(work, "copy.status"))
	require.NoError(t, err)
	require.Equal(t, "fail", strings.TrimSpace(string(status)))
	entries, err := filepath.Glob(filepath.Join(state, "backups", "2*"))
	require.NoError(t, err)
	require.Empty(t, entries, "a failed copy must not become a completed copy")
	partials, err := filepath.Glob(filepath.Join(state, "backups", ".partial-*"))
	require.NoError(t, err)
	require.Empty(t, partials, "a failed copy must not leave a partial folder")
}

func TestCopyScriptRemovesOldPartialsFirst(t *testing.T) {
	t.Parallel()
	script := renderedBackupScripts()["copy"]
	clean := strings.Index(script, "-name '.partial-*' -exec rm -rf {} +")
	backup := strings.Index(script, ".backup")
	require.GreaterOrEqual(t, clean, 0)
	require.Less(t, clean, backup, "remove old partial folders before the copy (full PVC)")
}

func TestScaleDownDoesNotUseWaitForDelete(t *testing.T) {
	t.Parallel()
	script := renderedBackupScripts()["scale-down"]
	require.NotContains(t, script, "wait --for=delete", "kubectl wait fails when no pod matches")
	require.Contains(t, script, "get pods -l app.kubernetes.io/name=filestash -o name")
}

func TestScaleUpRetries(t *testing.T) {
	t.Parallel()
	script := renderedBackupScripts()["scale-up"]
	require.Contains(t, script, "for i in 1 2 3 4 5")
	require.Contains(t, script, "scale deployment/filestash --replicas=1 && break")
}
