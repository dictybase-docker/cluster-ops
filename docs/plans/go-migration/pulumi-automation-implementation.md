# Pulumi Automation API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Commits:** each task ends with one `git add` + `git commit -m` step (Conventional Commits). Never stage `docs/plans/**` (except an approved README status cell), `.superpowers/**`, ledgers, `.env*` or `.tool-versions*` scratch files.

**Goal:** Move the Pulumi CLI orchestration of the composite deploy flows to the Pulumi Automation API. Gain structured preview diffs, a protected-resource gate before `--yes`, and secret-safe structured logs.

**Architecture:** One new package, `internal/pulumiauto`, holds the flows. Each flow takes an injected seam (the `CreateProbes` pattern) so unit tests run without Pulumi. A production adapter wraps `auto.Stack`. Registry entries bind folder, stack, backend and secrets provider. Justfile recipes become thin `go run` wrappers and keep their `Next:` echoes and rollback `*-shell` copies.

**Tech Stack:** Go 1.26, fp-go v2 (house stack), `github.com/pulumi/pulumi/sdk/v3 v3.255.0` (`go/auto`, `go/auto/optup`, `go/auto/optpreview`, `go/auto/events`, `go/common/apitype`), urfave/cli v2, testify/require, bash contract tests, just.

**Spec:** [`pulumi-automation.md`](pulumi-automation.md) — authority for scope, non-goals and the go/no-go criteria. Its code blocks are sketches and do not compile against the real API; this plan replaces them. Its Phases 2–3 (datastore deploys, `create-multiple-resources`) stay out of scope here.

**Status:** Draft — NOT replay-verified. Execute task-by-task (TDD, RED→GREEN recorded), then change this line to `Ready to execute — Verified: replayed via execution on <range> (<date>)`.

---

## Staleness audit (supersedes the spec's inventory)

Already fixed or already done — **do not redo**:

