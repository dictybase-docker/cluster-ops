package kops

import (
	"os"
	"strings"
	"testing"
)

func TestFindUnresolvedVars(t *testing.T) {
	tests := []struct {
		name     string
		tmpl     string
		expected []string
	}{
		{
			name:     "no vars",
			tmpl:     "hello world",
			expected: nil,
		},
		{
			name:     "var with default",
			tmpl:     "${VAR:-default}",
			expected: nil,
		},
		{
			name:     "unresolved var",
			tmpl:     "${NONEXISTENT_VAR_FOR_TEST}",
			expected: []string{"NONEXISTENT_VAR_FOR_TEST"},
		},
		{
			name:     "resolved var",
			tmpl:     "${HOME}",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findUnresolvedVars(tt.tmpl)
			if tt.expected == nil && got != nil {
				t.Errorf("expected nil, got %v", got)
				return
			}
			if len(got) != len(tt.expected) {
				t.Errorf("got %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCollectTemplates(t *testing.T) {
	dir := t.TempDir()

	t.Run("finds templates", func(t *testing.T) {
		os.WriteFile(dir+"/test.yaml.tmpl", []byte("test"), 0644)
		os.WriteFile(dir+"/other.txt", []byte("ignore"), 0644)

		files, err := collectTemplates(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 || !strings.HasSuffix(files[0], "test.yaml.tmpl") {
			t.Errorf("expected 1 template, got %v", files)
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		emptyDir := t.TempDir()
		_, err := collectTemplates(emptyDir)
		if err == nil {
			t.Error("expected error for empty template dir")
		}
	})
}
