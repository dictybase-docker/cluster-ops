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
	os.WriteFile(key1, []byte(`{}`), 0644)
	os.WriteFile(key2, []byte(`{}`), 0644)
	os.WriteFile(envFile, []byte("# existing env file\n"), 0644)

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

func TestSetCredential_MissingKey(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.test.cluster")
	os.WriteFile(envFile, []byte("# env\n"), 0644)

	err := SetCredential(envFile, "/nonexistent/key.json")
	if err == nil {
		t.Error("expected error for missing key file, got nil")
	}
}
