package kops

import (
	"strings"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/blang/semver"
)

func TestParseSemver(t *testing.T) {
	v131 := semver.MustParse("1.31.0")

	// Direct test: parseSemver("1.31.0") should succeed
	v := parseSemver("1.31.0")
	if E.IsLeft(v) {
		// Extract error for debugging
		err := E.Fold(
			func(e error) error { return e },
			func(sv semver.Version) error { return nil },
		)(v)
		t.Fatalf("parseSemver(1.31.0) failed: %v", err)
	}

	isGTE := E.Fold(
		func(err error) bool { return false },
		func(sv semver.Version) bool { return sv.GTE(v131) },
	)(v)
	if !isGTE {
		t.Error("1.31.0 should be GTE 1.31.0")
	}
}

func TestParseSemver_Failures(t *testing.T) {
	for _, ver := range []string{"", "not-a-version", "v1.31.0"} {
		v := parseSemver(ver)
		if !E.IsLeft(v) {
			t.Errorf("expected error for %q, got success", ver)
		}
	}
}

func TestUpdateCommandArgs(t *testing.T) {
	vLegacy := semver.MustParse("1.29.2")
	vReconcile := semver.MustParse("1.31.0")
	vModern := semver.MustParse("1.36.3")

	tests := []struct {
		name     string
		version  semver.Version
		apply    bool
		expected string
	}{
		{
			name:     "legacy version apply",
			version:  vLegacy,
			apply:    true,
			expected: "update cluster --yes --admin",
		},
		{
			name:     "legacy version plan",
			version:  vLegacy,
			apply:    false,
			expected: "update cluster --admin",
		},
		{
			name:     "v1.31.0 boundary apply",
			version:  vReconcile,
			apply:    true,
			expected: "reconcile cluster --yes",
		},
		{
			name:     "v1.31.0 boundary plan",
			version:  vReconcile,
			apply:    false,
			expected: "reconcile cluster",
		},
		{
			name:     "modern version apply",
			version:  vModern,
			apply:    true,
			expected: "reconcile cluster --yes",
		},
		{
			name:     "modern version plan",
			version:  vModern,
			apply:    false,
			expected: "reconcile cluster",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := strings.Join(updateCommandArgs(tc.version, tc.apply), " ")
			if actual != tc.expected {
				t.Errorf("expected command %q, got %q", tc.expected, actual)
			}
		})
	}
}