| Item | State |
|---|---|
| SDK/CLI version skew (spec's no-go condition) | FIXED in `cf9871f`: SDK downgraded to 3.255.0 = registry `pulumi_version` (kubernetes program SDK co-downgraded to 4.33.0) |
| Local dev CLI (machine has v3.108.0) | Environment action, not code: run `just install-tool --name pulumi --version 3.255.0` before execution (Task 1 checks) |
| Phase 0 foundations (`internal/registry`, `internal/preflight`, `internal/waitfor`, CLI base) | DONE in the earlier plans |
| `check-backend`, `check-tools` "stay shell, belong in preflight" | MIGRATED already (`preflight pulumi-backend`, `preflight tools`) — the spec's "Recipes left in shell" rows for them are stale |
| `internal/pulumiauto` | Still greenfield (verified absent) |

Live scope (recipe line numbers re-derived; the spec's are stale):

| Flow | Recipe | CLI target |
|---|---|---|
| `ensure-stack` | `pulumi.justfile` | `stack ensure` |
| `set-secret` / `set-config` | `pulumi.justfile` | `stack config set --secret` / `stack config set` |
| `preview` | `pulumi.justfile` | `stack preview` |
| `create-resource` / `remove-resource` / `cleanup-resource` | `pulumi.justfile` | `stack up` / `stack destroy` / `stack rm` |
| `new-stack` / `new-stack-from` | `pulumi.justfile` | `stack ensure --create` / `stack ensure --copy-config-from` |
| `bootstrap-service` | `pulumi.justfile` | `deploy service` (composite) |
| `deploy-backend-services` | `ci.justfile` | `deploy backend` (loop) |

Design deltas vs the spec (decided here):
- CLI trees: new `cluster-ops stack …` and `cluster-ops deploy …` (no collision with `bucket`/`iam`/`sa`/`preflight`/…).
- Real API signatures only (pinned below). The spec's `event.Type` string switch and `auto.Project(auto.Project{…})` do not exist.
- Injected seams (`StackOpener`, `StackAPI`) instead of direct `auto.Stack` use. `auto.Stack` is a struct and cannot be faked.
- Structured secret redaction (value masking from the stack config) instead of the spec's "filter messages containing `secret`" string match. `optup.ProgressStreams` never points at stdout in CI mode.
- The protected-resource gate uses the typed preview event stream plus `PreviewResult.ChangeSummary`. Counts alone cannot name the resource type.
- `StackLock` is dropped (YAGNI — the spec itself says serial is the default and fine).

## Real API surface (pinned from `sdk/v3@v3.255.0`; the spec sketches differ)

```go
auto.NewLocalWorkspace(ctx context.Context, opts ...auto.LocalWorkspaceOption) (auto.Workspace, error)
auto.SelectStackLocalSource(ctx, name, workDir string, opts ...auto.LocalWorkspaceOption) (auto.Stack, error)
auto.UpsertStackLocalSource(ctx, name, workDir string, opts ...auto.LocalWorkspaceOption) (auto.Stack, error)

(*auto.Stack).Preview(ctx, ...optpreview.Option) (auto.PreviewResult, error)
(*auto.Stack).Up(ctx, ...optup.Option) (auto.UpResult, error)
(*auto.Stack).Destroy(ctx, ...optdestroy.Option) (auto.DestroyResult, error)
(*auto.Stack).SetConfig(ctx, key string, val auto.ConfigValue) error   // ConfigValue{Value string; Secret bool}
(*auto.Stack).GetAllConfig(ctx) (auto.ConfigMap, error)
(*auto.Stack).Outputs(ctx) (auto.OutputMap, error)                     // map[string]OutputValue

auto.PreviewResult{StdOut, StdErr string; ChangeSummary map[apitype.OpType]int}
optup.EventStreams(channels ...chan<- events.EngineEvent)      // same in optpreview
optup.ProgressStreams(writers ...io.Writer)                    // same in optpreview
events.EngineEvent{ apitype.EngineEvent; Error error }         // typed pointer payloads:
    .ResourcePreEvent *ResourcePreEvent  .ResourceEvent *ResourceEvent
    .DiagnosticEvent *DiagnosticEvent    .SummaryEvent *SummaryEvent
```

## Global Constraints

- fp-go house stack only; `F.Pipe`; no inside-out calls; no nested closure layers (named steps); `IOE.TryCatchError` holds raw effects, wrapping in `MapLeft`.
- Seams first: every flow takes interfaces. Production adapters wrap `auto.Stack`. No direct `auto` calls outside `internal/pulumiauto/adapter.go`.
- Secrets never reach logs or `Result.Detail`. Redaction masks the VALUES of all secret config keys (`GetAllConfig` → `ConfigValue.Secret`) in every event string. `ProgressStreams` writes to `io.Discard` in CI mode. No keyword or string matching for this.
- Protected-resource gate: a preview with `delete`/`replace` on a protected type (`PersistentVolumeClaim`, `StatefulSet`, `Deployment` in the database namespaces, anything with `Database` in its type token) blocks `Up` unless `--allow-protected` is set. Classification uses typed event metadata (`apitype.OpType`, `Metadata.Type`), never message text.
- `MutationMode{DryRun}` applies here too: dry-run stops at `Preview` and never reaches `Up`/`Destroy`.
- Identity binding explicit: folder, stack, backend and secrets provider come from the registry entry (`registry.LoadByStack`) or flags. Never ambient.
- Wrappers keep `Next:` echoes and use `quote()` on every user-supplied interpolation.
- Contract tests mock `go` and `pulumi` on one argv log and assert ORDER: preflight → preview → up, and `stack rm`/`destroy` after the confirmation prompt only.
- Gate before done: `just check` zero failures + Task 9 commands.

## Review Focus

Inputs the tasks' tests do not exercise — check each; each is pinned to a task step:
1. A secret config value leaks through a diagnostic event or engine output (Task 5 `TestUpRedactsSecretValues`).
2. A protected replace reaches `Up` (Task 4 `TestGateBlocksProtectedReplace`).
3. The Pulumi CLI is missing or version-skewed at workspace build: the failure names the cause (Task 2 `TestOpenStackNamesMissingCLI`).
4. `ChangeSummary` maps wrong (replaces counted as creates; `same` ignored) (Task 4 `TestSummaryFromChange`).
5. Config writes land on the wrong stack or folder (Task 2 `TestOpenStackBindsRegistryEntry`, Task 3 `TestSetConfigTargetsStack`).

---

## Task 1: Baseline

**Files:** none

- [ ] **Step 1:** `git log --oneline -1` → head after `cf9871f`.
- [ ] **Step 2:** `pulumi version` → `v3.255.0` (run `just install-tool --name pulumi --version 3.255.0` first if it reports another version; the machine ships 3.108.0 today).
- [ ] **Step 3:** `go build ./... && gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → `DONE 428 tests, 1 skipped`. `test -d internal/pulumiauto` → absent.

---

## Task 2: Workspace seams + stack open

**Files:** Create `internal/pulumiauto/workspace.go`, `internal/pulumiauto/workspace_test.go`, `internal/pulumiauto/adapter.go`

**Interfaces (exact):**
```go
package pulumiauto

// DeployTarget binds a folder+stack to its registry entry.
type DeployTarget struct {
	Folder, Stack string
	Backend       string // entry.PulumiState
	Secrets       string // entry.KmsSecretsProvider ("passphrase" locally)
	CredFile      string // GOOGLE_APPLICATION_CREDENTIALS
	Passphrase    string // PULUMI_CONFIG_PASSPHRASE for local stacks
}

// DeployMode governs mutations (the gcp-converge MutationMode shape).
type DeployMode struct {
	DryRun         bool
	AllowProtected bool
}

// StackOpener opens stacks against a FULL target (production: autoOpener in
// adapter.go wires WorkDir/SecretsProvider/EnvVars from the target).
type StackOpener interface {
	Select(ctx context.Context, t DeployTarget) (StackAPI, error)
	Upsert(ctx context.Context, t DeployTarget) (StackAPI, error)
}

// StackAPI is the flow surface; auto.Stack is a struct and cannot be faked.
// Preview and Up receive the event channel: the caller creates it, the
// adapter wires optpreview/optup.EventStreams, the caller closes it after the
// call returns and its collector goroutine exits on close.
type StackAPI interface {
	Name() string
	Preview(ctx context.Context, events chan<- events.EngineEvent) (auto.PreviewResult, error)
	Up(ctx context.Context, events chan<- events.EngineEvent) (auto.UpResult, error)
	Destroy(ctx context.Context) error
	SetConfig(ctx context.Context, key string, val auto.ConfigValue) error
	GetAllConfig(ctx context.Context) (auto.ConfigMap, error)
	Outputs(ctx context.Context) (auto.OutputMap, error)
}

// PreflightRunner and RolloutWaiter are the two existing package seams the
// composite (Task 7) depends on.
type PreflightRunner func() []preflight.Result
type RolloutWaiter func(ctx context.Context) (string, error)

// LoadServices reads config/services.yaml in list order (yq-free: gopkg.in/
// yaml.v3). Missing file is a named error, not an empty list.
func LoadServices(path string) ([]ServiceSpec, error)

// OpenStack selects the stack or creates it when create is set. Every binding
// comes from the target; the failure names the cause (CLI missing, no
// Pulumi.yaml, wrong backend).
func OpenStack(ctx context.Context, opener StackOpener, t DeployTarget, create bool) (StackAPI, error)
```

The shared test double (Task 2 step 3, reused by every later task) is complete
and compilable:

```go
// fakeStack records every call and replays canned results/events.
type fakeStack struct {
	name        string
	previewRes  auto.PreviewResult
	upRes       auto.UpResult
	previewEv   []events.EngineEvent // pushed on the channel before return
	upEv        []events.EngineEvent
	configs     map[string]auto.ConfigValue
	outputs     auto.OutputMap
	log         *[]string // shared sequence log
}

func (f *fakeStack) Name() string { return f.name }

func (f *fakeStack) Preview(_ context.Context, ch chan<- events.EngineEvent) (auto.PreviewResult, error) {
	*f.log = append(*f.log, "preview")
	for _, e := range f.previewEv {
		ch <- e
	}
	return f.previewRes, nil
}

func (f *fakeStack) Up(_ context.Context, ch chan<- events.EngineEvent) (auto.UpResult, error) {
	*f.log = append(*f.log, "up")
	for _, e := range f.upEv {
		ch <- e
	}
	return f.upRes, nil
}

func (f *fakeStack) Destroy(context.Context) error {
	*f.log = append(*f.log, "destroy")
	return nil
}

func (f *fakeStack) SetConfig(_ context.Context, key string, val auto.ConfigValue) error {
	*f.log = append(*f.log, "set-config:"+key)
	if f.configs == nil {
		f.configs = map[string]auto.ConfigValue{}
	}
	f.configs[key] = val
	return nil
}

func (f *fakeStack) GetAllConfig(context.Context) (auto.ConfigMap, error) {
	return f.configs, nil
}

func (f *fakeStack) Outputs(context.Context) (auto.OutputMap, error) {
	return f.outputs, nil
}
```

- [ ] **Step 1: Write the failing tests** (`workspace_test.go`):

```go
package pulumiauto

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeOpener struct {
	selected []string // "stack|folder|backend" per call
	upserted []string
	failWith error
	returned *fakeStack
}

func (f *fakeOpener) Select(_ context.Context, t DeployTarget) (StackAPI, error) {
	f.selected = append(f.selected, t.Stack+"|"+t.Folder+"|"+t.Backend)
	return f.returned, f.failWith
}

func (f *fakeOpener) Upsert(_ context.Context, t DeployTarget) (StackAPI, error) {
	f.upserted = append(f.upserted, t.Stack+"|"+t.Folder+"|"+t.Backend)
	return f.returned, f.failWith
}

func TestOpenStackBindsRegistryEntry(t *testing.T) {
	op := &fakeOpener{returned: &fakeStack{name: "experiments"}}
	target := DeployTarget{Folder: "modware-annotation", Stack: "experiments",
		Backend: "gs://pulumi-state-proj", Secrets: "gcpkms://projects/p/…/keys/pulumi"}
	s, err := OpenStack(context.Background(), op, target, false)
	require.NoError(t, err)
	require.Equal(t, []string{"experiments|modware-annotation|gs://pulumi-state-proj"}, op.selected)
	require.Empty(t, op.upserted, "select must not create")
	require.Equal(t, "experiments", s.Name())
}

func TestOpenStackCreatesWhenAsked(t *testing.T) {
	op := &fakeOpener{returned: &fakeStack{name: "new-stack"}}
	_, err := OpenStack(context.Background(), op, DeployTarget{Folder: "f", Stack: "new-stack"}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"new-stack|f|"}, op.upserted)
	require.Empty(t, op.selected)
}

// Review Focus 3: a missing CLI must surface as a named cause, not a panic.
func TestOpenStackNamesMissingCLI(t *testing.T) {
	_, err := OpenStack(context.Background(), &fakeOpener{failWith: exec.ErrNotFound},
		DeployTarget{Folder: "f", Stack: "s"}, false)
	require.ErrorIs(t, err, exec.ErrNotFound)
	require.Contains(t, err.Error(), "pulumi")
}

func TestOpenStackRejectsUnboundTarget(t *testing.T) {
	_, err := OpenStack(context.Background(), &fakeOpener{}, DeployTarget{}, true)
	require.Error(t, err, "folder and stack must be bound")
}
```

- [ ] **Step 2: Run → Expected build failure `undefined: StackOpener` / `fakeStack`.**
- [ ] **Step 3: Implement.** `OpenStack` validates the target, then calls `Select` (or `Upsert` when `create`). Errors wrap with the named cause (`fmt.Errorf("open stack %s in %s: %w", …)`). `adapter.go` holds `autoStack{auto.Stack}` implementing `StackAPI`, plus `autoOpener` implementing `StackOpener` with `SelectStackLocalSource`/`UpsertStackLocalSource` and `NewLocalWorkspace` options (`WorkDir`, `SecretsProvider`, `EnvVars` for `PULUMI_BACKEND_URL`/`PULUMI_CONFIG_PASSPHRASE`/`GOOGLE_APPLICATION_CREDENTIALS`). `fakeStack` is the shared test double (added here, used by later tasks).
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: Commit.** `git add internal/pulumiauto/ && git commit -m "feat(pulumi): workspace seams and stack open bound to the registry"`

---

## Task 3: Config and outputs

**Files:** Create `internal/pulumiauto/config.go`, `internal/pulumiauto/config_test.go`; extend `fakeStack`

**Interfaces (exact):**
```go
// SetConfig writes one key; secret marks it for the secrets provider.
func SetConfig(ctx context.Context, s StackAPI, key, value string, secret bool) IOE.IOEither[error, struct{}]

// SecretValues returns every secret config VALUE of the stack. The redactor
// (Task 5) and the gate (Task 4) consume it; never log the result.
func SecretValues(ctx context.Context, s StackAPI) IOE.IOEither[error, []string]

// StackOutput reads one output value.
func StackOutput(ctx context.Context, s StackAPI, key string) IOE.IOEither[error, string]
```

- [ ] **Step 1: Tests:** `TestSetConfigTargetsStack` (fake records `SetConfig(key, ConfigValue{Value, Secret:false})`) · `TestSetSecretMarksSecret` (`secret=true` → `ConfigValue.Secret == true`; the fake's log shows the key but the test asserts the value never appears in any captured log line) · `TestSecretValuesCollectsOnlySecrets` (mixed config map → only `Secret:true` values returned) · `TestStackOutputReadsKey` (`OutputValue{Value: "ns1"}` → `"ns1"`; missing key → named error `output %q not set on stack %s`).
- [ ] **Step 2: Run → Expected build failure `undefined: SetConfig`.**
- [ ] **Step 3: Implement** (named steps; `GetAllConfig` map scan; missing key via `errors.Is(err, errOutputNotFound)` sentinel, not string match).
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: Commit.** `git add internal/pulumiauto/config* && git commit -m "feat(pulumi): config, secret collection and stack outputs"`

---

## Task 4: Preview summary + protected-resource gate

**Files:** Create `internal/pulumiauto/preview.go`, `internal/pulumiauto/preview_test.go`

**Interfaces (exact):**
```go
// PreviewSummary counts plan changes. SummaryFrom maps the typed
// ChangeSummary: "create"/"update"/"delete"/"replace"/"same" OpTypes (local
// consts over apitype.OpType — data keys, never message matching).
type PreviewSummary struct{ Creates, Updates, Deletes, Replaces, Same int }
func SummaryFrom(change map[apitype.OpType]int) PreviewSummary

// ProtectedChange is one blocked delete/replace.
type ProtectedChange struct{ URN, Type string; Op apitype.OpType }

// PreviewGate runs the preview and blocks on protected types. It creates the
// event channel, drains it in a named collector goroutine until close, calls
// Preview, then closes the channel and waits for the collector — no leaks, no
// deadlock. ProtectedTypes is injectable (default: PVC, StatefulSet,
// Deployment, and any type token containing "Database").
func PreviewGate(ctx context.Context, s StackAPI, protected []string) IOE.IOEither[error, GateResult]
type GateResult struct {
	Summary   PreviewSummary
	Blocked   []ProtectedChange
}
```

- [ ] **Step 1: Tests** (fakeStack.Preview returns a canned `auto.PreviewResult` and pushes canned `events.EngineEvent`s on the channel):
```go
func TestSummaryFrom(t *testing.T) {
	got := SummaryFrom(map[apitype.OpType]int{
		opCreate: 2, opUpdate: 1, opDelete: 1, opReplace: 3, opSame: 9,
	})
	require.Equal(t, PreviewSummary{Creates: 2, Updates: 1, Deletes: 1, Replaces: 3, Same: 9}, got)
}

func TestGateBlocksProtectedReplace(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log, previewRes: auto.PreviewResult{
		ChangeSummary: map[apitype.OpType]int{opReplace: 1},
	}, previewEv: []events.EngineEvent{statefulSetReplaceEvent()}}
	got, err := E.UnwrapError(PreviewGate(context.Background(), st, nil)())
	require.NoError(t, err)
	require.Len(t, got.Blocked, 1)
	require.Equal(t, "kubernetes:apps/v1:StatefulSet", got.Blocked[0].Type)
}

