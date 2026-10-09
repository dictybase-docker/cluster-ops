package gcp

import (
	"context"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
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
