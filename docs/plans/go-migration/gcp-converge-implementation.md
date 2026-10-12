# GCP Converge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Commits:** each task ends with an explicit `git add` + `git commit -m` step (Conventional Commits). Never stage `docs/plans/**` (except an approved README status cell), `.superpowers/**`, ledgers, `.env*` or `.tool-versions*` scratch files.

**Goal:** Migrate the remaining GCP resource mutations (bucket lifecycle, IAM bindings, SA setup flows) from `gcloud`/`gsutil` shell to idempotent Go converge functions: describe-then-create/update, typed error classification (NotFound/AlreadyExists/PermissionDenied/Conflict), IAM etag concurrency with full-cycle retry, SA-key mint idempotency, and a `MutationMode{DryRun, Confirmed}` contract where dry-run never touches a mutation seam.

**Architecture:** Resource-specific converge functions in `internal/gcp` (the proven `kms.go` `ensure*` shape — NOT the spec's generic `ConvergeResource[T]` sketch, which violates the house anti-pattern rules). Each converge takes an injected API seam (interface per resource) so unit tests run without GCP; production adapters wrap the storage / `cloud.google.com/go/iam` / cloudresourcemanager / iam SDK surfaces built from explicit credentials. CLI subcommands extend the EXISTING trees (`bucket`, new `iam`, `sa`); justfile recipes become thin wrappers keeping preflight-before-mutation order; destructive deletes keep their interactive confirmation in the wrapper and pipe it to the CLI as `--confirm yes`.

**Tech Stack:** Go 1.26, fp-go v2 (house stack; real `IOE.Retrying`/`R.Monoid` APIs from v2.3.146), `cloud.google.com/go/storage`, `cloud.google.com/go/iam`, `google.golang.org/api/cloudresourcemanager/v1`, `google.golang.org/api/iam/v1`, testify/require, bash contract tests, just.

**Spec:** [`gcp-converge.md`](gcp-converge.md) — authority for converge semantics (describe-then-create, no `|| true`, dry-run, key-lifecycle rules, secrets handling). Its code blocks are sketches; its CLI sketch (`cluster-ops gcp …`) is superseded by the audit below; its generic `ConvergeResource[T]` is rejected (Design deltas).

**Status:** Ready to execute — Verified: replayed via execution on `b548c16..8948bfa` (2026-10-09): all 9 tasks + one review fix pass executed task-by-task (TDD, RED→GREEN recorded), 428 tests/1 skipped green, `just check` exit=0. Every deviation became a `Ruling:` line in the run ledger.

---

## Staleness audit (supersedes the spec's inventory)

Already done — **do not re-implement**:

| Item | Evidence |
|---|---|
| Phase 1 foundation (`internal/registry`, `internal/preflight`, `internal/waitfor`) | landed (phase-1 + review-fixes work) |
| `go run ./cmd/cluster-ops/` invocation migration | every migrated wrapper uses `go run` |
| `create-state-bucket` | `cluster-ops bucket create` → `gcp.CreateKopsStateBucket` (probe + versioning + soft-delete + lifecycle) |
| KMS keyring/key converge | `internal/gcp/kms.go` `keyringKeyWork` (fp-go probe-create pattern) |
| machine-image wait | `internal/gcp/machine_image.go` (create/delete still shell — spec defers image ops) |

Remaining — this plan's scope (recipe lines re-derived; the spec's line numbers are stale; just module namespaces verified via `just --list`):

