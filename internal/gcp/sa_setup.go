package gcp

import (
	"context"
	"fmt"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	R "github.com/IBM/fp-go/v2/retry"
	iamv1 "google.golang.org/api/iam/v1"
)

// SAAPI is the injected service-account surface, split probe/create so
// dry-run NEVER reaches a mutation seam.
type SAAPI interface {
	// GetSA probes existence (email derived from project+name).
	GetSA(ctx context.Context, project, name string) (email string, exists bool, err error)
	// CreateSA creates the SA; a typed AlreadyExists is absorbed
	// (created=false) — the probe/create race is converge, not failure.
	CreateSA(ctx context.Context, project, name, display string) (email string, created bool, err error)
}

// BackupSAConfig describes the backup SA flow: SA + role bindings + key.
type BackupSAConfig struct {
	Project, SAName, DisplayName string
	Roles                        []string
	KeyFilePath, CredFile        string
}

// CrossProjectReaderConfig describes the source-project reader flow:
// SA in the source project + bucket objectViewer + key.
type CrossProjectReaderConfig struct {
	SourceProject, SAName, DisplayName string
	Bucket                             string
	Role                               string // "roles/storage.objectViewer"
	KeyFilePath, CredFile              string
}

// saPropagationPolicy retries transient binding failures after a FRESH SA
// create — the resource manager sees new service accounts asynchronously
// (the spec's iamPropagationPolicy shape; swappable in tests).
var saPropagationPolicy = iamPropagationPolicy

type backupWork struct {
	sa    SAAPI
	iam   ProjectIAM
	keys  SAKeyAPI
	files KeyFiles
	cfg   BackupSAConfig
	mode  MutationMode
}

// SetupBackupSA composes the SA, role-binding and key converges in order
// SA → one binding per role → key. DryRun reports intent and touches no
// mutation seam. A failing binding stops the flow before the key is minted.
func SetupBackupSA(
	sa SAAPI, iam ProjectIAM, keys SAKeyAPI, files KeyFiles,
	cfg BackupSAConfig, mode MutationMode,
) IOE.IOEither[error, []Result] {
	w := backupWork{sa: sa, iam: iam, keys: keys, files: files, cfg: cfg, mode: mode}
	return F.Pipe1(w.ensureSA(), IOE.Chain(w.bindRolesThenKey))
}

// saProbe is the GetSA outcome.
type saProbe struct {
	email  string
	exists bool
}

// saEnsured is the ensured SA: its email, whether it was just created (drives
// propagation retry), and the SA-level Result.
type saEnsured struct {
	email   string
	created bool
	result  Result
}

func (w backupWork) ensureSA() IOE.IOEither[error, saEnsured] {
	return F.Pipe1(
		IOE.TryCatchError(w.rawGetSA),
		IOE.Chain(w.fromProbeSA),
	)
}

func (w backupWork) rawGetSA() (saProbe, error) {
	email, exists, err := w.sa.GetSA(context.Background(), w.cfg.Project, w.cfg.SAName)
	return saProbe{email: email, exists: exists}, err
}

func (w backupWork) fromProbeSA(p saProbe) IOE.IOEither[error, saEnsured] {
	name := "serviceAccounts/" + p.email
	if p.exists {
		return IOE.Of[error](saEnsured{
			email:  p.email,
			result: Result{Name: name, Action: ActionUnchanged},
		})
	}
	if w.mode.DryRun {
		return IOE.Of[error](saEnsured{
			email:  p.email,
			result: Result{Name: name, Action: ActionDryRun},
		})
	}
	return F.Pipe1(
		IOE.TryCatchError(w.rawCreateSA),
		IOE.Map[error](w.toEnsured(name)),
	)
}

func (w backupWork) rawCreateSA() (bool, error) {
	_, created, err := w.sa.CreateSA(
		context.Background(), w.cfg.Project, w.cfg.SAName, w.cfg.DisplayName)
	return created, err
}

