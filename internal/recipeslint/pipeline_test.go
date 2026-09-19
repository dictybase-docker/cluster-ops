package recipeslint

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureFindings lints the testdata fixture via the real `just --dump`.
func fixtureFindings(t *testing.T) []Finding {
	t.Helper()
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not installed")
	}
	out, err := exec.Command( //nolint:gosec // fixed argv, no shell; justfile path from the test fixture
		"just", "--justfile", filepath.Join("testdata", "Justfile"),
		"--dump", "--dump-format", "json",
	).Output()
	require.NoError(t, err, "just --dump")
	var d dump
	require.NoError(t, json.Unmarshal(out, &d))
	return Walk(d)
}

func TestWalkFixture(t *testing.T) {
	want := []Finding{
		{Recipe: "ambient-gcp-project", Line: 3, Rule: RuleAmbientGCPProject,
			Detail: `gcloud storage buckets list --project="$GOOGLE_CLOUD_PROJECT"`},
		{Recipe: "create-pipe-swallow", Line: 3, Rule: RuleCreatePipeSwallow,
			Detail: `gcloud iam service-accounts create sa-name --project="$PROJECT" || echo "already exists"`},
		{Recipe: "hardcoded-stack", Line: 3, Rule: RuleHardcodedStack,
			Detail: `pulumi stack output bucketName --stack prod`},
		{Recipe: "mktemp-template-no-x", Line: 3, Rule: RuleMktempNoX,
			Detail: "kubeconfig"},
		{Recipe: "suppressed-mktemp", Line: 4, Rule: RuleMktempNoX,
			Detail: "gcs-creds"},
		{Recipe: "unquoted-interp", Line: 3, Rule: RuleUnquotedInterp,
			Detail: "kubectl get cm \x00"},
	}
	got := fixtureFindings(t)
	require.Equal(t, want, got, "fixture findings mismatch")
}

func TestRunFailsOnFixture(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not installed")
	}
	require.Error(t, Run(Config{Root: "testdata"}), "Run must fail on the dirty fixture")
}

func TestRepoClean(t *testing.T) {
	err := Run(Config{Root: filepath.Join("..", "..")})
	require.NoError(t, err, "repo justfiles have findings — fix them before committing")
}
