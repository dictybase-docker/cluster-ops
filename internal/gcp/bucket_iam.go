package gcp

import (
	"context"
	"fmt"
	"slices"

	iam "cloud.google.com/go/iam"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/storage"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	R "github.com/IBM/fp-go/v2/retry"
)

// BucketIAM is the injected bucket-policy surface (production:
// StorageBucketIAM wrapping client.Bucket(name).IAM() — *iam.Handle).
type BucketIAM interface {
	GetPolicy(ctx context.Context, bucket string) (*iam.Policy, error)
	SetPolicy(ctx context.Context, bucket string, p *iam.Policy) error
}

type bucketIAMWork struct {
	iam    BucketIAM
	bucket string
	member string
	role   string
	mode   MutationMode
}

// ConvergeBucketIAMBinding mirrors the project-binding cycle: get→modify→set
// under etag optimistic concurrency, retrying the WHOLE cycle on conflict.
// Only unconditional bindings grant the plain role — a conditional binding
// never satisfies or absorbs the member.
func ConvergeBucketIAMBinding(
	iamAPI BucketIAM, bucket, member, role string, mode MutationMode,
) IOE.IOEither[error, Result] {
	w := bucketIAMWork{iam: iamAPI, bucket: bucket, member: member, role: role, mode: mode}
	return IOE.Retrying(etagRetryPolicy, w.attemptBind, retryOnConflict)
}

// attemptBind is the retried Kleisli: one complete get→modify→set cycle.
func (w bucketIAMWork) attemptBind(R.RetryStatus) IOE.IOEither[error, Result] {
	return F.Pipe1(w.getPolicy(), IOE.Chain(w.modifyAndSet))
}

func (w bucketIAMWork) name() string {
	return fmt.Sprintf("gs://%s/roles/%s", w.bucket, w.role)
}

func (w bucketIAMWork) getPolicy() IOE.IOEither[error, *iam.Policy] {
	return F.Pipe1(
		IOE.TryCatchError(w.rawGetPolicy),
		IOE.MapLeft[*iam.Policy](w.wrapGetErr),
	)
}

func (w bucketIAMWork) rawGetPolicy() (*iam.Policy, error) {
	return w.iam.GetPolicy(context.Background(), w.bucket)
}

func (w bucketIAMWork) wrapGetErr(err error) error {
	return fmt.Errorf("get bucket IAM policy for %s: %w", w.bucket, err)
}

func (w bucketIAMWork) modifyAndSet(p *iam.Policy) IOE.IOEither[error, Result] {
	if hasUnconditionalBinding(p, w.role, w.member) {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionUnchanged})
	}
	if w.mode.DryRun {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionDryRun})
	}
	updated := withUnconditionalMember(p, w.role, w.member)
	return F.Pipe1(
		IOE.TryCatchError(func() (struct{}, error) {
			return struct{}{}, w.iam.SetPolicy(context.Background(), w.bucket, updated)
		}),
		IOE.Map[error](w.updatedResult),
	)
}

func (w bucketIAMWork) updatedResult(struct{}) Result {
	return Result{Name: w.name(), Action: ActionUpdated}
}

// hasUnconditionalBinding reports whether member holds role in an
// unconditional binding (a conditional binding does not grant the plain role).
func hasUnconditionalBinding(p *iam.Policy, role, member string) bool {
	if p == nil || p.InternalProto == nil {
		return false
	}
	for _, b := range p.InternalProto.Bindings {
		if b.Role == role && b.Condition == nil && slices.Contains(b.Members, member) {
			return true
		}
	}
	return false
}

// withUnconditionalMember returns a COPY of the policy with member added to
// the role's unconditional binding (created when absent). Conditional
// bindings are copied untouched — never mutated, never absorbing the member
// (iam.Policy.Add is role-index-only and would corrupt a conditional-first
// policy). The input is never mutated: a conflicted retry must see it pristine.
func withUnconditionalMember(p *iam.Policy, role, member string) *iam.Policy {
	out := &iam.Policy{InternalProto: &iampb.Policy{}}
	if p != nil && p.InternalProto != nil {
		out.InternalProto.Etag = p.InternalProto.Etag
		out.InternalProto.Version = p.InternalProto.Version
		found := false
		for _, b := range p.InternalProto.Bindings {
			nb := &iampb.Binding{
				Role:      b.Role,
				Members:   append([]string(nil), b.Members...),
				Condition: b.Condition,
			}
			if b.Role == role && b.Condition == nil {
				nb.Members = append(nb.Members, member)
				found = true
			}
			out.InternalProto.Bindings = append(out.InternalProto.Bindings, nb)
		}
		if found {
			return out
		}
	}
	out.InternalProto.Bindings = append(out.InternalProto.Bindings,
		&iampb.Binding{Role: role, Members: []string{member}})
	return out
}

// ---- production adapter ----

// StorageBucketIAM adapts *storage.Client to BucketIAM
// (client.Bucket(bucket).IAM()).
type StorageBucketIAM struct {
	Client *storage.Client
}

func (a StorageBucketIAM) GetPolicy(ctx context.Context, bucket string) (*iam.Policy, error) {
	return a.Client.Bucket(bucket).IAM().Policy(ctx)
}

func (a StorageBucketIAM) SetPolicy(ctx context.Context, bucket string, p *iam.Policy) error {
	return a.Client.Bucket(bucket).IAM().SetPolicy(ctx, p)
}