func (w backupWork) toEnsured(name string) func(bool) saEnsured {
	return func(created bool) saEnsured {
		action := ActionUnchanged
		if created {
			action = ActionCreated
		}
		return saEnsured{
			email:   w.email(),
			created: created,
			result:  Result{Name: name, Action: action},
		}
	}
}

func (w backupWork) email() string {
	return w.cfg.SAName + "@" + w.cfg.Project + ".iam.gserviceaccount.com"
}

func (w backupWork) bindRolesThenKey(ensured saEnsured) IOE.IOEither[error, []Result] {
	return w.bindRoles(ensured, 0, []Result{ensured.result})
}

// bindRoles converges each role binding in order (sequential: a failed bind
// stops the flow before later binds and the key mint).
func (w backupWork) bindRoles(ensured saEnsured, idx int, acc []Result) IOE.IOEither[error, []Result] {
	if idx >= len(w.cfg.Roles) {
		return w.convergeKey(ensured, acc)
	}
	role := w.cfg.Roles[idx]
	return F.Pipe1(
		w.bindOne(ensured, role),
		IOE.Chain(w.afterRole(ensured, idx, acc)),
	)
}

// bindOne converges one binding, wrapping it in the propagation retry when
// the SA was just created (resource-manager visibility lag).
func (w backupWork) bindOne(ensured saEnsured, role string) IOE.IOEither[error, Result] {
	op := ConvergeProjectIAMBinding(w.iam, w.cfg.Project, saMember(ensured.email), role, w.mode)
	if !ensured.created {
		return op
	}
	return IOE.Retrying(saPropagationPolicy, reattempt(op), retryablePropagation)
}

// reattempt is the propagation-retry Kleisli: re-force the same cold op.
func reattempt(op IOE.IOEither[error, Result]) func(R.RetryStatus) IOE.IOEither[error, Result] {
	return func(R.RetryStatus) IOE.IOEither[error, Result] {
		return op
	}
}

// retryablePropagation retries transient post-create failures; forbidden and
// not-found are permanent and pass through (named, no string matching).
func retryablePropagation(res E.Either[error, Result]) bool {
	return F.Pipe1(res, E.Fold(propagationCandidate, alwaysFalse))
}

func propagationCandidate(err error) bool {
	return !IsNotFound(err) && !IsPermissionDenied(err)
}

func (w backupWork) afterRole(ensured saEnsured, idx int, acc []Result) func(Result) IOE.IOEither[error, []Result] {
	return func(r Result) IOE.IOEither[error, []Result] {
		return w.bindRoles(ensured, idx+1, append(acc, r))
	}
}

func (w backupWork) convergeKey(ensured saEnsured, acc []Result) IOE.IOEither[error, []Result] {
	return F.Pipe1(
		ConvergeSAKey(w.keys, w.files, ensured.email, w.cfg.KeyFilePath, w.mode),
		IOE.Map[error](appendKey(acc)),
	)
}

func appendKey(acc []Result) func(Result) []Result {
	return func(r Result) []Result {
		return append(acc, r)
	}
}

// saMember maps an SA email to its IAM member form.
func saMember(email string) string {
	return "serviceAccount:" + email
}

type readerWork struct {
	sa     SAAPI
	bucket BucketIAM
	keys   SAKeyAPI
	files  KeyFiles
	cfg    CrossProjectReaderConfig
	mode   MutationMode
}

// SetupCrossProjectReader composes SA (source project) → bucket binding →
// key. Same ordering, dry-run and stop-on-failure semantics as
// SetupBackupSA.
func SetupCrossProjectReader(
	sa SAAPI, bucketIAM BucketIAM, keys SAKeyAPI, files KeyFiles,
	cfg CrossProjectReaderConfig, mode MutationMode,
) IOE.IOEither[error, []Result] {
	w := readerWork{sa: sa, bucket: bucketIAM, keys: keys, files: files, cfg: cfg, mode: mode}
	return F.Pipe1(w.ensureSA(), IOE.Chain(w.bindBucketThenKey))
}