func TestGateAllowsPlainCreate(t *testing.T) {
	st := &fakeStack{previewRes: auto.PreviewResult{
		ChangeSummary: map[apitype.OpType]int{opCreate: 1},
	}, previewEv: []events.EngineEvent{createEvent()}}
	got, err := E.UnwrapError(PreviewGate(context.Background(), st, nil)())
	require.NoError(t, err)
	require.Empty(t, got.Blocked)
}

// Review Focus 2: a blocked gate stops the flow before Up.
func TestGateStopsBeforeUp(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log, previewEv: []events.EngineEvent{statefulSetReplaceEvent()}}
	mode := DeployMode{} // no AllowProtected
	_, err := E.UnwrapError(RunIfAllowed(context.Background(), st, mode, func() IOE.IOEither[error, auto.UpResult] {
		return Up(context.Background(), st, mode.UpOptions()) // named flow step
	})())
	require.Error(t, err)
	require.NotContains(t, log, "up")
}

// The collector exits on channel close: no goroutine leak, no deadlock.
func TestGateCollectorExitsOnClose(t *testing.T) {
	st := &fakeStack{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = E.UnwrapError(PreviewGate(context.Background(), st, nil)())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PreviewGate deadlocked on the event channel")
	}
}
```
(`statefulSetReplaceEvent`/`createEvent` build typed `events.EngineEvent` fixtures;
`RunIfAllowed` is the named gate step the CLI and Task 7 use.)
- [ ] **Step 2: Run → Expected build failure `undefined: PreviewGate`.**
- [ ] **Step 3: Implement.** `SummaryFrom` is pure. `PreviewGate` drains the event channel into `ProtectedChange` rows while `Preview` runs (named `collectProtected` step fed by the channel), then classifies `delete`/`replace` against the protected list by `Metadata.Type` token.
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: Commit.** `git add internal/pulumiauto/preview* && git commit -m "feat(pulumi): preview summary and the protected-resource gate"`

---

## Task 5: Up with secret-safe event logging

**Files:** Create `internal/pulumiauto/up.go`, `internal/pulumiauto/up_test.go`

**Interfaces (exact):**
```go
// Redactor masks secret config values in text (exact-value masking; values
// come from SecretValues — no keyword lists, no message matching).
type Redactor struct{ secrets []string } // empty values are ignored
func (r Redactor) Redact(s string) string

