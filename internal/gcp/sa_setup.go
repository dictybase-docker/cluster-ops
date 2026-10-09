package gcp

import (
	"context"
	"fmt"

	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	iamv1 "google.golang.org/api/iam/v1"
)

// SAAPI is the injected service-account surface. EnsureSA absorbs a typed
// AlreadyExists and reports which Action the converge took.
type SAAPI interface {
	EnsureSA(ctx context.Context, project, name, display string) (email string, action Action, err error)
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

type backupWork struct {
	sa    SAAPI
	iam   ProjectIAM
	keys  SAKeyAPI
	files KeyFiles
	cfg   BackupSAConfig
	mode  MutationMode
}

// SetupBackupSA composes the SA, role-binding and key converges in order
// SA → one binding per role → key. A failing binding stops the flow before
// the key is minted; a re-run reports every step unchanged.
func SetupBackupSA(
	sa SAAPI, iam ProjectIAM, keys SAKeyAPI, files KeyFiles,
	cfg BackupSAConfig, mode MutationMode,
) IOE.IOEither[error, []Result] {
	w := backupWork{sa: sa, iam: iam, keys: keys, files: files, cfg: cfg, mode: mode}
	return F.Pipe1(w.ensureSA(), IOE.Chain(w.bindRolesThenKey))
}

func (w backupWork) ensureSA() IOE.IOEither[error, saEnsured] {
	return F.Pipe1(
		IOE.TryCatchError(w.rawEnsureSA),
		IOE.MapLeft[saEnsured](w.wrapSAErr),
	)
}

func (w backupWork) rawEnsureSA() (saEnsured, error) {
	email, action, err := w.sa.EnsureSA(context.Background(), w.cfg.Project, w.cfg.SAName, w.cfg.DisplayName)
	return saEnsured{email: email, result: Result{Name: "serviceAccounts/" + email, Action: action}}, err
}

func (w backupWork) wrapSAErr(err error) error {
	return fmt.Errorf("ensure service account %s: %w", w.cfg.SAName, err)
}

// saEnsured is the ensured SA: its email plus the SA-level Result.
type saEnsured struct {
	email  string
	result Result
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
		ConvergeProjectIAMBinding(w.iam, w.cfg.Project, saMember(ensured.email), role, w.mode),
		IOE.Chain(w.afterRole(ensured, idx, acc)),
	)
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

// SetupCrossProjectReader composes SA (source project) → bucket objectViewer
// binding → key. Same ordering and stop-on-failure semantics as
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
		IOE.TryCatchError(w.rawEnsureSA),
		IOE.MapLeft[saEnsured](w.wrapSAErr),
	)
}

func (w readerWork) rawEnsureSA() (saEnsured, error) {
	email, action, err := w.sa.EnsureSA(
		context.Background(), w.cfg.SourceProject, w.cfg.SAName, w.cfg.DisplayName)
	return saEnsured{email: email, result: Result{Name: "serviceAccounts/" + email, Action: action}}, err
}

func (w readerWork) wrapSAErr(err error) error {
	return fmt.Errorf("ensure service account %s: %w", w.cfg.SAName, err)
}

func (w readerWork) bindBucketThenKey(ensured saEnsured) IOE.IOEither[error, []Result] {
	return F.Pipe1(
		ConvergeBucketIAMBinding(
			w.bucket, w.cfg.Bucket, saMember(ensured.email), w.cfg.Role, w.mode),
		IOE.Chain(w.afterBucket(ensured)),
	)
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

// IAMServiceAccountAPI adapts iam/v1 to SAAPI (typed AlreadyExists absorbed).
type IAMServiceAccountAPI struct {
	Service *iamv1.Service
}

func (a IAMServiceAccountAPI) EnsureSA(
	ctx context.Context, project, name, display string,
) (string, Action, error) {
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
		return email, ActionCreated, nil
	case IsAlreadyExists(err):
		return email, ActionUnchanged, nil
	default:
		return "", "", fmt.Errorf("create service account %s: %w", name, err)
	}
}
