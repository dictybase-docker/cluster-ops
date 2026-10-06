package backend

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	Arr "github.com/IBM/fp-go/v2/array"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

const (
	iamPermissionsCheckName = "IAM permissions"
	// testIamPermissions accepts at most 100 permissions per call.
	testIamBatchSize = 100
	fixDeployerRoles = "grant the deployer SA the roles in gcs-files/roles-permissions/deployer-roles.txt"
)

// CredentialsConfig holds parameters for deploy credentials checks.
type CredentialsConfig struct {
	Entry         registry.Entry
	SAKeyPath     string
	RolesFilePath string // gcs-files/roles-permissions/deployer-roles.txt
}

// RoleExpander returns the permissions a role grants.
type RoleExpander func(role string) IOE.IOEither[error, []string]

// PermissionTester returns the subset of a batch the caller holds.
type PermissionTester func(batch []string) IOE.IOEither[error, []string]

// CheckIAMPermissions verifies the deployer SA holds every permission its
// roles grant. testIamPermissions runs AS the deployer key, so grants via
// groups or inherited from the folder/org count; every missing permission
// is listed (the shell version grepped the project policy and stopped at
// the first miss).
func CheckIAMPermissions(cfg CredentialsConfig) IO.IO[preflight.Result] {
	return CheckIAMPermissionsWith(cfg, iamRoleExpander(cfg.SAKeyPath), crmPermissionTester(cfg))
}

// CheckIAMPermissionsWith is CheckIAMPermissions with injectable GCP calls.
func CheckIAMPermissionsWith(
	cfg CredentialsConfig,
	expand RoleExpander,
	test PermissionTester,
) IO.IO[preflight.Result] {
	return F.Pipe4(
		readRolesFile(cfg.RolesFilePath),
		IOE.Chain(IOE.TraverseArray(expand)),
		IOE.Map[error](F.Flow2(Arr.Flatten[string], uniqueSorted)),
		IOE.Chain(missingPermissions(test)),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   iamPermissionsCheckName,
					Detail: err.Error(),
					Fix:    fixDeployerRoles,
					Status: preflight.StatusFail,
				})
			},
			func(r permissionReport) IO.IO[preflight.Result] {
				return IO.Of(r.result())
			},
		),
	)
}

// permissionReport is the outcome of testing every wanted permission.
type permissionReport struct {
	wanted  int
	missing []string
}

func (r permissionReport) result() preflight.Result {
	if len(r.missing) > 0 {
		return preflight.Result{
			Name:   iamPermissionsCheckName,
			Detail: fmt.Sprintf("missing %d of %d: %s", len(r.missing), r.wanted, strings.Join(r.missing, ", ")),
			Fix:    fixDeployerRoles,
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{
		Name:   iamPermissionsCheckName,
		Detail: fmt.Sprintf("%d permissions held", r.wanted),
		Status: preflight.StatusPass,
	}
}

// ParseRoles returns the non-blank, non-comment lines of a roles file.
func ParseRoles(content string) []string {
	return F.Pipe2(
		strings.Split(content, "\n"),
		Arr.Map(strings.TrimSpace),
		Arr.Filter(func(line string) bool { return line != "" && !strings.HasPrefix(line, "#") }),
	)
}

func readRolesFile(path string) IOE.IOEither[error, []string] {
	return F.Pipe2(
		IOE.TryCatchError(func() ([]byte, error) { return os.ReadFile(path) }),
		IOE.Map[error](func(b []byte) []string { return ParseRoles(string(b)) }),
		// An empty or comment-only file would test nothing and pass — a
		// fail-open IAM gate.
		IOE.ChainEitherK(E.FromPredicate(
			Arr.IsNonEmpty[string],
			func([]string) error { return fmt.Errorf("no roles in %s — add the deployer roles first", path) },
		)),
	)
}

func uniqueSorted(perms []string) []string {
	out := slices.Clone(perms)
	slices.Sort(out)
	return slices.Compact(out)
}

// missingPermissions tests perms in batches and reports every one not held.
func missingPermissions(test PermissionTester) func([]string) IOE.IOEither[error, permissionReport] {
	return func(perms []string) IOE.IOEither[error, permissionReport] {
		return F.Pipe2(
			slices.Collect(slices.Chunk(perms, testIamBatchSize)),
			IOE.TraverseArray(test),
			IOE.Map[error](func(granted [][]string) permissionReport {
				held := Arr.Flatten(granted)
				return permissionReport{
					wanted: len(perms),
					missing: F.Pipe1(perms, Arr.Filter(func(p string) bool {
						return !slices.Contains(held, p)
					})),
				}
			}),
		)
	}
}

func iamRoleExpander(saKeyPath string) RoleExpander {
	return func(role string) IOE.IOEither[error, []string] {
		return IOE.TryCatchError(func() ([]string, error) {
			svc, err := iam.NewService(
				context.Background(),
				option.WithAuthCredentialsFile(option.ServiceAccount, saKeyPath),
			)
			if err != nil {
				return nil, fmt.Errorf("IAM client: %w", err)
			}
			resp, err := svc.Roles.Get(role).Do()
			if err != nil {
				return nil, fmt.Errorf("role %s: %w", role, err)
			}
			return resp.IncludedPermissions, nil
		})
	}
}

func crmPermissionTester(cfg CredentialsConfig) PermissionTester {
	return func(batch []string) IOE.IOEither[error, []string] {
		return IOE.TryCatchError(func() ([]string, error) {
			svc, err := cloudresourcemanager.NewService(
				context.Background(),
				option.WithAuthCredentialsFile(option.ServiceAccount, cfg.SAKeyPath),
			)
			if err != nil {
				return nil, fmt.Errorf("resource manager client: %w", err)
			}
			resp, err := svc.Projects.TestIamPermissions(
				cfg.Entry.GCPProject,
				&cloudresourcemanager.TestIamPermissionsRequest{Permissions: batch},
			).Do()
			if err != nil {
				return nil, fmt.Errorf("testIamPermissions on %s: %w", cfg.Entry.GCPProject, err)
			}
			return resp.Permissions, nil
		})
	}
}
