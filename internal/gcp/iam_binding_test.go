package gcp

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
)

type fakeProjectIAM struct {
	policy  *crm.Policy
	calls   []string
	setErrs []error   // popped per SetPolicy call (first non-nil is returned once)
	log     *[]string // optional shared sequence log
}

func (f *fakeProjectIAM) GetPolicy(context.Context, string) (*crm.Policy, error) {
	f.calls = append(f.calls, callGet)
	if f.log != nil {
		*f.log = append(*f.log, callGet)
	}
	return f.policy, nil
}

func (f *fakeProjectIAM) SetPolicy(_ context.Context, _ string, p *crm.Policy) error {
	f.calls = append(f.calls, callSet)
	if f.log != nil {
		*f.log = append(*f.log, callSet)
	}
	if len(f.setErrs) > 0 {
		err := f.setErrs[0]
		f.setErrs = f.setErrs[1:]
		if err != nil {
			return err
		}
	}
	f.policy = p
	return nil
}

// blockedIAM fails GetPolicy (a forbidden read, Review Focus 5 shape).
type blockedIAM struct {
	err  error
	sets int
}

func (b *blockedIAM) GetPolicy(context.Context, string) (*crm.Policy, error) {
	return nil, b.err
}

func (b *blockedIAM) SetPolicy(context.Context, string, *crm.Policy) error {
	b.sets++
	return nil
}

func testBindingPolicy(members ...string) *crm.Policy {
	return &crm.Policy{
		Etag:     testEtag,
		Bindings: []*crm.Binding{{Role: "roles/storage.admin", Members: members}},
	}
}

const (
	testProject = "proj-dcr"
	testEtag    = "etag-1"
	testMember  = "serviceAccount:sa@proj.iam.gserviceaccount.com"
	testRole    = "roles/storage.admin"
)

func TestProjectIAMBindingAddsMember(t *testing.T) {
	api := &fakeProjectIAM{policy: &crm.Policy{Etag: testEtag}}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, testProject, testMember, testRole, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, []string{callGet, callSet}, api.calls)
}

func TestProjectIAMBindingUnchanged(t *testing.T) {
	api := &fakeProjectIAM{policy: testBindingPolicy(testMember)}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, testProject, testMember, testRole, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Equal(t, []string{callGet}, api.calls)
}

// Review Focus 1: a 409 re-runs the WHOLE cycle (fresh get → fresh etag).
func TestProjectIAMBindingEtagRetry(t *testing.T) {
	api := &fakeProjectIAM{
		policy:  &crm.Policy{Etag: "stale"},
		setErrs: []error{&googleapi.Error{Code: http.StatusConflict}},
	}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, testProject, testMember, testRole, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, []string{callGet, callSet, callGet, callSet}, api.calls,
		"conflict retries the full get→modify→set cycle, not SetPolicy with the stale etag")
}

func TestProjectIAMBindingDryRun(t *testing.T) {
	api := &fakeProjectIAM{policy: &crm.Policy{Etag: testEtag}}
	mode := MutationMode{DryRun: true}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, testProject, testMember, testRole, mode)())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Equal(t, []string{callGet}, api.calls)
}

func TestProjectIAMBindingForbidden(t *testing.T) {
	blocked := &blockedIAM{err: &googleapi.Error{Code: http.StatusForbidden}}
	_, err := E.UnwrapError(ConvergeProjectIAMBinding(blocked, testProject, testMember, testRole, MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, blocked.sets, "a forbidden read never writes")
}

func TestRetryOnConflictPredicate(t *testing.T) {
	conflict := E.Left[Result](fmt.Errorf("etag: %w", &googleapi.Error{Code: http.StatusConflict}))
	require.True(t, retryOnConflict(conflict))
	ok := E.Of[error](Result{Action: ActionUpdated})
	require.False(t, retryOnConflict(ok))
}
