package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateEnvFile_ReplaceAndAppend(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.test.cluster")

	content := "# cluster config\n" +
		"PROJECT_ID=\"old-project\"\n" +
		"export TOPOLOGY=public\n" +
		"\n" +
		"# keep me\n" +
		"KEEP_ME=\"untouched\"\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	changes, err := UpdateEnvFile(envFile, map[string]string{
		"PROJECT_ID": "new-project",
		"TOPOLOGY":   "private",
		"NEW_VAR":    "hello",
	})
	if err != nil {
		t.Fatalf("UpdateEnvFile: %v", err)
	}

	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	text := string(got)
	mustContain(t, text, "PROJECT_ID=new-project\n")
	mustNotContain(t, text, "old-project")
	mustContain(t, text, "TOPOLOGY=private\n")
	mustNotContain(t, text, "export TOPOLOGY")
	mustContain(t, text, "NEW_VAR=hello\n")
	mustContain(t, text, "KEEP_ME=\"untouched\"")
	mustContain(t, text, "# cluster config")

	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(changes), changes)
	}
	byKey := map[string]Change{}
	for _, c := range changes {
		byKey[c.Key] = c
	}
	assertChange(t, byKey["PROJECT_ID"], false, `PROJECT_ID="old-project"`, "PROJECT_ID=new-project")
	assertChange(t, byKey["TOPOLOGY"], false, "export TOPOLOGY=public", "TOPOLOGY=private")
	assertChange(t, byKey["NEW_VAR"], true, "", "NEW_VAR=hello")
}

func mustContain(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("missing %q in:\n%s", want, text)
	}
}

func mustNotContain(t *testing.T, text, ban string) {
	t.Helper()
	if strings.Contains(text, ban) {
		t.Errorf("unexpected %q in:\n%s", ban, text)
	}
}

func assertChange(t *testing.T, c Change, added bool, old, newLine string) {
	t.Helper()
	if c.Added != added || c.Old != old || c.New != newLine {
		t.Errorf("change wrong: %+v (want added=%v old=%q new=%q)", c, added, old, newLine)
	}
}

func TestUpdateEnvFile_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.test.cluster")
	// a var already present should be replaced exactly once, not duplicated
	if err := os.WriteFile(envFile, []byte("TOTAL_NODES=4\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	if _, err := UpdateEnvFile(envFile, map[string]string{"TOTAL_NODES": "6"}); err != nil {
		t.Fatalf("UpdateEnvFile: %v", err)
	}
	got, _ := os.ReadFile(envFile)
	if count := strings.Count(string(got), "TOTAL_NODES="); count != 1 {
		t.Errorf("expected 1 TOTAL_NODES line, got %d:\n%s", count, got)
	}
}

func TestUpdateEnvFile_RejectsNewlines(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.test.cluster")
	if err := os.WriteFile(envFile, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	_, err := UpdateEnvFile(envFile, map[string]string{"A": "one\ntwo"})
	if err == nil {
		t.Fatal("expected error for newline-containing value, got nil")
	}
}

func TestUpdateEnvFile_MissingFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.doesnotexist")
	_, err := UpdateEnvFile(envFile, map[string]string{"A": "1"})
	if err == nil {
		t.Fatal("expected error for missing env file, got nil")
	}
}

func TestUpdateEnvFile_AtomicNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.test.cluster")
	if err := os.WriteFile(envFile, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	if _, err := UpdateEnvFile(envFile, map[string]string{"A": "2"}); err != nil {
		t.Fatalf("UpdateEnvFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".env-sync-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}
