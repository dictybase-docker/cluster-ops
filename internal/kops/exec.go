package kops

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	IOE "github.com/IBM/fp-go/v2/ioeither"
)

// runKopsIOE executes a kops command and returns an IOEither.
// The effect is lazy — call () to execute.
func runKopsIOE(args ...string) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		cmd := exec.Command("kops", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("kops %s: %w", strings.Join(args, " "), err)
		}
		return strings.Join(args, " "), nil
	})
}

// runKopsCaptureIOE executes kops and captures stdout.
func runKopsCaptureIOE(args ...string) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		cmd := exec.Command("kops", args...)
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("kops %s: %w", strings.Join(args, " "), err)
		}
		return string(out), nil
	})
}

// runKopsCapture is a convenience wrapper that executes immediately.
func runKopsCapture(args ...string) (string, error) {
	effect := runKopsCaptureIOE(args...)
	return E.UnwrapError(effect())
}

// ValidateClusterIOE runs `kops validate cluster --name=<name> --state=<state>
// --wait <m>m` and returns its captured output. The error wraps the exec error
// (%w), so errors.Is(exec.ErrNotFound) and *exec.ExitError survive for
// classification by callers.
func ValidateClusterIOE(name, state string, waitMinutes int) IOE.IOEither[error, string] {
	return runKopsCaptureIOE(validateClusterArgs(name, state, waitMinutes)...)
}

// Shared kops argv tokens (with update.go's cmdCluster and friends).
const (
	cmdValidate = "validate"
	flagWait    = "--wait"
)

// validateClusterArgs mirrors the shell recipe's argv: --name/--state are
// omitted when empty so kops falls back to its own defaults.
func validateClusterArgs(name, state string, waitMinutes int) []string {
	args := []string{cmdValidate, cmdCluster}
	if name != "" {
		args = append(args, "--name="+name)
	}
	if state != "" {
		args = append(args, "--state="+state)
	}
	return append(args, flagWait, fmt.Sprintf("%dm", waitMinutes))
}
