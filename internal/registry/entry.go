package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	"gopkg.in/yaml.v3"
)

// ErrEntryNotFound names a stack that has no registry entry (typed miss:
// only this case may fall back to the PULUMI_* environment).
var ErrEntryNotFound = errors.New("no registry entry")

// Entry represents a cluster registry entry from config/clusters/<name>.yaml
type Entry struct {
	Cluster            string `yaml:"cluster"`
	Stack              string `yaml:"stack"`
	KopsState          string `yaml:"kops_state"`
	GCPProject         string `yaml:"gcp_project"`
	KMSSecretsProvider string `yaml:"kms_secrets_provider"`
	PulumiState        string `yaml:"pulumi_state"`
	Namespace          string `yaml:"namespace"`
	CIEnv              string `yaml:"ci_env"`
	KopsVersion        string `yaml:"kops_version"`
	KubectlVersion     string `yaml:"kubectl_version"`
	PulumiVersion      string `yaml:"pulumi_version"`
}

// LoadByName loads the registry entry for the given cluster name.
func LoadByName(dir, name string) E.Either[error, Entry] {
	path := filepath.Join(dir, name+".yaml")
	return loadEntry(path)
}

// LoadByStack loads the registry entry matching the given stack name.
func LoadByStack(dir, stack string) E.Either[error, Entry] {
	return F.Pipe1(
		findEntryByStack(dir, stack),
		E.Chain(loadEntry),
	)
}

func findEntryByStack(dir, stack string) E.Either[error, string] {
	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return E.Left[string](fmt.Errorf("failed to list registry entries: %w", err))
	}
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var e Entry
		if err := yaml.Unmarshal(data, &e); err != nil {
			continue
		}
		if e.Stack == stack {
			return E.Right[error](path)
		}
	}
	return E.Left[string](fmt.Errorf("no registry entry with stack %q in %s: %w", stack, dir, ErrEntryNotFound))
}

func loadEntry(path string) E.Either[error, Entry] {
	data, err := os.ReadFile(path)
	if err != nil {
		return E.Left[Entry](fmt.Errorf(
			"no registry entry %s — create it with just gcp-cluster register-cluster: %w", path, err,
		))
	}
	var e Entry
	if err := yaml.Unmarshal(data, &e); err != nil {
		return E.Left[Entry](fmt.Errorf("failed to parse registry entry %s: %w", path, err))
	}
	return E.Right[error](e)
}
