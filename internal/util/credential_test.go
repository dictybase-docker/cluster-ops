package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetCredential_Replace(t *testing.T) {
	dir := t.TempDir()

	envFile := filepath.Join(dir, ".env.test.cluster")
	key1 := filepath.Join(dir, "key1.json")
	key2 := filepath.Join(dir, "key2.json")
	writeFixture(t, key1, `{}`)
	writeFixture(t, key2, `{}`)
	writeFixture(t, envFile, "# existing env file\n")

	// First write
	if err := SetCredential(envFile, key1); err != nil {
		t.Fatalf("first SetCredential: %v", err)
	}

	// Second write should replace
	if err := SetCredential(envFile, key2); err != nil {
		t.Fatalf("second SetCredential: %v", err)
	}

	content, _ := os.ReadFile(envFile)
	absKey2, _ := filepath.Abs(key2)
	if !strings.Contains(string(content), absKey2) {
		t.Errorf("expected %q in file, got: %s", absKey2, string(content))
	}
}

func TestSetCredential_ReplacesPlainLine(t *testing.T) {
	dir := t.TempDir()

	envFile := filepath.Join(dir, ".env.test.cluster")
	key := filepath.Join(dir, "key.json")
	if err := os.WriteFile(key, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	// Plain VAR=value format — no `export` prefix (matches .env.dev.dcr-experiments).
	if err := os.WriteFile(envFile, []byte("GOOGLE_APPLICATION_CREDENTIALS=${PWD}/old.json\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	if err := SetCredential(envFile, key); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}

	content, _ := os.ReadFile(envFile)
	absKey, _ := filepath.Abs(key)

	if got := strings.Count(string(content), "GOOGLE_APPLICATION_CREDENTIALS="); got != 1 {
		t.Errorf("expected exactly 1 credential line, got %d:\n%s", got, string(content))
	}
	if strings.Contains(string(content), "${PWD}/old.json") {
		t.Errorf("old credential value not replaced:\n%s", string(content))
	}
	if !strings.Contains(string(content), absKey) {
		t.Errorf("expected %q in file, got: %s", absKey, string(content))
	}
}

func TestSetCredential_MissingKey(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.test.cluster")
	writeFixture(t, envFile, "# env\n")

	err := SetCredential(envFile, "/nonexistent/key.json")
	if err == nil {
		t.Error("expected error for missing key file, got nil")
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