func (w readerWork) ensureSA() IOE.IOEither[error, saEnsured] {
	return F.Pipe1(
		IOE.TryCatchError(w.rawGetSA),
		IOE.Chain(w.fromProbeSA),
	)
}

func (w readerWork) rawGetSA() (saProbe, error) {
	email, exists, err := w.sa.GetSA(context.Background(), w.cfg.SourceProject, w.cfg.SAName)
	return saProbe{email: email, exists: exists}, err
}

func (w readerWork) fromProbeSA(p saProbe) IOE.IOEither[error, saEnsured] {
	name := "serviceAccounts/" + p.email
	if p.exists {
		return IOE.Of[error](saEnsured{
			email:  p.email,
			result: Result{Name: name, Action: ActionUnchanged},
		})
	}
	if w.mode.DryRun {
		return IOE.Of[error](saEnsured{
			email:  p.email,
			result: Result{Name: name, Action: ActionDryRun},
		})
	}
	return F.Pipe1(
		IOE.TryCatchError(w.rawCreateSA),
		IOE.Map[error](w.toEnsured(name)),
	)
}

func (w readerWork) rawCreateSA() (bool, error) {
	_, created, err := w.sa.CreateSA(
		context.Background(), w.cfg.SourceProject, w.cfg.SAName, w.cfg.DisplayName)
	return created, err
}

func (w readerWork) toEnsured(name string) func(bool) saEnsured {
	return func(created bool) saEnsured {
		action := ActionUnchanged
		if created {
			action = ActionCreated
		}
		return saEnsured{
			email:   w.cfg.SAName + "@" + w.cfg.SourceProject + ".iam.gserviceaccount.com",
			created: created,
			result:  Result{Name: name, Action: action},
		}
	}
}

func (w readerWork) bindBucketThenKey(ensured saEnsured) IOE.IOEither[error, []Result] {
	op := ConvergeBucketIAMBinding(
		w.bucket, w.cfg.Bucket, saMember(ensured.email), w.cfg.Role, w.mode)
	if ensured.created {
		op = IOE.Retrying(saPropagationPolicy, reattempt(op), retryablePropagation)
	}
	return F.Pipe1(op, IOE.Chain(w.afterBucket(ensured)))
}

func (w readerWork) afterBucket(ensured saEnsured) func(Result) IOE.IOEither[error, []Result] {
	return func(r Result) IOE.IOEither[error, []Result] {
		return F.Pipe1(
			ConvergeSAKey(w.keys, w.files, ensured.email, w.cfg.KeyFilePath, w.mode),
			IOE.Map[error](appendKey([]Result{ensured.result, r})),
		)
	}
}

// ---- production adapter ----

// IAMServiceAccountAPI adapts iam/v1 to SAAPI (probe/create split; typed
// NotFound/AlreadyExists classified, never message-matched).
type IAMServiceAccountAPI struct {
	Service *iamv1.Service
}

func (a IAMServiceAccountAPI) GetSA(ctx context.Context, project, name string) (string, bool, error) {
	email := name + "@" + project + ".iam.gserviceaccount.com"
	_, err := a.Service.Projects.ServiceAccounts.
		Get("projects/-/serviceAccounts/" + email).Context(ctx).Do()
	switch {
	case err == nil:
		return email, true, nil
	case IsNotFound(err):
		return email, false, nil
	default:
		return email, false, fmt.Errorf("get service account %s: %w", name, err)
	}
}

func (a IAMServiceAccountAPI) CreateSA(
	ctx context.Context, project, name, display string,
) (string, bool, error) {
	email := name + "@" + project + ".iam.gserviceaccount.com"
	_, err := a.Service.Projects.ServiceAccounts.Create(
		"projects/"+project,
		&iamv1.CreateServiceAccountRequest{
			AccountId:      name,
			ServiceAccount: &iamv1.ServiceAccount{DisplayName: display},
		},
	).Context(ctx).Do()
	switch {
	case err == nil:
		return email, true, nil
	case IsAlreadyExists(err):
		return email, false, nil
	default:
		return email, false, fmt.Errorf("create service account %s: %w", name, err)
	}
}