| Recipe | Location | Mutation | Task |
|---|---|---|---|
| `gcp-pulumi pulumi-gcs-setup` | `pulumi.justfile:21` | bucket create/update + versioning + lifecycle | 3 |
| `gcp-cluster delete-state-bucket` | `cluster.justfile:618` | **destructive** object/version purge + bucket delete | 4 |
| `gcp-role add-role-to-sa` | `role.justfile:37` | project IAM bind | 5 |
| `gcp-role assign-roles-to-sa` | `role.justfile:69` | batch project IAM bind | 5 |
| bucket IAM binds | `arangodb.justfile:1895` (`grant-source-bucket-reader`), `postgres.justfile:225` (`configure-source`) | bucket IAM bind | 6 |
| `arangodb setup-backup-sa`, `redis setup-backup-sa` | `arangodb.justfile:1552`, `redis.justfile:270` | SA + 1 role + key | 7, 8 |
| `postgres configure-backup` | `postgres.justfile:106` (spec's `setup-backup-sa` renamed) | SA + 2 roles + key | 7, 8 |
| `postgres configure-source` | `postgres.justfile:225` (spec's `setup-source-reader` renamed; cross-project via `src_gcloud`) | SA + bucket IAM + key | 7, 8 |
| `gcp-cluster sa-accounts-setup` | `cluster.justfile:76` | SA + key | 8 |

Design deltas vs the spec (decided here):
- CLI under existing trees: `cluster-ops bucket converge|delete`, new `cluster-ops iam add-binding|add-bucket-binding`, `cluster-ops sa setup-backup|setup-reader|create-key`. No `gcp` supercommand.
- NO generic `ConvergeResource[T]` — resource-specific converge functions with injected seams (house rule: no closure factories).
- Uniform `MutationMode{DryRun, Confirmed}`: `DryRun` skips every mutation seam and reports intent (`ActionDryRun`); destructive flows additionally require `Confirmed` (otherwise error before any mutation).
- IAM conflict handling retries the WHOLE get→modify→set cycle (fresh etag per attempt) via the real `IOE.Retrying(policy, action, check)` signature — the spec's `IOE.Retrying(policy, func(error) bool, op)` sketch does not compile.
- `create-hmac-key` stays shell and intentionally non-convergent (minting a new key each run is its purpose).
- gcloud config/auth UX stays shell (spec non-goal).

## Global Constraints

- fp-go house stack only; `F.Pipe`; no inside-out calls; no nested closure layers (named semantic steps — the `kms.go` `ensure*` shape); `IOE.TryCatchError` holds raw effects, wrapping in `MapLeft`.
- Every converge is **idempotent**: re-run yields `unchanged`. Describe-then-create/update; never `create || echo "already exists"`; never `|| true` on a mutation.
- Typed error classification only (`errors.As` on `*googleapi.Error`, grpc `status.FromError`): `IsNotFound`/`IsAlreadyExists`/`IsPermissionDenied`/`IsConflict`. Never message matching. PermissionDenied is reported as itself — never as missing/already-exists.
- `MutationMode` contract: `DryRun` ⇒ no mutation seam is called (tested); destructive flows without `Confirmed` error before any mutation. All validation (object counts, identity binding, flags) precedes the first mutating call.
- Identity binding explicit (registry entry / flags / cluster-env): never ambient `$GOOGLE_CLOUD_PROJECT`.
- Secrets (SA key material) never in argv, logs, or Result details; key IDs and file paths may be logged (audit). Key files written 0o600 by a step outside the cloud seam.
- Output contract: converge commands print one `created|updated|unchanged|dry-run` action line; wrapper-embedded checks keep `preflight.Print` wording.
- Every user-supplied justfile interpolation landing in shell renders through `quote()` (bare `{{ quote(x) }}`).
- Contract tests mock `go` AND `pulumi` on PATH (shared argv call log, `MOCK_GO_EXIT`) and assert converge-before-mutation order; behavior assertions live in Go tests behind seams.
- Gate before done: `just check` zero failures + the Final gate commands (Task 9 step 5).

## Review Focus

Input classes the tasks' tests do not exercise — reviewers check each deliberately; each pinned to a task step:
1. Concurrent etag conflict (409) mid read-modify-write: the retried action re-runs the FULL get→modify→set cycle with a fresh etag — never a lost update (Task 5 `TestProjectIAMBindingEtagRetry`).
2. Bucket exists with drift (versioning off / lifecycle stale): `updated`, immediate re-run reports `unchanged` (Task 3 `TestBucketConvergeUpdatesDrift`).
3. Key file present but its key deleted upstream: re-mint exactly once (Task 7 `TestSAKeyRemintsWhenKeyGone`).
4. Destructive delete of a versioned bucket purges noncurrent versions before the bucket delete; dry-run calls NO mutation seam; the confirm gate precedes every mutation (Task 4 `TestBucketDeletePreflightOrder`, `TestBucketDeleteDryRunDeletesNothing`, contract §4).
5. `PermissionDenied` on create (403) is classified and named — never treated as NotFound/AlreadyExists (Task 2 `TestClassifyPermissionDenied`, Task 3 `TestBucketConvergeForbidden`).

---

## Task 1: Baseline

**Files:** none

- [ ] **Step 1:** `git log --oneline -1` → current head of `terratest-preflight` (after `b548c16`) without these changes.
- [ ] **Step 2:** `go build ./... && gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → `DONE 379 tests, 1 skipped`.
- [ ] **Step 3:** Scope check: `rg -c 'gcloud |gsutil ' just_modules/*.justfile` matches the audit; `rg -n 'func bucketCommand' cmd/cluster-ops/main.go` → the `bucket` tree exists.

---

## Task 2: Shared classifier + converge result + MutationMode

**Files:** Create `internal/gcp/converge.go`, `internal/gcp/converge_test.go`

**Interfaces (exact):**
```go
package gcp

// Action is what a converge did.
type Action string

const (
	ActionCreated   Action = "created"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionDryRun    Action = "dry-run"
)

// MutationMode governs mutations: DryRun skips every mutation seam and
// reports intent; Confirmed gates destructive flows.
type MutationMode struct {
	DryRun    bool
	Confirmed bool
}

// Result reports one converge outcome.
type Result struct {
	Name   string // resource identifier: "gs://bucket", "projects/p/roles/x", "projects/p/serviceAccounts/e"
	Action Action
}

// Typed classification — errors.As on *googleapi.Error, grpc status codes
// otherwise. Never rendered-message matching.
func IsNotFound(err error) bool
func IsAlreadyExists(err error) bool
func IsPermissionDenied(err error) bool
func IsConflict(err error) bool // 409 / codes.Aborted
```

- [ ] **Step 1: Write the failing tests** (`converge_test.go`):

```go
package gcp

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClassifyGoogleAPI(t *testing.T) {
	require.True(t, IsNotFound(&googleapi.Error{Code: http.StatusNotFound}))
	require.True(t, IsAlreadyExists(&googleapi.Error{Code: http.StatusConflict}))
	require.True(t, IsPermissionDenied(&googleapi.Error{Code: http.StatusForbidden}))
	require.True(t, IsConflict(&googleapi.Error{Code: http.StatusConflict}))
	require.False(t, IsNotFound(&googleapi.Error{Code: http.StatusForbidden}))
}

func TestClassifyGRPCStatus(t *testing.T) {
	require.True(t, IsNotFound(status.Error(codes.NotFound, "nope")))
	require.True(t, IsAlreadyExists(status.Error(codes.AlreadyExists, "exists")))
	require.True(t, IsPermissionDenied(status.Error(codes.PermissionDenied, "denied")))
	require.True(t, IsConflict(status.Error(codes.Aborted, "etag")))
	require.False(t, IsAlreadyExists(status.Error(codes.NotFound, "nope")))
}

func TestClassifyWrappedAndUnknown(t *testing.T) {
	wrapped := fmt.Errorf("create bucket: %w", &googleapi.Error{Code: http.StatusForbidden})
	require.True(t, IsPermissionDenied(wrapped), "classification unwraps")
	require.False(t, IsNotFound(errors.New("connection refused")))
}

func TestMutationModeDefaults(t *testing.T) {
	require.False(t, (MutationMode{}).DryRun, "default runs mutations")
}
```

- [ ] **Step 2: Run → Expected build failure `undefined: IsNotFound`.**
- [ ] **Step 3: Implement** per the block; document `IsConflict` = 409/`codes.Aborted`.
- [ ] **Step 4: Tests pass** (`DONE 4 tests`).
- [ ] **Step 5: Commit.**
  `git add internal/gcp/converge.go internal/gcp/converge_test.go`
  `git commit -m "feat(gcp): typed converge result, error classification, mutation mode"`

---

## Task 3: Bucket converge → `bucket converge`

**Files:** Create `internal/gcp/bucket_converge.go`, `internal/gcp/bucket_converge_test.go`; modify `cmd/cluster-ops/main.go` (extend `bucketCommand()`), `just_modules/pulumi.justfile:21-70`, `docs/reference/pulumi/backend-bootstrap.md`

Behavior source: `pulumi.justfile:21-70` — create when missing; ensure versioning; apply lifecycle when given; `pulumi login` stays in the wrapper.

**Interfaces (exact):**
```go
// BucketAPI is the injected GCS surface (production: storageBucketAPI over *storage.Client).
type BucketAPI interface {
	Get(ctx context.Context, name string) (*storage.BucketAttrs, error) // typed NotFound via googleapi
	Create(ctx context.Context, cfg BucketConfig) error
	Update(ctx context.Context, name string, versioning bool, lifecycle storage.Lifecycle) error
}

type BucketConfig struct {
	Name, ProjectID, Location string
	Versioning                bool
	Lifecycle                 storage.Lifecycle
	CredFile                  string // "" = application default credentials
}

// ConvergeBucket: describe → create | update-if-drift | unchanged.
// DryRun reports the intended action and calls no mutation seam.
func ConvergeBucket(api BucketAPI, cfg BucketConfig, mode MutationMode) IOE.IOEither[error, Result]

// lifecycleEqual compares lifecycle rules deterministically via canonical
// JSON bytes (storage.LifecycleRule is not safely ==/slices.Equal-able).
func lifecycleEqual(a, b storage.Lifecycle) bool
```

- [ ] **Step 1: Write the failing tests** (`bucket_converge_test.go`; fake records every call):

```go
type fakeBucketAPI struct {
	attrs    *storage.BucketAttrs // nil → Get returns googleapi 404
	getErr   error
	calls    []string // "get" | "create" | "update"
}

func (f *fakeBucketAPI) Get(context.Context, string) (*storage.BucketAttrs, error) {
	f.calls = append(f.calls, "get")
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.attrs == nil {
		return nil, &googleapi.Error{Code: http.StatusNotFound}
	}
	return f.attrs, nil
}

func (f *fakeBucketAPI) Create(context.Context, BucketConfig) error {
	f.calls = append(f.calls, "create")
	return nil
}

func (f *fakeBucketAPI) Update(context.Context, string, bool, storage.Lifecycle) error {
	f.calls = append(f.calls, "update")
	return nil
}

func testBucketCfg() BucketConfig {
	return BucketConfig{
		Name: "state-bucket", ProjectID: "proj-dcr", Location: "US",
		Versioning: true,
		Lifecycle:  storage.Lifecycle{Rules: []storage.LifecycleRule{{Action: storage.LifecycleAction{Type: "Delete"}, Condition: storage.LifecycleCondition{AgeInDays: 30}}}},
	}
}

func TestBucketConvergeCreatesMissing(t *testing.T) {
	api := &fakeBucketAPI{}
	got, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionCreated, got.Action)
	require.Equal(t, []string{"get", "create"}, api.calls)
}

func TestBucketConvergeUpdatesDrift(t *testing.T) {
	api := &fakeBucketAPI{attrs: &storage.BucketAttrs{VersioningEnabled: false}}
	got, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, []string{"get", "update"}, api.calls)
}

func TestBucketConvergeUnchanged(t *testing.T) {
	cfg := testBucketCfg()
	api := &fakeBucketAPI{attrs: &storage.BucketAttrs{
		VersioningEnabled: true, Lifecycle: cfg.Lifecycle,
	}}
	got, err := E.UnwrapError(ConvergeBucket(api, cfg, MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Equal(t, []string{"get"}, api.calls)
}

func TestBucketConvergeDryRunMutatesNothing(t *testing.T) {
	api := &fakeBucketAPI{}
	got, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{DryRun: true})())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Equal(t, []string{"get"}, api.calls, "dry-run touches no mutation seam")
}

func TestBucketConvergeForbidden(t *testing.T) {
	api := &fakeBucketAPI{getErr: &googleapi.Error{Code: http.StatusForbidden}}
	_, err := E.UnwrapError(ConvergeBucket(api, testBucketCfg(), MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err), "403 must not read as missing")
	require.Equal(t, []string{"get"}, api.calls)
}

func TestLifecycleEqual(t *testing.T) {
	a := storage.Lifecycle{Rules: []storage.LifecycleRule{{Action: storage.LifecycleAction{Type: "Delete"}, Condition: storage.LifecycleCondition{AgeInDays: 30}}}}
	b := storage.Lifecycle{Rules: []storage.LifecycleRule{{Action: storage.LifecycleAction{Type: "Delete"}, Condition: storage.LifecycleCondition{AgeInDays: 30}}}}
	require.True(t, lifecycleEqual(a, b))
	require.False(t, lifecycleEqual(a, storage.Lifecycle{}))
}
```

- [ ] **Step 2: Run → Expected build failure `undefined: BucketAPI` / `undefined: testBucketCfg` (as a whole: `ConvergeBucket`, `lifecycleEqual`).**
- [ ] **Step 3: Implement** — named steps in the `kms.go` shape (`probeBucket`, `bucketFromProbe`, `createBucket`, `updateBucketIfDrift`); raw SDK calls in `TryCatchError`; classification via Task 2. `lifecycleEqual` = canonical-JSON byte comparison (`json.Marshal` both, `bytes.Equal`; document that field order is structurally stable). Production `storageBucketAPI(*storage.Client)` adapter + `newStorageClientFrom(credFile)` (mirrors `pulumi.go`'s `newStorageClient`).
- [ ] **Step 4: Tests pass** (`DONE 6 tests`).
- [ ] **Step 5: CLI.** `bucketCommand()` gains `{Name: "converge", Action: bucketConvergeAction}` — flags `--project` (required), `--bucket` (required), `--location` (default `US`), `--versioning` (bool, default true), `--lifecycle-file`, `--cred-file`, `--dry-run`. `--dry-run` prints `[DRY RUN] <name> <action>` to stderr + a one-line JSON summary to stdout.
- [ ] **Step 6: Wrapper.** `pulumi-gcs-setup` body → `go run ./cmd/cluster-ops bucket converge --bucket {{ quote(gcs_bucket) }} --project "$project_id" --location {{ quote(location) }} …` then keeps its `pulumi login "gs://…"` line and echo tails; lifecycle-file passed when set. Old body renamed `pulumi-gcs-setup-shell`.
- [ ] **Step 7: Lint + docs.** `golangci-lint run ./internal/gcp/... ./cmd/cluster-ops/...` → `0 issues.`; `docs/reference/pulumi/backend-bootstrap.md` flag table → `--help`.
- [ ] **Step 8: Commit.**
  `git add internal/gcp/bucket_converge.go internal/gcp/bucket_converge_test.go cmd/cluster-ops/main.go just_modules/pulumi.justfile docs/reference/pulumi/backend-bootstrap.md`
  `git commit -m "feat(gcp): converge GCS bucket versioning and lifecycle"`

---

## Task 4: Destructive bucket delete → `bucket delete`

**Files:** Create `internal/gcp/bucket_delete.go`, `internal/gcp/bucket_delete_test.go`; modify `cmd/cluster-ops/main.go`, `just_modules/cluster.justfile:618-690`, `docs/reference/kops/` teardown doc

Behavior source: `cluster.justfile:618+` — dry-run lists sample + object count; `--confirm yes` gates deletion; the interactive `Type 'delete'` prompt stays in the wrapper (it pipes `--confirm yes` onward only after the operator types it). A versioned bucket must purge objects AND noncurrent versions before the bucket delete.

**Interfaces (exact):**
```go
// BucketDeleteAPI enumerates and removes (injected; production wraps the
// storage client, versions included).
type BucketDeleteAPI interface {
	CountObjects(ctx context.Context, name string) (int64, error) // objects + noncurrent versions
	DeleteObjects(ctx context.Context, name string) error          // recursive purge incl. versions
	DeleteBucket(ctx context.Context, name string) error
}

// DeleteBucketState validates first (count + Confirmed), then purges objects
// and deletes the bucket once. DryRun reports intent and calls no mutation
// seam; missing Confirmed errors before any mutation.
func DeleteBucketState(api BucketDeleteAPI, name string, mode MutationMode) IOE.IOEither[error, Result]
```

- [ ] **Step 1: Write the failing tests** (`bucket_delete_test.go`; fake records calls):

```go
type fakeDeleteAPI struct {
	calls []string
	count int64
}

func (f *fakeDeleteAPI) CountObjects(context.Context, string) (int64, error) {
	f.calls = append(f.calls, "count")
	return f.count, nil
}
func (f *fakeDeleteAPI) DeleteObjects(context.Context, string) error {
	f.calls = append(f.calls, "purge")
	return nil
}
func (f *fakeDeleteAPI) DeleteBucket(context.Context, string) error {
	f.calls = append(f.calls, "delete-bucket")
	return nil
}

func TestBucketDeleteDryRunDeletesNothing(t *testing.T) {
	api := &fakeDeleteAPI{count: 42}
	got, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{DryRun: true})())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Equal(t, []string{"count"}, api.calls, "dry-run touches no mutation seam")
}

func TestBucketDeleteRefusesWithoutConfirm(t *testing.T) {
	api := &fakeDeleteAPI{count: 42}
	_, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{})())
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmation required")
	require.Equal(t, []string{"count"}, api.calls, "refusal precedes every mutation")
}

func TestBucketDeletePreflightOrder(t *testing.T) {
	api := &fakeDeleteAPI{count: 42}
	got, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{Confirmed: true})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	// count → purge (objects+versions) → delete-bucket: strict order.
	require.Equal(t, []string{"count", "purge", "delete-bucket"}, api.calls)
}

func TestBucketDeleteEmptyBucketSkipsPurge(t *testing.T) {
	api := &fakeDeleteAPI{count: 0}
	_, err := E.UnwrapError(DeleteBucketState(api, "state-bucket", MutationMode{Confirmed: true})())
	require.NoError(t, err)
	require.Equal(t, []string{"count", "delete-bucket"}, api.calls)
}
```

- [ ] **Step 2: Run → Expected build failure `undefined: DeleteBucketState`.**
- [ ] **Step 3: Implement** — named `preflightDelete` (count + gate) then `purgeAndDelete` (skips purge when count == 0); validate-before-mutation enforced structurally. Production `storageDeleteAPI` adapter (objects + versions via `iterator` over `Objects(..., Versions: true)`).
- [ ] **Step 4: Tests pass** (`DONE 4 tests`).
- [ ] **Step 5: CLI** `bucket delete --bucket --confirm yes --dry-run --cred-file`; wrapper `delete-state-bucket` keeps its echo/preview/prompt UX and passes `--confirm yes` only after the operator types `delete`.
- [ ] **Step 6: Lint + docs.**
- [ ] **Step 7: Commit.**
  `git add internal/gcp/bucket_delete.go internal/gcp/bucket_delete_test.go cmd/cluster-ops/main.go just_modules/cluster.justfile docs/reference/kops/`
  `git commit -m "feat(gcp): converge-safe state bucket deletion with preflight"`

---

## Task 5: Project IAM binding converge → `iam add-binding`

**Files:** Create `internal/gcp/iam_binding.go`, `internal/gcp/iam_binding_test.go`; modify `cmd/cluster-ops/main.go` (new `iamCommand()`), `just_modules/role.justfile:37-120`, `docs/reference/` role doc

Behavior sources: `role.justfile:37` (`add-role-to-sa`), `:69` (`assign-roles-to-sa`, roles from the existing `readRolesFromFile`).

**Interfaces (exact):**
```go
import (
	R "github.com/IBM/fp-go/v2/retry"
	crm "google.golang.org/api/cloudresourcemanager/v1"
)

// ProjectIAM is the injected read-modify-write surface (etag on crm.Policy).
type ProjectIAM interface {
	GetPolicy(ctx context.Context, project string) (*crm.Policy, error)
	SetPolicy(ctx context.Context, project string, p *crm.Policy) error
}

// ConvergeProjectIAMBinding runs the get→modify→set cycle under etag
// optimistic concurrency. On conflict (409/Aborted) IOE.Retrying re-runs the
// WHOLE cycle with a fresh etag (3 attempts, 100ms exponential backoff —
// the iamPropagationPolicy shape from service_account.go:180).
func ConvergeProjectIAMBinding(iam ProjectIAM, project, member, role string, mode MutationMode) IOE.IOEither[error, Result]

// attemptBind is the retried Kleisli: one complete get→modify→set cycle.
func attemptBind(iam ProjectIAM, project, member, role string, mode MutationMode) func(R.RetryStatus) IOE.IOEither[error, Result]

// bindOnce = get → find-or-create binding → add member → set (or dry-run short-circuit after get).
func bindOnce(iam ProjectIAM, project, member, role string, mode MutationMode) IOE.IOEither[error, Result]

// retryOnConflict is the IOE.Retrying check predicate (named, per house rules).
func retryOnConflict(res E.Either[error, Result]) bool

// alwaysFalse is the non-conflict arm for E.Fold (named — no inline closure).
func alwaysFalse(Result) bool
```

Real fp-go retry wiring (v2.3.146 `ioeither/retry.go:30` — `Retrying(policy, action Kleisli[error, R.RetryStatus, A], check Predicate[Either[error, A]])`):
```go
var etagRetryPolicy = R.Monoid.Concat(
	R.LimitRetries(3),
	R.ExponentialBackoff(100*time.Millisecond),
)

func ConvergeProjectIAMBinding(iam ProjectIAM, project, member, role string, mode MutationMode) IOE.IOEither[error, Result] {
	return IOE.Retrying[error, Result](etagRetryPolicy, attemptBind(iam, project, member, role, mode), retryOnConflict)
}

func retryOnConflict(res E.Either[error, Result]) bool {
	return F.Pipe1(res, E.Fold(IsConflict, alwaysFalse))
}
```

- [ ] **Step 1: Write the failing tests** (`iam_binding_test.go`; fake records calls, holds the policy):

```go
type fakeProjectIAM struct {
	policy  *crm.Policy
	calls   []string
	setErrs []error // popped per SetPolicy call (first non-nil is returned once)
}

func (f *fakeProjectIAM) GetPolicy(context.Context, string) (*crm.Policy, error) {
	f.calls = append(f.calls, "get")
	return f.policy, nil
}

func (f *fakeProjectIAM) SetPolicy(_ context.Context, _ string, p *crm.Policy) error {
	f.calls = append(f.calls, "set")
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

func testBindingPolicy(members ...string) *crm.Policy {
	p := &crm.Policy{Etag: "etag-1", Bindings: []*crm.Binding{{Role: "roles/storage.admin", Members: members}}}
	return p
}

func TestProjectIAMBindingAddsMember(t *testing.T) {
	api := &fakeProjectIAM{policy: &crm.Policy{Etag: "etag-1"}}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, "proj-dcr", "serviceAccount:sa@proj.iam.gserviceaccount.com", "roles/storage.admin", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, []string{"get", "set"}, api.calls)
}

func TestProjectIAMBindingUnchanged(t *testing.T) {
	api := &fakeProjectIAM{policy: testBindingPolicy("serviceAccount:sa@proj.iam.gserviceaccount.com")}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, "proj-dcr", "serviceAccount:sa@proj.iam.gserviceaccount.com", "roles/storage.admin", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Equal(t, []string{"get"}, api.calls)
}

// Review Focus 1: a 409 re-runs the WHOLE cycle (fresh get → fresh etag).
func TestProjectIAMBindingEtagRetry(t *testing.T) {
	api := &fakeProjectIAM{
		policy:  &crm.Policy{Etag: "stale"},
		setErrs: []error{&googleapi.Error{Code: http.StatusConflict}},
	}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, "proj-dcr", "serviceAccount:sa@proj.iam.gserviceaccount.com", "roles/storage.admin", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, []string{"get", "set", "get", "set"}, api.calls,
		"conflict retries the full get→modify→set cycle, not SetPolicy with the stale etag")
}

func TestProjectIAMBindingDryRun(t *testing.T) {
	api := &fakeProjectIAM{policy: &crm.Policy{Etag: "etag-1"}}
	got, err := E.UnwrapError(ConvergeProjectIAMBinding(api, "proj-dcr", "serviceAccount:sa@proj.iam.gserviceaccount.com", "roles/storage.admin", MutationMode{DryRun: true})())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Equal(t, []string{"get"}, api.calls)
}

func TestProjectIAMBindingForbidden(t *testing.T) {
	api := &fakeProjectIAM{policy: &crm.Policy{}}
	api.setErrs = nil
	// GetPolicy 403 via a dedicated fake variant:
	blocked := &blockedIAM{err: &googleapi.Error{Code: http.StatusForbidden}}
	_, err := E.UnwrapError(ConvergeProjectIAMBinding(blocked, "proj-dcr", "serviceAccount:sa@x", "roles/storage.admin", MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
}
// (blockedIAM is a ProjectIAM whose GetPolicy returns the error; SetPolicy records.)
```

- [ ] **Step 2: Run → Expected build failure `undefined: ProjectIAM`.**
- [ ] **Step 3: Implement** per the pinned block (production `crmProjectIAM` adapter over `cloudresourcemanager/v1` `Projects.GetIamPolicy/SetIamPolicy`; members as `"serviceAccount:<email>"` strings — no transformation).
- [ ] **Step 4: Tests pass** (`DONE 5 tests`).
- [ ] **Step 5: CLI** `iam add-binding --project --member --role --cred-file --dry-run` + `iamCommand()` tree. Wrapper `add-role-to-sa` → `go run ./cmd/cluster-ops iam add-binding --project "$project_id" --member "serviceAccount:$sa_email" --role {{ quote(role) }} …`, keeping its verify echo; `assign-roles-to-sa` loops `readRolesFromFile` output into the same subcommand (loop stays shell).
- [ ] **Step 6: Lint + docs.**
- [ ] **Step 7: Commit.**
  `git add internal/gcp/iam_binding.go internal/gcp/iam_binding_test.go cmd/cluster-ops/main.go just_modules/role.justfile docs/reference/`
  `git commit -m "feat(gcp): converge project IAM bindings with full-cycle etag retry"`

---

## Task 6: Bucket IAM binding converge → `iam add-bucket-binding`

**Files:** Create `internal/gcp/bucket_iam.go`, `internal/gcp/bucket_iam_test.go`; modify `cmd/cluster-ops/main.go`, `just_modules/arangodb.justfile:1895-1960`, `just_modules/postgres.justfile:225+`, `docs/reference/` docs

**Interfaces (exact):** the bucket IAM surface is `cloud.google.com/go/iam` (`BucketHandle.IAM()`), NOT `storage.Policy`:
```go
import iam "cloud.google.com/go/iam"

// BucketIAM is the injected bucket-policy surface (production:
// storageBucketIAM wrapping client.Bucket(name).IAM() — iam.Handle).
type BucketIAM interface {
	GetPolicy(ctx context.Context, bucket string) (*iam.Policy, error)
	SetPolicy(ctx context.Context, bucket string, p *iam.Policy) error
}

// ConvergeBucketIAMBinding mirrors Task 5's cycle: get→modify→set with
// iam.Policy(Role(bindings.RoleName(role)).Add(member)), retrying the full
// cycle on conflict via etagRetryPolicy/retryOnConflict. Roles/members map:
// role "roles/storage.objectViewer" → bindings.RoleName; member
// "serviceAccount:<email>" → iam.ServiceAccount(email) (strings.TrimPrefix).
func ConvergeBucketIAMBinding(iamAPI BucketIAM, bucket, member, role string, mode MutationMode) IOE.IOEither[error, Result]

// bindingMember converts the CLI member form to the iam.Policy member value.
func bindingMember(member string) iam.Identity
```

- [ ] **Step 1: Write the failing tests** (fake mirrors Task 5's, over `iam.Policy`): adds-member (`ActionUpdated`, `get,set`) · unchanged (`get` only) · dry-run (no `set`) · forbidden named (`IsPermissionDenied`) · `TestBucketIAMBindingConditionalSameRole` (policy holds a CONDITIONAL binding for the same role → the plain binding is still added; `iam.Policy` splits conditional bindings — assert the unconditional set gains the member).
- [ ] **Step 2: Run → Expected build failure `undefined: BucketIAM`.**
- [ ] **Step 3: Implement** + production `storageBucketIAM` adapter (`client.Bucket(b).IAM().Policy(ctx)` / `.SetPolicy(ctx, p)`).
- [ ] **Step 4: Tests pass** (`DONE 5 tests`).
- [ ] **Step 5: CLI** `iam add-bucket-binding --bucket --member --role --cred-file --dry-run`; wrappers `grant-source-bucket-reader` (`arangodb.justfile:1895`) and `configure-source` (`postgres.justfile:225`) replace their `gcloud storage buckets add-iam-policy-binding` lines (secret-writing tails stay shell).
- [ ] **Step 6: Lint + docs.**
- [ ] **Step 7: Commit.**
  `git add internal/gcp/bucket_iam.go internal/gcp/bucket_iam_test.go cmd/cluster-ops/main.go just_modules/arangodb.justfile just_modules/postgres.justfile docs/reference/`
  `git commit -m "feat(gcp): converge bucket IAM bindings"`

---

## Task 7: SA key lifecycle converge → `sa create-key` extension

**Files:** Create `internal/gcp/sa_key.go`, `internal/gcp/sa_key_test.go`; modify `internal/gcp/service_account.go` (extract reusable pieces behind the seam), `cmd/cluster-ops/main.go`, `docs/reference/` sa doc

Spec rule (binding): mint only when the key file is missing or its key is gone upstream; never mint when a live key matches; audit-log key IDs (never secrets).

**Interfaces (exact) — cloud seam and file write are SEPARATE:**
```go
// SAKeyAPI is the injected CLOUD surface only (no file IO inside).
// ListKeys returns normalized key IDs (last path segment of
// projects/p/serviceAccounts/e/keys/<id>).
type SAKeyAPI interface {
	ListKeys(ctx context.Context, saEmail string) ([]string, error)
	CreateKey(ctx context.Context, saEmail string) (keyID string, keyJSON []byte, err error)
}

// ConvergeSAKey: file present AND its private_key_id in ListKeys →
// unchanged; file present but key gone → re-mint; file missing → mint.
// Exactly one mint in every path. DryRun reports the intent without minting.
// The key file is written 0o600 by writeKeyFile OUTSIDE the cloud seam.
func ConvergeSAKey(api SAKeyAPI, saEmail, keyFilePath string, mode MutationMode) IOE.IOEither[error, Result]

// keyFileID extracts private_key_id from a key JSON blob (encoding/json).
func keyFileID(keyJSON []byte) (string, error)

// writeKeyFile writes key material 0o600 (tested for its mode bits).
func writeKeyFile(path string, keyJSON []byte) error

// keyIDFromPath normalizes a keys/<id> resource path to <id>.
func keyIDFromPath(resource string) string
```

- [ ] **Step 1: Write the failing tests** (`sa_key_test.go`; file access via injected `ReadFile/Stat` — the `CreateProbes` pattern — plus the fake cloud seam):

```go
type fakeSAKeyAPI struct {
	live    []string
	mints   int
	lastID  string
	listErr error
}

func (f *fakeSAKeyAPI) ListKeys(context.Context, string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.live, nil
}

func (f *fakeSAKeyAPI) CreateKey(context.Context, string) (string, []byte, error) {
	f.mints++
	f.lastID = fmt.Sprintf("k-new-%d", f.mints)
	return f.lastID, []byte(`{"private_key_id": "` + f.lastID + `"}`), nil
}

func TestSAKeyReusesLiveFile(t *testing.T) {
	api := &fakeSAKeyAPI{live: []string{"k1"}}
	files := fakeFiles{keyFile: `{"private_key_id": "k1"}`}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, "sa@p.iam.gserviceaccount.com", "key.json", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Zero(t, api.mints)
}

func TestSAKeyMintsWhenMissing(t *testing.T) {
	api := &fakeSAKeyAPI{live: nil}
	files := fakeFiles{}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, "sa@p.iam.gserviceaccount.com", "key.json", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionCreated, got.Action)
	require.Equal(t, 1, api.mints)
	require.Equal(t, os.FileMode(0o600), files.wroteMode, "key material written 0600")
}

// Review Focus 3: dead upstream key → exactly one re-mint.
func TestSAKeyRemintsWhenKeyGone(t *testing.T) {
	api := &fakeSAKeyAPI{live: []string{"k2"}}
	files := fakeFiles{keyFile: `{"private_key_id": "k1"}`}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, "sa@p.iam.gserviceaccount.com", "key.json", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, 1, api.mints, "remints exactly once")
}

func TestSAKeyDryRunMintsNothing(t *testing.T) {
	api := &fakeSAKeyAPI{live: nil}
	files := fakeFiles{}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, "sa@p.iam.gserviceaccount.com", "key.json", MutationMode{DryRun: true})())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Zero(t, api.mints)
}

func TestSAKeyListErrorNamed(t *testing.T) {
	api := &fakeSAKeyAPI{listErr: &googleapi.Error{Code: http.StatusForbidden}}
	files := fakeFiles{}
	_, err := E.UnwrapError(ConvergeSAKey(api, files, "sa@p.iam.gserviceaccount.com", "key.json", MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, api.mints, "a forbidden listing never mints")
}

func TestKeyIDFromPath(t *testing.T) {
	require.Equal(t, "abc123", keyIDFromPath("projects/p/serviceAccounts/e/keys/abc123"))
	require.Equal(t, "abc123", keyIDFromPath("abc123"))
}
// (fakeFiles implements ReadFile/Stat/writeFile recording mode bits.)
```

- [ ] **Step 2: Run → Expected build failure `undefined: SAKeyAPI`.**
- [ ] **Step 3: Implement** (key file parsed in-process via `keyFileID`; audit line logs key ID + path only). Production `iamSAKeyAPI` adapter over `google.golang.org/api/iam/v1` (`Projects.ServiceAccounts.Keys.List/Create`), key JSON decoded from the create response.
- [ ] **Step 4: Tests pass** (`DONE 6 tests`).
- [ ] **Step 5: CLI** `sa create-key --sa-email --key-file --cred-file --dry-run` re-wired over the converge (existing handler becomes a thin edge).
- [ ] **Step 6: Lint + docs.**
- [ ] **Step 7: Commit.**
  `git add internal/gcp/sa_key.go internal/gcp/sa_key_test.go internal/gcp/service_account.go cmd/cluster-ops/main.go docs/reference/`
  `git commit -m "feat(gcp): idempotent service-account key lifecycle"`

---

## Task 8: SA setup flows → `sa setup-backup` / `sa setup-reader`

**Files:** Create `internal/gcp/sa_setup.go`, `internal/gcp/sa_setup_test.go`; modify `cmd/cluster-ops/main.go`, `just_modules/{arangodb,redis,postgres,cluster}.justfile`, `docs/reference/{arangodb,redis,postgres,kops}/` docs

Behavior sources: `arangodb.justfile:1552` + `redis.justfile:270` (SA + 1 role + key), `postgres.justfile:106` `configure-backup` (SA + 2 roles + key), `arangodb.justfile:1895` + `postgres.justfile:225` (cross-project SA + bucket IAM + key), `cluster.justfile:76` `sa-accounts-setup` (SA + key).

**Interfaces (exact):**
```go
// SAAPI is the injected service-account surface (production: iamSAAdapter).
type SAAPI interface {
	EnsureSA(ctx context.Context, project, name, display string) (email string, err error) // typed AlreadyExists absorbed
}

type BackupSAConfig struct {
	Project, SAName, DisplayName string
	Roles                        []string
	KeyFilePath                  string
	CredFile                     string
}

type CrossProjectReaderConfig struct {
	SourceProject, SAName, DisplayName string
	Bucket                             string
	Role                               string // "roles/storage.objectViewer"
	KeyFilePath, CredFile              string
}

// SetupBackupSA composes Task 5 + Task 7 primitives in order
// SA → one role binding per role → key. Re-run: all unchanged.
func SetupBackupSA(sa SAAPI, iam ProjectIAM, keys SAKeyAPI, files KeyFiles, cfg BackupSAConfig, mode MutationMode) IOE.IOEither[error, []Result]

// SetupCrossProjectReader composes SAAPI + Task 6 + Task 7:
// SA in source project → bucket IAM binding → key.
func SetupCrossProjectReader(sa SAAPI, bucketIAM BucketIAM, keys SAKeyAPI, files KeyFiles, cfg CrossProjectReaderConfig, mode MutationMode) IOE.IOEither[error, []Result]

// KeyFiles is the injected key-file IO (Stat/ReadFile/WriteFile) shared with Task 7.
type KeyFiles interface {
	Stat(path string) (fs.FileInfo, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
}
```

- [ ] **Step 1: Write the failing tests** (`sa_setup_test.go`; composed fakes record a shared call log):

```go
type composedFakes struct {
	sa      *fakeEnsureSA
	iam     *fakeProjectIAM
	keys    *fakeSAKeyAPI
	files   *fakeFiles
	log     *[]string
}

func TestSetupBackupSAHappy(t *testing.T) {
	// fresh world: EnsureSA "created" → two role sets "set" → one key mint.
	got, err := E.UnwrapError(SetupBackupSA(f.sa, f.iam, f.keys, f.files, twoRoleCfg(), MutationMode{})())
	require.NoError(t, err)
	require.Len(t, got, 4) // SA + 2 roles + key
	require.Equal(t, []string{"ensure-sa", "get", "set", "get", "set", "list-keys", "create-key"}, *f.log,
		"SA → roles → key, in order")
}

func TestSetupBackupSAIdempotent(t *testing.T) {
	// second run against the converged world: every step ActionUnchanged,
	// zero mutation seams called (log holds reads only).
}

func TestSetupReaderGrantsBucket(t *testing.T) {
	// bucket binding for member serviceAccount:<sa email>, role objectViewer
	// recorded on the fake bucket IAM.
}

// A role bind failing with 403 must stop the flow BEFORE key minting.
func TestSetupStopsOnForbiddenRole(t *testing.T) {
	_, err := E.UnwrapError(SetupBackupSA(f.sa, blockedIAM{err: &googleapi.Error{Code: http.StatusForbidden}}, f.keys, f.files, twoRoleCfg(), MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, f.keys.mints, "validate-before-later-mutation: no key minted after a failed binding")
}
```

(Fixtures: `twoRoleCfg()` = `BackupSAConfig{Project: "proj-dcr", SAName: "backup-gcs-sa", DisplayName: "backup", Roles: []string{"roles/storage.admin", "roles/storage.objectViewer"}, KeyFilePath: "credentials/proj-dcr/backup-gcs-sa.json"}`; `fakeEnsureSA` logs `ensure-sa`; the step stubs append to the shared log.)

- [ ] **Step 2: Run → Expected build failure `undefined: SetupBackupSA`.**
- [ ] **Step 3: Implement** as pure composition of the Task 5/6/7 converge functions (no new mutation logic; extract any `service_account.go` internals behind `SAAPI`/`KeyFiles` instead of duplicating — named steps, no nested closures).
- [ ] **Step 4: Tests pass** (`DONE 4 tests`).
- [ ] **Step 5: CLI + wrappers.** `sa setup-backup --project --name --display --roles … --key-file … --cred-file --dry-run`; `sa setup-reader --bucket --source-project --sa-name …`. Wrappers `setup-backup-sa` (arangodb/redis), `configure-backup` (postgres), `grant-source-bucket-reader`, `configure-source`, `sa-accounts-setup` switch their gcloud mutation blocks to the new subcommands (secret-write tails stay shell); old bodies renamed `*-shell`.
- [ ] **Step 6: Lint + docs.**
- [ ] **Step 7: Commit.**
  `git add internal/gcp/sa_setup.go internal/gcp/sa_setup_test.go cmd/cluster-ops/main.go just_modules/arangodb.justfile just_modules/redis.justfile just_modules/postgres.justfile just_modules/cluster.justfile docs/reference/`
  `git commit -m "feat(gcp): converge SA setup flows (backup + cross-project reader)"`

---

## Task 9: Contract tests + docs + final gate

**Files:** Create `scripts/test-gcp-converge.sh`; modify `Justfile` (add `test-gcp-converge` to `check` deps); modify `docs/reference/` per touched area

- [ ] **Step 1: Contract tests** (`test-gcp-converge.sh`, the `test-datastore-recipes.sh` shape — mock BOTH `go` and `pulumi` on PATH into one shared argv call log, `MOCK_GO_EXIT` controls the `go` mock):
  §1 `just gcp-pulumi pulumi-gcs-setup --gcs_bucket t1 …` → log line `go … bucket converge …` exists and its index precedes the `pulumi login` log line (order contract);
  §2 `just gcp-role add-role-to-sa --sa-name x --role roles/viewer …` → log contains `iam add-binding --member serviceAccount:… --role roles/viewer`;
  §3 `MOCK_GO_EXIT=1` → each wrapper exits non-zero;
  §4 interactive delete: `printf 'delete\n' | just gcp-cluster delete-state-bucket --cluster t1 --confirm yes` → log contains `bucket delete … --confirm yes` AND that line postdates the wrapper's preflight echoes (echo timestamps not needed — assert the wrapper fails closed when the prompt input is NOT `delete`: `printf 'no\n' | …` exits non-zero with no `bucket delete` in the log).
- [ ] **Step 2:** `bash scripts/test-gcp-converge.sh` → `gcp converge contract tests PASSED`; `just lint-recipes` → `no recipe findings`.
- [ ] **Step 3: Acceptance rg gates:** `rg 'create \|\| echo "already exists"' just_modules/` → nothing; mutated resources show no `|| true` (`rg '\|\| true' just_modules/pulumi.justfile just_modules/role.justfile` → nothing); converge-before-mutation order asserted per wrapper in §1/§4.
- [ ] **Step 4: Docs sync** — each touched `docs/reference/<area>/*.md` flag table → `cluster-ops … --help`; `just docs docs-lint` → `PASS` (capture `echo "exit=$?"`).
- [ ] **Step 5: Final gate.**
  - `go build ./... && go vet ./... && go vet -tags=integration ./internal/...` → no output
  - `go test -race -count=1 ./internal/gcp/ ./cmd/cluster-ops/` → all `ok`
  - `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → `DONE <N> tests, 1 skipped`, no failures
  - `golangci-lint run --build-tags=integration ./internal/... ./cmd/cluster-ops/...` → `0 issues.`
  - `just check; echo "exit=$?"` → every contract script prints its `PASSED` line (now incl. `gcp converge contract tests PASSED`), `exit=0` (gitignored `.env.prod.dcr-kube1` + `.tool-versions.prod.dcr-kube1` at repo root required)
- [ ] **Step 6: Commit.**
  `git add scripts/test-gcp-converge.sh Justfile docs/reference/`
  `git commit -m "test(recipes): contract tests for gcp converge wrappers"`

---

## Deferred (not in this plan)

- gcloud config/auth UX (`configure-gcloud`, `cleanup-gcloud-config`, `bootstrap-identities`, `rotate-to-*`) — spec non-goal (ambient state).
- Image ops (`create-image-from-instance`, `delete-machine-image`), `delete-backup` lifecycle, `create-hmac-key` (intentionally non-convergent).
- IAM condition expressions (`--condition`), batch IAM API for `assign-roles-to-sa` (shell loop suffices).
- The spec's generic `ConvergeResource[T]` — rejected (house anti-pattern; resource-specific converge functions instead).
- Live experiments/prod parity smoke pass + k3d integration suite (no cluster in scope).
- Etag/409 behavior under real concurrency beyond the injected-retry tests.
