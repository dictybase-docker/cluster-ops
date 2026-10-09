// Package toolchain checks the local toolchain binaries and versions
// (migrated from scripts/pulumi/check-tools.sh).
package toolchain

import (
	"encoding/json"
	"os/exec"
	"strings"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
)

// ToolsConfig injects PATH lookup and version capture (the LoginReader
// pattern), so checks are testable without real binaries.
type ToolsConfig struct {
	LookPath func(bin string) (string, error)
	Version  func(bin string) (string, error) // returns the raw version output
}

// Tool binary names.
const (
	binPulumi  = "pulumi"
	binGcloud  = "gcloud"
	binKubectl = "kubectl"
	binJq      = "jq"
	binYq      = "yq"
)

// tools is the checked toolchain in the shell's order.
var tools = []string{binPulumi, binGcloud, binKubectl, binJq, binYq}

// DefaultToolsConfig wires exec.LookPath and the per-tool version commands
// exactly as scripts/pulumi/check-tools.sh runs them.
func DefaultToolsConfig() ToolsConfig {
	return ToolsConfig{
		LookPath: exec.LookPath,
		Version: func(bin string) (string, error) {
			out, err := exec.Command(bin, versionArgs(bin)...).Output() //nolint:gosec // G204: fixed per-tool argv below
			return string(out), err
		},
	}
}

// versionArgs mirrors the shell's version invocations.
func versionArgs(bin string) []string {
	switch bin {
	case binPulumi, binGcloud:
		return []string{"version"}
	case binKubectl:
		return []string{"version", "--client", "-o", "json"}
	default:
		return []string{"--version"}
	}
}

// CheckTools returns one check per tool, in the shell's order. Missing binary
// is a fail; a version that cannot be read still passes as "version unknown".
func CheckTools(cfg ToolsConfig) []IO.IO[preflight.Result] {
	checks := make([]IO.IO[preflight.Result], 0, len(tools))
	for _, tool := range tools {
		checks = append(checks, checkTool(cfg, tool))
	}
	return checks
}

func checkTool(cfg ToolsConfig, tool string) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if _, err := cfg.LookPath(tool); err != nil {
			return preflight.Result{Name: tool, Detail: "not found on PATH", Status: preflight.StatusFail}
		}
		version := "version unknown"
		if out, err := cfg.Version(tool); err == nil {
			if v := parseVersion(tool, out); v != "" && v != "null" {
				version = v
			}
		}
		return preflight.Result{Name: tool, Detail: tool + " " + version, Status: preflight.StatusPass}
	}
}

// parseVersion extracts the displayed version per tool. Display parsing only —
// never used to detect error conditions.
func parseVersion(tool, out string) string {
	switch tool {
	case binGcloud:
		for line := range strings.SplitSeq(out, "\n") {
			rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Google Cloud SDK ")
			if !ok {
				continue
			}
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return fields[len(fields)-1]
			}
		}
		return ""
	case binKubectl:
		var v struct {
			ClientVersion struct {
				GitVersion string `json:"gitVersion"`
			} `json:"clientVersion"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			return ""
		}
		return v.ClientVersion.GitVersion
	default:
		lines := strings.Split(out, "\n")
		return strings.TrimSpace(lines[0])
	}
}
