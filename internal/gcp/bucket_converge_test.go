package gcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

// call-name constants keep goconst quiet across the recorded fakes.
const (
	callGet    = "get"
	callCreate = "create"
	callUpdate = "update"
)

type fakeBucketAPI struct {
	attrs  *storage.BucketAttrs // nil → Get returns googleapi 404
	getErr error
	calls  []string // "get" | "create" | "update"
}

func (f *fakeBucketAPI) Get(context.Context, string) (*storage.BucketAttrs, error) {
	f.calls = append(f.calls, callGet)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.attrs == nil {
		return nil, &googleapi.Error{Code: http.StatusNotFound}
	}
	return f.attrs, nil
}

func (f *fakeBucketAPI) Create(context.Context, BucketConfig) error {
	f.calls = append(f.calls, callCreate)
	return nil
}

func (f *fakeBucketAPI) Update(context.Context, string, bool, storage.Lifecycle) error {
	f.calls = append(f.calls, callUpdate)
	return nil
}

func testBucketCfg() BucketConfig {
	return BucketConfig{
		Name: "state-bucket", ProjectID: "proj-dcr", Location: "US",
		Versioning: true,
		Lifecycle: storage.Lifecycle{Rules: []storage.LifecycleRule{{
			Action:    storage.LifecycleAction{Type: lifecycleActionDelete},
			Condition: storage.LifecycleCondition{AgeInDays: 30},
		}}},
	}
}

func TestBucketConvergeCreatesMissing(t *testing.T) {
	api := &fakeBucketAPI{}
	got, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionCreated, got.Action)
	require.Equal(t, []string{callGet, callCreate}, api.calls)
}

func TestBucketConvergeUpdatesDrift(t *testing.T) {
	api := &fakeBucketAPI{attrs: &storage.BucketAttrs{VersioningEnabled: false}}
	got, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, []string{callGet, callUpdate}, api.calls)
}

func TestBucketConvergeUnchanged(t *testing.T) {
	cfg := testBucketCfg()
	api := &fakeBucketAPI{attrs: &storage.BucketAttrs{
		VersioningEnabled: true, Lifecycle: cfg.Lifecycle,
	}}
	got, err := E.UnwrapError(ConvergeBucket(api, cfg, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Equal(t, []string{callGet}, api.calls)
}

func TestBucketConvergeDryRunMutatesNothing(t *testing.T) {
	api := &fakeBucketAPI{}
	got, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{DryRun: true})())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Equal(t, []string{callGet}, api.calls, "dry-run touches no mutation seam")
}

func TestBucketConvergeForbidden(t *testing.T) {
	api := &fakeBucketAPI{getErr: &googleapi.Error{Code: http.StatusForbidden}}
	_, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err), "403 must not read as missing")
	require.Equal(t, []string{callGet}, api.calls)
}

func TestLifecycleEqual(t *testing.T) {
	a := testBucketCfg().Lifecycle
	b := testBucketCfg().Lifecycle
	require.True(t, lifecycleEqual(a, b))
	require.False(t, lifecycleEqual(a, storage.Lifecycle{}))
}

// --lifecycle-file feeds gcloud's YAML lifecycle format (recipe parity):
// rule: [{action: {type: Delete}, condition: {age: 30}}]
func TestParseGcloudLifecycle(t *testing.T) {
	src := "rule:\n  - action:\n      type: Delete\n    condition:\n      age: 30\n"
	got, err := ParseGcloudLifecycle(strings.NewReader(src))
	require.NoError(t, err)
	want := storage.Lifecycle{Rules: []storage.LifecycleRule{{
		Action:    storage.LifecycleAction{Type: lifecycleActionDelete},
		Condition: storage.LifecycleCondition{AgeInDays: 30},
	}}}
	require.True(t, lifecycleEqual(got, want))
}
