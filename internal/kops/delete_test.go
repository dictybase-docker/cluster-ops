package kops

import (
	"testing"
)

func TestDeleteCluster_DryRun(t *testing.T) {
	t.Skip("requires kops state store — integration test")
}
