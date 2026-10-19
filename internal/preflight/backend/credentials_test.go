package backend_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
)

func rolesFile(t *testing.T, content string) backend.CredentialsConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployer-roles.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return backend.CredentialsConfig{RolesFilePath: path}
}

// expandTo returns a RoleExpander granting n numbered permissions per role.
func expandTo(n int) backend.RoleExpander {
	return func(role string) IOE.IOEither[error, []string] {
		perms := make([]string, 0, n)
		for i := range n {
			perms = append(perms, fmt.Sprintf("%s.perm%03d", role, i))
		}
		return IOE.Of[error](perms)
	}
}

// holdAllBut is a PermissionTester that records batch sizes and grants
// everything except the listed permissions.
func holdAllBut(batchSizes *[]int, denied ...string) backend.PermissionTester {
	return func(batch []string) IOE.IOEither[error, backend.PermissionProbe] {
		*batchSizes = append(*batchSizes, len(batch))
		granted := []string{}
		for _, p := range batch {
			if !slices.Contains(denied, p) {
				granted = append(granted, p)
			}
		}
		return IOE.Of[error](backend.PermissionProbe{Granted: granted})
	}
}

func TestParseRolesSkipsBlanksAndComments(t *testing.T) {
	got := backend.ParseRoles("roles/a\n\n# comment\n  roles/b  \n")
	require.Equal(t, []string{"roles/a", "roles/b"}, got)
}

func TestIAMListsEveryMissingPermissionAcrossBatches(t *testing.T) {
	var sizes []int
	cfg := rolesFile(t, "roles/x\nroles/y\n")
	// 2 roles × 120 perms = 240 → batches of 100, 100, 40.
	test := holdAllBut(&sizes, "roles/x.perm005", "roles/y.perm119")

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(120), test)()

	require.Equal(t, preflight.StatusFail, result.Status)
	require.Equal(t, []int{100, 100, 40}, sizes)
	require.Contains(t, result.Detail, "missing 2 of 240")
	require.Contains(t, result.Detail, "roles/x.perm005")
	require.Contains(t, result.Detail, "roles/y.perm119")
}

func TestIAMDeduplicatesOverlappingRoles(t *testing.T) {
	var sizes []int
	cfg := rolesFile(t, "roles/x\nroles/x\n")

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(3), holdAllBut(&sizes))()

	require.Equal(t, preflight.StatusPass, result.Status)
	require.Equal(t, "3 permissions held", result.Detail)
}

func TestIAMReportsAPIError(t *testing.T) {
	cfg := rolesFile(t, "roles/x\n")
	failing := func(_ []string) IOE.IOEither[error, backend.PermissionProbe] {
		return IOE.Left[backend.PermissionProbe](errors.New("testIamPermissions on dcr-kube1: 403"))
	}

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(1), failing)()

	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "403")
}

func TestIAMMissingRolesFile(t *testing.T) {
	var sizes []int
	cfg := backend.CredentialsConfig{RolesFilePath: filepath.Join(t.TempDir(), "absent.txt")}

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(1), holdAllBut(&sizes))()

	require.Equal(t, preflight.StatusFail, result.Status)
	require.Empty(t, sizes, "no API call without a roles file")
}

// Regression: an empty or comment-only roles file tested nothing and passed
// the gate ("0 permissions held") — a fail-open IAM gate.
func TestIAMIgnoresPermissionsInvalidForProject(t *testing.T) {
	var sizes []int
	cfg := rolesFile(t, "roles/x\n")
	skip := func(batch []string) IOE.IOEither[error, backend.PermissionProbe] {
		sizes = append(sizes, len(batch))
		return IOE.Of[error](backend.PermissionProbe{
			Granted: []string{"roles/x.perm000"},
			Skipped: []string{"roles/x.perm001"},
		})
	}

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(2), skip)()

	require.Equal(t, preflight.StatusPass, result.Status)
	require.Equal(t, "1 permissions held", result.Detail)
}

func TestIAMRejectsEmptyRolesFile(t *testing.T) {
	var sizes []int
	cfg := rolesFile(t, "\n# nothing yet\n")

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(1), holdAllBut(&sizes))()

	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "no roles")
	require.Empty(t, sizes, "no API call without roles")
}
