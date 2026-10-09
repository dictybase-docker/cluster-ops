package gcp

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClassifyGoogleAPI(t *testing.T) {
	require.True(t, IsNotFound(&googleapi.Error{Code: http.StatusNotFound}))
	require.True(t, IsAlreadyExists(&googleapi.Error{Code: http.StatusConflict}))
	require.True(t, IsPermissionDenied(&googleapi.Error{Code: http.StatusForbidden}))
	require.True(t, IsConflict(&googleapi.Error{Code: http.StatusConflict}))
	require.False(t, IsNotFound(&googleapi.Error{Code: http.StatusForbidden}))
}

func TestClassifyGRPCStatus(t *testing.T) {
	require.True(t, IsNotFound(status.Error(codes.NotFound, "nope")))
	require.True(t, IsAlreadyExists(status.Error(codes.AlreadyExists, "exists")))
	require.True(t, IsPermissionDenied(status.Error(codes.PermissionDenied, "denied")))
	require.True(t, IsConflict(status.Error(codes.Aborted, "etag")))
	require.False(t, IsAlreadyExists(status.Error(codes.NotFound, "nope")))
}

func TestClassifyWrappedAndUnknown(t *testing.T) {
	wrapped := fmt.Errorf("create bucket: %w", &googleapi.Error{Code: http.StatusForbidden})
	require.True(t, IsPermissionDenied(wrapped), "classification unwraps")
	require.False(t, IsNotFound(errors.New("connection refused")))
}

func TestMutationModeDefaults(t *testing.T) {
	require.False(t, (MutationMode{}).DryRun, "default runs mutations")
}
