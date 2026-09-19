package recipeslint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlatten(t *testing.T) {
	got := flatten([]any{"kubectl get cm ", []any{"interp"}})
	require.Equal(t, "kubectl get cm \x00", got)
}

func TestFlattenAllStrings(t *testing.T) {
	require.Equal(t, "echo hello", flatten([]any{"echo hello"}))
}

func TestCodePart(t *testing.T) {
	tests := []struct{ in, want string }{
		{"echo ok", "echo ok"},
		{"echo ok # trailing", "echo ok "},
		{"echo 'a # b'", "echo 'a # b'"},
		{"echo \"a # b\" # trailing", "echo \"a # b\" "},
		{"# pulumi-gcp falls back to $GOOGLE_CLOUD_PROJECT", ""},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, codePart(tc.in), "codePart(%q)", tc.in)
	}
}

func TestInterpOutsideQuotes(t *testing.T) {
	tests := map[string]bool{
		"kubectl get cm {{name}}":       true,  // bare: flagged
		"kubectl get cm \"{{ name }}\"": false, // quoted: fine
		"echo '{{name}}'":               true,  // single quotes evaluate: flagged
		"echo \"{{ a }} {{ b }}\"":      false,
	}
	for line, want := range tests {
		require.Equal(t, want, interpOutsideQuotes(line), "interpOutsideQuotes(%q)", line)
	}
}

func TestCheckMktemp(t *testing.T) {
	for _, safe := range []string{
		"out=$(mktemp)",
		"out=$(mktemp -d)",
		"out=$(mktemp -t kubeconfig-XXXXXX)",
		"tmp_dir=$(mktemp -d \"\\x00/config/kops/.bootstrap.XXXXXX\")", // quoted template w/ interp
		"tmp=$(mktemp -d \"${dir}/.export.XXXXXX\")",
	} {
		_, hit := checkMktemp(safe)
		require.False(t, hit, "must not be flagged: %s", safe)
	}
	tmpl, hit := checkMktemp("out=$(mktemp -t kubeconfig)")
	require.True(t, hit)
	require.Equal(t, "kubeconfig", tmpl)
}

func mkRecipe(body ...string) recipe {
	r := recipe{Shebang: true}
	for _, line := range body {
		r.Body = append(r.Body, []any{line})
	}
	return r
}

func TestCheckRecipeRules(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash", // 1
		"set -euo pipefail",   // 2
		"gcloud storage buckets list --project=\"$GOOGLE_CLOUD_PROJECT\"", // 3
	)
	findings := checkRecipe("t", r)
	require.Len(t, findings, 1)
	require.Equal(t, RuleAmbientGCPProject, findings[0].Rule)
	require.Equal(t, 3, findings[0].Line)
}

func TestCheckRecipeCommentNotCode(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"# pulumi-gcp falls back to $GOOGLE_CLOUD_PROJECT when gcp:project unset",
		"echo ok",
	)
	require.Empty(t, checkRecipe("t", r), "comment must not be flagged")
}

func TestSuppressions(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash", // 1
		"set -euo pipefail",   // 2
		"out=$(mktemp -t kubeconfig)  # lint-recipes:allow-mktemp-no-x", // 3
		"out2=$(mktemp -t gcs-creds)",                                   // 4
	)
	findings := checkRecipe("t", r)
	require.Len(t, findings, 1, "suppressed line 3 must not be flagged, neighbor line 4 must")
	require.Equal(t, 4, findings[0].Line)
	require.Equal(t, RuleMktempNoX, findings[0].Rule)
}

func TestPortForwardWarn(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"kubectl port-forward -n ns svc/svc 5432:5432 &",
		"echo ok",
	)
	findings := checkRecipe("t", r)
	require.Len(t, findings, 1)
	require.True(t, findings[0].warn(), "port-forward finding must be warn-only")
	// with a trap, nothing is reported
	rTrap := mkRecipe(
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"trap cleanup EXIT",
		"kubectl port-forward -n ns svc/svc 5432:5432 &",
	)
	require.Empty(t, checkRecipe("t", rTrap), "trap must silence port-forward rule")
}

func TestWalkSorted(t *testing.T) {
	d := dump{Recipes: map[string]recipe{
		"zeta":  mkRecipe("#!/usr/bin/env bash", "set -euo pipefail", "mktemp -t a"),
		"alpha": mkRecipe("#!/usr/bin/env bash", "set -euo pipefail", "mktemp -t b"),
	}}
	findings := Walk(d)
	require.Len(t, findings, 2)
	require.Equal(t, "alpha", findings[0].Recipe, "walk output must be sorted by recipe")
}
