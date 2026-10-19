package backend

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

func TestPermissionInvalidForResource(t *testing.T) {
	err := &googleapi.Error{
		Code:    400,
		Message: "Permission resourcemanager.projects.list is not valid for this resource.",
	}
	perm, ok := permissionInvalidForResource(fmt.Errorf("testIamPermissions: %w", err))
	require.True(t, ok)
	require.Equal(t, "resourcemanager.projects.list", perm)

	_, ok = permissionInvalidForResource(fmt.Errorf("testIamPermissions on dcr-kube1: 403"))
	require.False(t, ok)
}
