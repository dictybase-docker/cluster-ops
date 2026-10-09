package gcp

import (
	"context"
	"net/http"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

type fakeDeleteAPI struct {
	calls []string
	count int64
}

func (f *fakeDeleteAPI) CountObjects(context.Context, string) (int64, error) {
	f.calls = append(f.calls, callCount)
	return f.count, nil
}

func (f *fakeDeleteAPI) DeleteObjects(context.Context, string) error {
	f.calls = append(f.calls, callPurge)
	return nil
}

func (f *fakeDeleteAPI) DeleteBucket(context.Context, string) error {
	f.calls = append(f.calls, callDeleteBucket)
	return nil
}

func TestBucketDeleteDryRunDeletesNothing(t *testing.T) {
	api := &fakeDeleteAPI{count: 42}
	got, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{DryRun: true})())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Equal(t, []string{callCount}, api.calls, "dry-run touches no mutation seam")
}

func TestBucketDeleteRefusesWithoutConfirm(t *testing.T) {
	api := &fakeDeleteAPI{count: 42}
	_, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{})())
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmation required")
	require.Equal(t, []string{callCount}, api.calls, "refusal precedes every mutation")
}

func TestBucketDeletePreflightOrder(t *testing.T) {
	api := &fakeDeleteAPI{count: 42}
	got, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{Confirmed: true})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	// count → purge (objects+versions) → delete-bucket: strict order.
	require.Equal(t, []string{callCount, callPurge, callDeleteBucket}, api.calls)
}

func TestBucketDeleteEmptyBucketSkipsPurge(t *testing.T) {
	api := &fakeDeleteAPI{count: 0}
	_, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{Confirmed: true})())
	require.NoError(t, err)
	require.Equal(t, []string{callCount, callDeleteBucket}, api.calls)
}

// A missing bucket is a clean no-op in BOTH modes (the recipe's "Nothing to
// delete"); only real probe errors (403 etc.) fail.
type absentDeleteAPI struct {
	fakeDeleteAPI
}

func (a *absentDeleteAPI) CountObjects(context.Context, string) (int64, error) {
	a.calls = append(a.calls, callCount)
	return 0, &googleapi.Error{Code: http.StatusNotFound}
}

func TestBucketDeleteAbsentIsNoop(t *testing.T) {
	api := &absentDeleteAPI{}
	got, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Equal(t, []string{callCount}, api.calls, "absent bucket touches no mutation seam")
}

// A classified probe failure (403) surfaces before any mutation — the old
// shell preview treated it as "Nothing to delete".
func TestBucketDeleteProbeErrorNamed(t *testing.T) {
	api := &blockedDeleteAPI{err: &googleapi.Error{Code: http.StatusForbidden}}
	_, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{Confirmed: true})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, api.purged, "a forbidden probe never deletes")
}

type blockedDeleteAPI struct {
	err    error
	purged int
}

func (b *blockedDeleteAPI) CountObjects(context.Context, string) (int64, error) {
	return 0, b.err
}

func (b *blockedDeleteAPI) DeleteObjects(context.Context, string) error {
	b.purged++
	return nil
}

func (b *blockedDeleteAPI) DeleteBucket(context.Context, string) error {
	b.purged++
	return nil
}
