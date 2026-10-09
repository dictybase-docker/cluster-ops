package gcp

import (
	"context"
	"net/http"
	"testing"

	iam "cloud.google.com/go/iam"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	expr "google.golang.org/genproto/googleapis/type/expr"
)

type fakeBucketIAM struct {
	policy *iam.Policy
	calls  []string
	sets   int
}

func (f *fakeBucketIAM) GetPolicy(context.Context, string) (*iam.Policy, error) {
	f.calls = append(f.calls, callGet)
	return f.policy, nil
}

func (f *fakeBucketIAM) SetPolicy(_ context.Context, _ string, p *iam.Policy) error {
	f.calls = append(f.calls, callSet)
	f.sets++
	f.policy = p
	return nil
}

type blockedBucketIAM struct {
	err  error
	sets int
}

func (b *blockedBucketIAM) GetPolicy(context.Context, string) (*iam.Policy, error) {
	return nil, b.err
}

func (b *blockedBucketIAM) SetPolicy(context.Context, string, *iam.Policy) error {
	b.sets++
	return nil
}

const testBucketName = "state-bucket"

func bucketPolicy(bindings ...*iampb.Binding) *iam.Policy {
	return &iam.Policy{InternalProto: &iampb.Policy{Etag: []byte(testEtag), Bindings: bindings}}
}

func plainBinding() *iampb.Binding {
	return &iampb.Binding{Role: testRole, Members: []string{testMember}}
}

func conditionalBinding() *iampb.Binding {
	return &iampb.Binding{
		Role:      testRole,
		Members:   []string{testMember},
		Condition: &expr.Expr{Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"},
	}
}

func TestBucketIAMBindingAddsMember(t *testing.T) {
	api := &fakeBucketIAM{policy: bucketPolicy()}
	got, err := E.UnwrapError(ConvergeBucketIAMBinding(api, testBucketName, testMember, testRole, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, []string{callGet, callSet}, api.calls)
	require.True(t, hasUnconditionalBinding(api.policy, testRole, testMember))
}

func TestBucketIAMBindingUnchanged(t *testing.T) {
	api := &fakeBucketIAM{policy: bucketPolicy(plainBinding())}
	got, err := E.UnwrapError(ConvergeBucketIAMBinding(api, testBucketName, testMember, testRole, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Equal(t, []string{callGet}, api.calls)
}

func TestBucketIAMBindingDryRun(t *testing.T) {
	api := &fakeBucketIAM{policy: bucketPolicy()}
	mode := MutationMode{DryRun: true}
	got, err := E.UnwrapError(ConvergeBucketIAMBinding(api, testBucketName, testMember, testRole, mode)())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Equal(t, []string{callGet}, api.calls)
	require.Zero(t, api.sets, "dry-run touches no mutation seam")
}

func TestBucketIAMBindingForbidden(t *testing.T) {
	blocked := &blockedBucketIAM{err: &googleapi.Error{Code: http.StatusForbidden}}
	_, err := E.UnwrapError(ConvergeBucketIAMBinding(blocked, testBucketName, testMember, testRole, MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, blocked.sets, "a forbidden read never writes")
}

// A conditional binding for the same role never satisfies or absorbs the
// plain member: the converge must add an unconditional binding.
func TestBucketIAMBindingConditionalSameRole(t *testing.T) {
	api := &fakeBucketIAM{policy: bucketPolicy(conditionalBinding())}
	got, err := E.UnwrapError(ConvergeBucketIAMBinding(api, testBucketName, testMember, testRole, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)

	var conditional *iampb.Binding
	for _, b := range api.policy.InternalProto.Bindings {
		if b.Condition != nil {
			conditional = b
		}
	}
	require.NotNil(t, conditional, "conditional binding preserved")
	require.Equal(t, []string{testMember}, conditional.Members,
		"conditional binding must not absorb the plain member")
	require.True(t, hasUnconditionalBinding(api.policy, testRole, testMember))

	again, err := E.UnwrapError(ConvergeBucketIAMBinding(api, testBucketName, testMember, testRole, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, again.Action, "immediate re-run is a no-op")
}
