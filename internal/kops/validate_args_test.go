package kops

import (
	"testing"

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
