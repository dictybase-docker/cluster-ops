package gcp

import (
	"os"
	"strings"
	"testing"
)

func TestValidateEnvironmentRequiresCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/tmp/sa.json")
	if err := validateEnvironment(); err != nil {
		t.Fatalf("validateEnvironment() = %v, want nil", err)
	}
}

func TestValidateEnvironmentMissingCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "placeholder")
	if err := os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS"); err != nil {
		t.Fatalf("unset GOOGLE_APPLICATION_CREDENTIALS: %v", err)
	}

	err := validateEnvironment()
	if err == nil {
		t.Fatal("validateEnvironment() = nil, want error")
	}
	if !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Fatalf("validateEnvironment() = %v, want mention of GOOGLE_APPLICATION_CREDENTIALS", err)
	}
}

func TestValidateEnvironmentIgnoresGitOwnedIdentity(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/tmp/sa.json")
	for _, key := range []string{
		"KOPS_CLUSTER_NAME",
		"KOPS_STATE_STORE",
		"KUBECONFIG",
		"SSH_KEY",
		"KUBERNETES_VERSION",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

	if err := validateEnvironment(); err != nil {
		t.Fatalf("validateEnvironment() = %v, want nil when only credentials are set", err)
	}
}
