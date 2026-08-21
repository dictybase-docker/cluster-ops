package kops

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
)

func TestRunKopsIOE(t *testing.T) {
	_, err := exec.LookPath("kops")
	if err != nil {
		t.Skip("kops not installed — skipping integration test")
	}

	effect := runKopsIOE("version")
	result := effect() // Either[error, string]
	if E.IsLeft(result) {
		t.Fatalf("kops version failed")
	}
	output := E.Fold(
		func(err error) string { return "" },
		func(s string) string { return s },
	)(result)
	if output == "" {
		t.Error("expected non-empty output from kops version")
	}
}

func TestRunKopsCaptureIOE(t *testing.T) {
	_, err := exec.LookPath("kops")
	if err != nil {
		t.Skip("kops not installed — skipping integration test")
	}

	effect := runKopsCaptureIOE("version")
	result := effect()
	if E.IsLeft(result) {
		t.Fatalf("kops version capture failed")
	}
	output := E.Fold(
		func(err error) string { return "" },
		func(s string) string { return s },
	)(result)
	if output == "" {
		t.Error("expected non-empty output from kops version")
	}
}

func TestBuildKopsArgs(t *testing.T) {
	args := []string{"create", "cluster", "--name", "test.k8s.local", "--state", "gs://bucket/", "--project", "proj"}
	expected := "kops create cluster --name test.k8s.local --state gs://bucket/ --project proj"
	actual := fmt.Sprintf("kops %s", joinArgs(args))
	if actual != expected {
		t.Errorf("got %q, want %q", actual, expected)
	}
}

func joinArgs(args []string) string {
	result := args[0]
	for i := 1; i < len(args); i++ {
		if args[i] == "" {
			continue
		}
		result += " " + args[i]
	}
	return result
}

var _ = os.Stdout
