package toolchain

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
)

func runAll(cfg ToolsConfig) []preflight.Result {
	return preflight.Run(CheckTools(cfg))
}

func fakeTools(versions map[string]string, missing map[string]bool) ToolsConfig {
	return ToolsConfig{
		LookPath: func(bin string) (string, error) {
			if missing[bin] {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/" + bin, nil
		},
		Version: func(bin string) (string, error) {
			if v, ok := versions[bin]; ok {
				return v, nil
			}
			return "", errors.New("no version")
		},
	}
}

func TestCheckToolsAllPresent(t *testing.T) {
	got := runAll(fakeTools(map[string]string{
		binPulumi:  "3.255.0\n",
		binGcloud:  "Google Cloud SDK 451.0.0\n",
		binKubectl: `{"clientVersion":{"gitVersion":"v1.28.8"}}`,
		binJq:      "jq-1.7\n",
		binYq:      "yq (https://github.com/mikefarah/yq/) version v4.44.1\n",
	}, nil))
	require.Len(t, got, 5)
	for _, r := range got {
		require.Equal(t, preflight.StatusPass, r.Status, "%s: %s", r.Name, r.Detail)
	}
	require.Equal(t, binPulumi+" 3.255.0", got[0].Detail)
	require.Equal(t, binGcloud+" 451.0.0", got[1].Detail)
	require.Equal(t, binKubectl+" v1.28.8", got[2].Detail)
	require.Equal(t, binJq+" jq-1.7", got[3].Detail)
	require.Contains(t, got[4].Detail, "v4.44.1")
}

func TestCheckToolsMissingBinary(t *testing.T) {
	got := runAll(fakeTools(nil, map[string]bool{binKubectl: true}))
	var kubectl preflight.Result
	for _, r := range got {
		if r.Name == binKubectl {
			kubectl = r
		}
	}
	require.Equal(t, preflight.StatusFail, kubectl.Status)
	require.Equal(t, "not found on PATH", kubectl.Detail)
	require.Len(t, got, 5, "never short-circuit: every tool is reported")
}

func TestCheckToolsVersionUnknown(t *testing.T) {
	got := runAll(fakeTools(map[string]string{binJq: ""}, nil))
	require.Equal(t, preflight.StatusPass, got[3].Status)
	require.Equal(t, binJq+" version unknown", got[3].Detail)
}

func TestCheckToolsVersionReaders(t *testing.T) {
	cfg := DefaultToolsConfig()
	require.NotNil(t, cfg.LookPath)
	require.NotNil(t, cfg.Version)
}
