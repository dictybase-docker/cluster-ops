package gcp

import (
	"testing"
)

func TestDisableRequestDefaultLeavesDependentsEnabled(t *testing.T) {
	req := disableRequest("dcr-kube1", "bigquery.googleapis.com", false)

	if got := req.GetName(); got != "projects/dcr-kube1/services/bigquery.googleapis.com" {
		t.Fatalf("Name = %q", got)
	}
	if req.GetDisableDependentServices() {
		t.Fatal("DisableDependentServices must default to false")
	}
}

func TestDisableRequestOptInDisablesDependents(t *testing.T) {
	req := disableRequest("dcr-kube1", "bigquery.googleapis.com", true)

	if !req.GetDisableDependentServices() {
		t.Fatal("DisableDependentServices must be true when opt-in flag is set")
	}
}