// UpOptions: CIRun silences raw engine output (ProgressStreams → io.Discard);
// DryRun stops before Up. Logger receives ONLY whitelisted fields:
// step, op, URN (resource events) and redacted diagnostics at debug level.
type UpOptions struct {
	CIRun    bool
	DryRun   bool
	Logger   *slog.Logger
	Secrets  []string // from SecretValues
}
func Up(ctx context.Context, s StackAPI, ev chan<- events.EngineEvent, opts UpOptions) IOE.IOEither[error, auto.UpResult]
```

Raw engine output is ALWAYS discarded: `optup.ProgressStreams(io.Discard)` is
wired unconditionally in the adapter. Operators read the structured log. The
`CIRun` field therefore only toggles log verbosity, never the raw stream.

- [ ] **Step 1: Tests** (fakeStack.Up pushes canned events and returns a canned `auto.UpResult`):
```go
func TestUpRedactsSecretValues(t *testing.T) {
	var buf bytes.Buffer
	st := &fakeStack{upEv: []events.EngineEvent{diagnosticEvent("db password is s3cr3t-value")}}
	opts := UpOptions{Logger: slog.New(slog.NewTextHandler(&buf, nil)),
		Secrets: []string{"s3cr3t-value"}}
	_, err := E.UnwrapError(Up(context.Background(), st, make(chan events.EngineEvent), opts)())
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "s3cr3t-value")
}

