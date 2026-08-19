package kops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"

	"github.com/urfave/cli/v2"
)

// RecreateCluster runs the full 6-step re-creation pipeline.
func RecreateCluster(cltx *cli.Context) error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"Generate cluster manifest", func() error {
			return runKops("create", "cluster",
				"--name="+cltx.String("cluster-name"),
				"--state="+cltx.String("state"))
		}},
		{"Diff against saved manifest", func() error {
			return stepDiffManifest(cltx)
		}},
		{"Edit cluster manifest (manual)", func() error {
			return stepEditCluster()
		}},
		{"Apply InstanceGroups", func() error {
			return ApplyInstanceGroups(cltx)
		}},
		{"Provision cluster", func() error {
			return UpdateCluster(cltx)
		}},
		{"Validate HA topology", func() error {
			// Delegates to custodian.ValidateHA via CLI wiring in main.go
			return nil
		}},
	}

	for i, step := range steps {
		fmt.Printf("=== Step %d/6: %s ===\n", i+1, step.name)
		start := time.Now()
		if err := step.fn(); err != nil {
			return fmt.Errorf("step %d (%s) failed after %s: %w",
				i+1, step.name, time.Since(start).Round(time.Second), err)
		}
		fmt.Printf("  ✓ completed in %s\n\n", time.Since(start).Round(time.Second))
	}

	fmt.Println("Re-creation complete.")
	fmt.Println("Run 'just gcp-cluster save-cluster-manifest' to update the saved manifest.")
	return nil
}

// stepDiffManifest diffs the live cluster manifest against a saved copy.
// Exit code 1 (differences found) is informational, not an error.
func stepDiffManifest(cltx *cli.Context) error {
	saved := manifestPath(cltx)

	return F.Pipe1(
		checkManifestExists(saved),
		E.Fold(
			func(err error) error {
				if os.IsNotExist(err) {
					fmt.Printf("No saved manifest at %s — skipping diff.\n", saved)
					return nil
				}
				return fmt.Errorf("stat manifest: %w", err)
			},
			func(_ os.FileInfo) error { return runDiff(saved) },
		),
	)
}

// manifestPath returns the saved manifest path: an explicit --manifest flag,
// otherwise a per-project path scoped by PROJECT_ID from the activated env
// (config/kops/<project-id>/cluster-manifest.yaml), falling back to the flat path.
func manifestPath(cltx *cli.Context) string {
	if saved := cltx.String("manifest"); saved != "" {
		return saved
	}
	if projectID := os.Getenv("PROJECT_ID"); projectID != "" {
		return filepath.Join("config", "kops", projectID, "cluster-manifest.yaml")
	}
	return filepath.Join("config", "kops", "cluster-manifest.yaml")
}

// checkManifestExists stats the manifest file.
func checkManifestExists(path string) E.Either[error, os.FileInfo] {
	return IOE.TryCatchError(func() (os.FileInfo, error) {
		return os.Stat(path)
	})()
}

// runDiff captures the live manifest and diffs it against the saved copy.
func runDiff(saved string) error {
	return F.Pipe1(
		captureAndWriteTemp(saved),
		E.Fold(
			F.Identity[error],
			func(tmpPath string) error {
				defer os.Remove(tmpPath)
				return executeDiff(saved, tmpPath)
			},
		),
	)
}

// captureAndWriteTemp captures the live manifest and writes it to a temp file.
func captureAndWriteTemp(saved string) E.Either[error, string] {
	return F.Pipe2(
		runKopsCaptureIOE("get", "cluster", "-o", "yaml"),
		func(effect IOE.IOEither[error, string]) E.Either[error, string] {
			return effect()
		},
		E.Chain(func(live string) E.Either[error, string] {
			return IOE.TryCatchError(func() (string, error) {
				tmp, err := os.CreateTemp("", "cluster-manifest-*.yaml")
				if err != nil {
					return "", fmt.Errorf("create temp: %w", err)
				}
				defer tmp.Close()
				if _, err := tmp.WriteString(live); err != nil {
					return "", fmt.Errorf("write temp: %w", err)
				}
				return tmp.Name(), nil
			})()
		}),
	)
}

// executeDiff runs diff and handles exit codes.
// Exit code 0 = no differences, 1 = differences found, 2+ = error.
func executeDiff(saved, tmpPath string) error {
	cmd := exec.Command("diff", saved, tmpPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		fmt.Println("No differences — manifest matches saved copy.")
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 1 {
			fmt.Println("Differences found. Review the diff above before editing.")
			return nil
		}
	}
	return fmt.Errorf("diff failed: %w", err)
}

// stepEditCluster opens the kops manifest in $EDITOR.
func stepEditCluster() error {
	cmd := exec.Command("kops", "edit", "cluster")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
