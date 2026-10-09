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
	callGet          = "get"
	callCreate       = "create"
	callUpdate       = "update"
	callCount        = "count"
	callPurge        = "purge"
	callDeleteBucket = "delete-bucket"
	callSet          = "set"
	callGetSA        = "get-sa"
	callCreateSA     = "create-sa"
	callListKeys     = "list-keys"
	callCreateKey    = "create-key"
	callDeleteKey    = "delete-key"
	callMkdirAll     = "mkdir-all"
)

type fakeBucketAPI struct {
	attrs            *storage.BucketAttrs // nil → Get returns googleapi 404
	getErr           error
	createErr        error
	attrsAfterCreate *storage.BucketAttrs // served by Get after a raced create
	created          bool
	calls            []string // callGet | callCreate | callUpdate
}

func (f *fakeBucketAPI) Get(context.Context, string) (*storage.BucketAttrs, error) {
	f.calls = append(f.calls, callGet)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.attrs == nil {
		if f.created && f.attrsAfterCreate != nil {
			return f.attrsAfterCreate, nil
		}
		return nil, &googleapi.Error{Code: http.StatusNotFound}
	}
	return f.attrs, nil
}

func (f *fakeBucketAPI) Create(context.Context, BucketConfig) error {
	f.calls = append(f.calls, callCreate)
	f.created = true
	return f.createErr
}

func (f *fakeBucketAPI) Update(context.Context, string, bool, *storage.Lifecycle) error {
	f.calls = append(f.calls, callUpdate)
	return nil
}

func testBucketCfg() BucketConfig {
	return BucketConfig{
		Name: "state-bucket", ProjectID: "proj-dcr", Location: "US",
		Versioning: true,
		Lifecycle: &storage.Lifecycle{Rules: []storage.LifecycleRule{{
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
		VersioningEnabled: true, Lifecycle: *cfg.Lifecycle,
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
	a := *testBucketCfg().Lifecycle
	b := *testBucketCfg().Lifecycle
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

// I1: no --lifecycle-file must LEAVE existing rules untouched (the first
// draft converged an empty lifecycle and cleared the bucket's rules).
func TestBucketConvergeLeavesLifecycleWithoutFile(t *testing.T) {
	existing := storage.Lifecycle{Rules: []storage.LifecycleRule{{
		Action:    storage.LifecycleAction{Type: lifecycleActionDelete},
		Condition: storage.LifecycleCondition{AgeInDays: 90},
	}}}
	cfg := testBucketCfg()
	cfg.Lifecycle = nil // --lifecycle-file omitted
	api := &fakeBucketAPI{attrs: &storage.BucketAttrs{
		VersioningEnabled: true, Lifecycle: existing,
	}}
	got, err := E.UnwrapError(ConvergeBucket(api, cfg, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action,
		"absent lifecycle config must not clear existing rules")
}

// C3: the parser maps the full common condition set…
func TestParseGcloudLifecycleFull(t *testing.T) {
	src := "rule:\n  - action:\n      type: Delete\n    condition:\n" +
		"      age: 30\n      isLive: true\n      numNewerVersions: 3\n" +
		"      matchesStorageClass: [STANDARD]\n      matchesPrefix: [backups/]\n      date: 2030-01-01\n"
	got, err := ParseGcloudLifecycle(strings.NewReader(src))
	require.NoError(t, err)
	require.Len(t, got.Rules, 1)
	c := got.Rules[0].Condition
	require.Equal(t, int64(30), c.AgeInDays)
	require.Equal(t, storage.Live, c.Liveness)
	require.Equal(t, int64(3), c.NumNewerVersions)
	require.Equal(t, []string{"STANDARD"}, c.MatchesStorageClasses)
	require.Equal(t, []string{"backups/"}, c.MatchesPrefix)
	require.False(t, c.CreatedBefore.IsZero())
}

// …and FAILS CLOSED on anything it cannot map: dropping constraints would
// broaden a Delete rule onto live objects.
func TestParseGcloudLifecycleRejectsUnknown(t *testing.T) {
	src := "rule:\n  - action:\n      type: Delete\n    condition:\n      doesNotExist: 1\n"
	_, err := ParseGcloudLifecycle(strings.NewReader(src))
	require.Error(t, err, "unknown condition fields must fail closed")
}

// Review Focus 5 / I7: a create losing a probe/create race (typed 409) must
// re-probe and converge the winner's bucket, not fail or stop at "created".
func TestBucketConvergeCreateRace(t *testing.T) {
	cfg := testBucketCfg()
	api := &fakeBucketAPI{
		createErr:        &googleapi.Error{Code: http.StatusConflict},
		attrsAfterCreate: &storage.BucketAttrs{VersioningEnabled: false}, // drifted winner
	}
	got, err := E.UnwrapError(ConvergeBucket(api, cfg, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action,
		"already-exists on create re-probes and converges the drift")
	require.Equal(t, []string{callGet, callCreate, callGet, callUpdate}, api.calls)
}

// Review Focus 2 pinned to the fake's statefulness: an immediate second run
// against the converged world reports unchanged.
func TestBucketConvergeSecondRunUnchanged(t *testing.T) {
	cfg := testBucketCfg()
	api := &fakeBucketAPI{attrsAfterCreate: &storage.BucketAttrs{
		VersioningEnabled: cfg.Versioning, Lifecycle: *cfg.Lifecycle,
	}}
	_, err := E.UnwrapError(ConvergeBucket(api, cfg, MutationMode{})())
	require.NoError(t, err) // created
	got, err := E.UnwrapError(ConvergeBucket(api, cfg, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action, "re-run after create is a no-op")
}