func TestUpDryRunNeverUpdates(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log}
	mode := DeployMode{DryRun: true}
	_, err := E.UnwrapError(Up(context.Background(), st, make(chan events.EngineEvent), mode.UpOptions())())
	require.NoError(t, err)
	require.NotContains(t, log, "up")
}

func TestRedactorMasksExactValues(t *testing.T) {
	r := Redactor{secrets: []string{"s3cr3t", ""}}
	require.Equal(t, "db pass is ***", r.Redact("db pass is s3cr3t"))
	require.Equal(t, "plain text", (Redactor{}).Redact("plain text"),
		"empty secrets are ignored; empty redactor is identity")
}

// The event consumer must exit when the channel closes.
func TestUpCollectorExitsOnClose(t *testing.T) { /* same done-channel shape as TestGateCollectorExitsOnClose */ }
```
- [ ] **Step 2: Run → Expected build failure `undefined: Redactor`.**
- [ ] **Step 3: Implement.** `Up` wires `optup.EventStreams(ev)` and `optup.ProgressStreams(io.Discard)` when `CIRun`. A named `logEvents` consumer reads the channel through `Redact` and logs only whitelisted fields.
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: Commit.** `git add internal/pulumiauto/up* && git commit -m "feat(pulumi): secret-safe event logging for update"`

---

## Task 6: CLI `stack` tree

**Files:** Create `cmd/cluster-ops/stack.go`; modify `cmd/cluster-ops/main.go`; modify `just_modules/pulumi.justfile` (`ensure-stack`, `set-config`, `set-secret`, `preview`, `create-resource`, `remove-resource`, `cleanup-resource`, `new-stack`, `new-stack-from`)

**Subcommands (exact flags):**
```
stack ensure     --folder <dir> --stack <name> [--create] [--copy-config-from <stack>]
stack config set --folder <dir> --stack <name> --key <k> --value <v> [--secret]
stack preview    --folder <dir> --stack <name> [--allow-protected]
stack up         --folder <dir> --stack <name> [--dry-run] [--allow-protected]
stack destroy    --folder <dir> --stack <name> [--dry-run]
stack outputs    --folder <dir> --stack <name> --key <k>
```

- [ ] **Step 1: Tests** (`cmd/cluster-ops/stack_test.go`): `TestStackEnsureFlagContract` (running the app with `stack ensure --help` lists the flags above) · `TestStackUpDryRunFlag` (action with `--dry-run` reaches `Up` with `UpOptions.DryRun` — captured via a test seam hook). Keep tests at the flag/wiring level; flow behavior lives in `internal/pulumiauto`.
- [ ] **Step 2: Run → Expected build failure `undefined: stackCommand`.**
- [ ] **Step 3: Implement.** Actions resolve the registry entry (`registry.LoadByStack`), build `DeployTarget`, open the stack, run the flow, print one summary line (`stack <name>: <creates>/<updates>/<deletes>/<replaces>`), exit 1 on gate blockers unless `--allow-protected`.
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: Wrappers.** Each `pulumi.justfile` recipe listed above becomes `go run ./cmd/cluster-ops stack …` with `quote()` args; old bodies renamed `*-shell` with their `[arg]` attributes copied; `Next:` echoes stay.
- [ ] **Step 6: Lint + docs.** `golangci-lint run ./...` → `0 issues.`; `docs/reference/backend/pulumi-automation.md` created (STYLE.md shape: Behavior + Flags).
- [ ] **Step 7: Commit.** `git add cmd/cluster-ops/ just_modules/pulumi.justfile docs/reference/backend/pulumi-automation.md && git commit -m "feat(pulumi): stack CLI tree and thin recipe wrappers"`

---

## Task 7: `deploy service` composite (bootstrap-service)

**Files:** Create `internal/pulumiauto/bootstrap.go`, `internal/pulumiauto/bootstrap_test.go`; modify `cmd/cluster-ops/deploy.go` (new), `just_modules/pulumi.justfile` (`bootstrap-service`)

**Interfaces (exact):**
```go
// BootstrapService runs the full service gate: preflight checks (internal/
// preflight) → open stack → set config (image tag) → preview + gate → up →
// rollout wait (internal/waitfor). A gate block stops the flow before Up.
func BootstrapService(ctx context.Context, opener StackOpener, t DeployTarget, svc ServiceSpec, mode DeployMode) IOE.IOEither[error, BootstrapResult]
type ServiceSpec struct {
	Folder, Stack, ImageTag string
	Preflight               PreflightRunner
	Rollout                 RolloutWaiter
}
type BootstrapResult struct{ Summary PreviewSummary; Rollout string }
```

- [ ] **Step 1: Tests** (composed fakes, one shared call log — the `gcp-converge` Task 8 pattern):
  `TestBootstrapHappy` (log order `preflight, open, set-config, preview, up, rollout`) · `TestBootstrapStopsOnGate` (protected replace → error, `up` NOT in the log) · `TestBootstrapStopsOnPreflightFail` (a failing preflight Result → no `open`) · `TestBootstrapIdempotent` (second run: preview `same` → `up` still runs with `--skip-preview` semantics? no — pin: up runs only when the summary is non-empty; second run reports `unchanged`).
- [ ] **Step 2: Run → Expected build failure `undefined: BootstrapService`.**
- [ ] **Step 3: Implement** as named-step composition of Tasks 2–5 (no new mutation logic; no nested closures).
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: CLI + wrapper.** `deploy service --folder --stack --image-tag [--dry-run] [--allow-protected]`; `bootstrap-service` recipe wraps it (rollback copy `bootstrap-service-shell`; `Next:` echo stays).
- [ ] **Step 6: Commit.** `git add internal/pulumiauto/bootstrap* cmd/cluster-ops/deploy.go just_modules/pulumi.justfile && git commit -m "feat(pulumi): bootstrap-service composite flow"`

---

## Task 8: `deploy backend` loop + contract tests

**Files:** Create `cmd/cluster-ops/deploy.go` extension + `internal/pulumiauto/multi.go`, `multi_test.go`; create `scripts/test-pulumi-automation.sh`; modify `Justfile` (add `test-pulumi-automation` to `check` deps)

**Interfaces:** `DeployBackendServices(ctx, opener, target DeployTarget, services []ServiceSpec, mode MutationMode) IOE.IOEither[error, []BootstrapResult]` — serial, in list order (`config/services.yaml` order preserved).

- [ ] **Step 1: Tests:** `TestDeployServicesSerialOrder` (two services; shared log shows service A fully before service B) · `TestDeployServicesStopsOnFirstFailure` (A's gate blocks → B never runs).
- [ ] **Step 2: Run → Expected build failure.**
- [ ] **Step 3: Implement** (named sequential composition — the `SetupBackupSA` shape).
- [ ] **Step 4: Contract tests** (`test-pulumi-automation.sh`, mocks `go` + `pulumi` + `jq` on one argv log):
  §1 `just gcp-pulumi bootstrap-service --folder f --stack s --image-tag v1` → log order: `preflight` before `stack preview` before `stack up`;
  §2 `just gcp-pulumi ensure-stack --folder f --stack s` → argv `stack ensure --folder f --stack s`;
  §3 `MOCK_GO_EXIT=1` propagates non-zero from each wrapper;
  §4 `just ci deploy-backend-services …` runs `deploy backend` and stops at the first failing service (`MOCK_GO_EXIT=1` on the second call).
- [ ] **Step 5:** `bash scripts/test-pulumi-automation.sh` → `pulumi automation contract tests PASSED`; `just lint-recipes` → `no recipe findings`.
- [ ] **Step 6: Commit.** `git add internal/pulumiauto/multi* cmd/cluster-ops/deploy.go scripts/test-pulumi-automation.sh Justfile && git commit -m "feat(pulumi): deploy backend loop with order contracts"`

---

## Task 9: Final gate

**Files:** none

- [ ] **Step 1:** `go build ./... && go vet ./... && go vet -tags=integration ./internal/...` → no output.
- [ ] **Step 2:** `go test -race -count=1 ./internal/pulumiauto/ ./cmd/cluster-ops/` → all `ok`.
- [ ] **Step 3:** `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → `DONE <N> tests, 1 skipped`.
- [ ] **Step 4:** `golangci-lint run --build-tags=integration ./internal/... ./cmd/cluster-ops/...` → `0 issues.`
- [ ] **Step 5:** `just check; echo "exit=$?"` → every contract script prints its `PASSED` line (now incl. `pulumi automation contract tests PASSED`), `exit=0`.
- [ ] **Step 6:** Acceptance rg gates: `rg 'ProgressStreams\(os' internal/` → nothing (CI mode discards raw engine output); `rg 'contains.*secret|strings.Contains.*[Ss]ecret' internal/pulumiauto/` → nothing (redaction is value-based); `rg 'pulumi ' just_modules/pulumi.justfile | grep -v shell` → only `pulumi login` (the two setup recipes stay shell).
- [ ] **Step 7: Commit.** `git add docs/reference/backend/pulumi-automation.md Justfile scripts/test-pulumi-automation.sh && git commit -m "test(recipes): pulumi automation final gate and docs"` — exact paths only; never `docs/plans/**`.

---

## Deferred (not in this plan)

- Phase 2 datastore deploys (`deploy-arangodb-*`, `deploy-postgres-*`, …) and Phase 3 `create-multiple-resources` — the spec's later phases.
- `StackLock` / parallel deploys — YAGNI (serial is the default and fine).
- k3d integration suite (`//go:build integration`) against the local file backend.
- `pulumi-gcs-setup`, `pulumi-local-setup`, `fork-stack` stay shell (spec's own list).
- Local CLI upgrade to 3.255.0 on dev machines (environment action, Task 1).
