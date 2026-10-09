package gcp

import (
	"context"
	"fmt"
	"slices"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	R "github.com/IBM/fp-go/v2/retry"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"
)

// ProjectIAM is the injected read-modify-write surface (etag on crm.Policy).
type ProjectIAM interface {
	GetPolicy(ctx context.Context, project string) (*crm.Policy, error)
	SetPolicy(ctx context.Context, project string, p *crm.Policy) error
}

// etagRetryPolicy retries conflicted cycles 3 times with exponential backoff
// (the iamPropagationPolicy shape from service_account.go).
var etagRetryPolicy = R.Monoid.Concat(
	R.LimitRetries(3),
	R.ExponentialBackoff(100*time.Millisecond),
)

type iamWork struct {
	iam     ProjectIAM
	project string
	member  string
	role    string
	mode    MutationMode
}

// ConvergeProjectIAMBinding runs the get→modify→set cycle under etag
// optimistic concurrency. On conflict IOE.Retrying re-runs the WHOLE cycle
// with a fresh etag; other errors pass through typed.
func ConvergeProjectIAMBinding(
	iam ProjectIAM, project, member, role string, mode MutationMode,
) IOE.IOEither[error, Result] {
	w := iamWork{iam: iam, project: project, member: member, role: role, mode: mode}
	return IOE.Retrying(etagRetryPolicy, w.attemptBind, retryOnConflict)
}

// attemptBind is the retried Kleisli: one complete get→modify→set cycle.
func (w iamWork) attemptBind(R.RetryStatus) IOE.IOEither[error, Result] {
	return w.bindOnce()
}

func (w iamWork) name() string {
	return fmt.Sprintf("projects/%s/roles/%s", w.project, w.role)
}

func (w iamWork) bindOnce() IOE.IOEither[error, Result] {
	return F.Pipe1(w.getPolicy(), IOE.Chain(w.modifyAndSet))
}

func (w iamWork) getPolicy() IOE.IOEither[error, *crm.Policy] {
	return F.Pipe1(
		IOE.TryCatchError(w.rawGetPolicy),
		IOE.MapLeft[*crm.Policy](w.wrapGetErr),
	)
}

func (w iamWork) rawGetPolicy() (*crm.Policy, error) {
	return w.iam.GetPolicy(context.Background(), w.project)
}

func (w iamWork) wrapGetErr(err error) error {
	return fmt.Errorf("get IAM policy for %s: %w", w.project, err)
}

func (w iamWork) modifyAndSet(p *crm.Policy) IOE.IOEither[error, Result] {
	updated := addMember(p, w.role, w.member)
	if updated == nil {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionUnchanged})
	}
	if w.mode.DryRun {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionDryRun})
	}
	return F.Pipe1(
		IOE.TryCatchError(func() (struct{}, error) {
			return struct{}{}, w.iam.SetPolicy(context.Background(), w.project, updated)
		}),
		IOE.Map[error](w.updatedResult),
	)
}

func (w iamWork) updatedResult(struct{}) Result {
	return Result{Name: w.name(), Action: ActionUpdated}
}

// addMember returns a COPY of the policy with member added to role, or nil
// when already bound. The input is never mutated — a conflicted retry must
// observe the pristine policy again.
func addMember(p *crm.Policy, role, member string) *crm.Policy {
	for _, b := range p.Bindings {
		if b.Role == role && slices.Contains(b.Members, member) {
			return nil
		}
	}
	out := *p // shallow copy: fresh Bindings below
	out.Bindings = make([]*crm.Binding, 0, len(p.Bindings)+1)
	found := false
	for _, b := range p.Bindings {
		nb := *b
		nb.Members = append([]string(nil), b.Members...)
		if nb.Role == role {
			nb.Members = append(nb.Members, member)
			found = true
		}
		out.Bindings = append(out.Bindings, &nb)
	}
	if !found {
		out.Bindings = append(out.Bindings, &crm.Binding{Role: role, Members: []string{member}})
	}
	return &out
}

// retryOnConflict is the IOE.Retrying check predicate (named per house rules).
func retryOnConflict(res E.Either[error, Result]) bool {
	return F.Pipe1(res, E.Fold(IsConflict, alwaysFalse))
}

func alwaysFalse(Result) bool { return false }

// ---- production adapter ----

// CRMProjectIAM adapts cloudresourcemanager to ProjectIAM.
type CRMProjectIAM struct {
	Service *crm.Service
}

func (a CRMProjectIAM) GetPolicy(ctx context.Context, project string) (*crm.Policy, error) {
	return a.Service.Projects.GetIamPolicy(project, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
}

func (a CRMProjectIAM) SetPolicy(ctx context.Context, project string, p *crm.Policy) error {
	_, err := a.Service.Projects.SetIamPolicy(project, &crm.SetIamPolicyRequest{Policy: p}).Context(ctx).Do()
	return err
}

// NewCRMService builds a cloudresourcemanager service from an explicit
// credential file ("" = application default credentials).
func NewCRMService(credFile string) IOE.IOEither[error, *crm.Service] {
	return IOE.TryCatchError(func() (*crm.Service, error) {
		if credFile == "" {
			return crm.NewService(context.Background())
		}
		return crm.NewService(
			context.Background(),
			option.WithAuthCredentialsFile(option.ServiceAccount, credFile),
		)
	})
}
