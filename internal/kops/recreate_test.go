package kops

import (
	"os"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
)

func TestCheckManifestExists(t *testing.T) {
	t.Run("file exists", func(t *testing.T) {
		tmp := t.TempDir()
		path := tmp + "/manifest.yaml"
		os.WriteFile(path, []byte("test"), 0644)

		result := checkManifestExists(path)
		if E.IsLeft(result) {
			t.Error("expected file to exist")
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		result := checkManifestExists("/nonexistent/manifest.yaml")
		if !E.IsLeft(result) {
			t.Error("expected error for missing file")
		}
	})
}
