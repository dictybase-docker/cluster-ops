package kops

import (
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
