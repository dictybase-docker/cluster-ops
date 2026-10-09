package kops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
)

// validateClusterArgs mirrors the shell recipe's argv: --name/--state are
// omitted when empty so kops falls back to its own defaults.
func TestValidateClusterArgs(t *testing.T) {
	require.Equal(t,
		[]string{cmdValidate, cmdCluster, "--name=kn", "--state=gs://s", flagWait, "20m"},
		validateClusterArgs("kn", "gs://s", 20))
	require.Equal(t,
		[]string{cmdValidate, cmdCluster, flagWait, "5m"},
		validateClusterArgs("", "", 5))
}

// The production runner must kill a hung kops at the wait budget: the timeout
// classifies as context.DeadlineExceeded, not a generic exit error.
func TestValidateClusterIOETimesOut(t *testing.T) {
	shim := t.TempDir()
	sleepy := "#!/usr/bin/env bash\nexec sleep 30\n"
	//nolint:gosec // G306: test shim must be executable
	require.NoError(t, os.WriteFile(filepath.Join(shim, "kops"), []byte(sleepy), 0o755))
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	restore := validateGrace
	validateGrace = 50 * time.Millisecond
	defer func() { validateGrace = restore }()

	start := time.Now()
	_, err := E.UnwrapError(ValidateClusterIOE("kn", "gs://s", 0)())
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second, "hung kops must be killed at the deadline")
}
