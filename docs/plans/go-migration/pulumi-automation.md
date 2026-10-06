# Plan: Pulumi Automation API Migration

**Status**: Proposed — optional; do after the four mandatory categories (checks/probes, port-forward, wait/poll, GCP converge), and only if deploy flows keep growing.

## Table of Contents
- [Goal](#goal)
- [Non-goals](#non-goals)
- [Decision criteria](#decision-criteria)
- [Current state](#current-state)
- [Inventory](#inventory)
- [Design](#design)
  - [Package structure](#package-structure)
  - [Workspace construction](#workspace-construction)
  - [Preview with structured diff](#preview-with-structured-diff)
  - [Event streaming to slog](#event-streaming-to-slog)
  - [Concurrency and locking](#concurrency-and-locking)
  - [Deterministic ordering](#deterministic-ordering)
- [Target flows](#target-flows)
- [Phases](#phases)
- [Acceptance](#acceptance)
- [Risks](#risks)
- [Cost-benefit](#cost-benefit)
- [Recipes left in shell](#recipes-left-in-shell)

## Goal

Move Pulumi CLI orchestration from shell to the Pulumi Automation API (`github.com/pulumi/pulumi/sdk/v3/go/auto`). Enables structured preview diffs, programmatic event handling, and better composition when deploy flows grow beyond the ~60 shell recipes that wrap `pulumi -C <folder> <cmd>`.

## Non-goals

- Do **not** migrate until the four mandatory categories land (checks/probes, port-forward, wait/poll, GCP converge). This category is optional.
- Do **not** remove the `pulumi` CLI binary dependency. The Automation API calls the CLI under the hood; the binary must still be present and match the SDK version.
- Do **not** change the stack config file format. `Pulumi.<stack>.yaml` files remain the committed source of truth; CI deploys read them from `cluster-ops` develop. The Automation API reads these files exactly as the CLI does.
- Do **not** change the backend URLs, secrets providers, or encryption keys. GCS backend (`gs://pulumi-state-<project>`), local backend (`file://pulumi-files/local-state/`), KMS secrets provider (GCP), and passphrase provider (local k3d) all stay.
- Do **not** touch recipes that are thin wrappers around a single `pulumi` call with no parsing (`just gcp-pulumi preview`). Only migrate recipes with orchestration logic (multi-step flows, output parsing, idempotency, concurrency).

## Decision criteria

Go only if:

1. **Deploy flows keep growing** beyond bootstrap-backend → scaffold-stack → check-prereqs → bootstrap-service → deploy-backend-services (~5 composite recipes, ~800 LOC).
2. **Preview diff parsing** becomes required (refuse `--yes` when a replace/delete hits protected resources like PVCs; show create/update/delete/replace counts).
3. **Structured event logging** is needed (filter Pulumi's event stream to slog, not raw stdout).
4. **Concurrency** becomes a blocker (serial-by-default is fine; parallel stacks need per-stack locks).

If the deploy flows stabilize after phase-1 (the mandatory categories), **do not** do this plan. The shell recipes work, and the Automation API adds Go code to maintain for marginal gain.

No-go if:

- The Automation API version drifts from the CLI version in the registry (`config/clusters/<cluster>.yaml` owns `pulumi_version`). Version skew breaks plugin installs.
- State locks become a problem (Automation API acquires the same GCS-backed locks the CLI does; no additional locking needed, but contention is possible).
- Secret leakage in event logs is a risk (Automation API event stream can contain decrypted values; must filter before logging).

## Current state

| Aspect | Today | Problem |
|--------|-------|---------|
| Pulumi SDK | `github.com/pulumi/pulumi/sdk/v3 v3.266.0` in go.mod | Already present for Pulumi programs; Automation API is part of the same SDK |
| CLI orchestration | ~243 `pulumi` CLI calls across ~60 shell recipes in just_modules/*.justfile | Shell parsing of CLI output is fragile (JSON flags help but `jq` everywhere); no structured event handling |
| Stack config files | `Pulumi.<stack>.yaml` committed to cluster-ops repo | Source of truth for CI; must stay |
| Backend setup | `pulumi-gcs-setup` (GCS), `pulumi-local-setup` (filesystem) | Login recipes; Automation API can consume these or set backends programmatically |
| Secrets providers | `gcpkms://projects/<project>/locations/global/keyRings/<ring>/cryptoKeys/<key>` (GCP), `passphrase` (local k3d) | Set per stack; Automation API respects these |
| Output parsing | `pulumi stack output <key>` in shell, captured via `$(...)` | Used in ~6 places (arangodb, postgres, redis, logto, nats, minio) to read namespace/credential references from namespace-bootstrap stack |
| Idempotency | `pulumi stack ls --json \| jq -e 'any(.[]; .name == $s)'` to check stack existence; `kubectl get deploy/<name>` to check running image tag before `up` | Shell-based describe-then-create; Automation API can wrap this |
| Concurrency | Serial deploys in `deploy-backend-services` (loop over services) | One service at a time; no locks needed; parallel stacks would need per-stack locking |
| Ordered deploys | `pulumi-files/resources/initial-resources.txt`, `database-and-storage-resources.txt` (7 + 6 projects) | StackReference dependencies (namespace-bootstrap → arangodb-single → create-arangodb-databases); serial order enforced by recipe loop |
| Version pinning | `config/clusters/<cluster>.yaml` owns `pulumi_version` (e.g. "3.108.0"); `just ci sync-deploy-vars` publishes to GitHub variables | CLI binary must match this version; Automation API SDK must match or stay within one minor |

## Inventory

### Core orchestration recipes (pulumi.justfile, ~778 LOC)

| Recipe | Line | CLI calls | Parsing | Idempotency | Target API flow |
|--------|------|-----------|---------|-------------|-----------------|
| `bootstrap-backend` | 8 | 0 (calls script) | — | describe-then-create | Out of scope (one-time setup) |
| `pulumi-gcs-setup` | 21 | 1 (`pulumi login`) | none | describe bucket | `auto.Login` or env var |
| `ensure-stack` | 84 | 3 (`stack select`, `stack init`, `stack ls`) | exit code | select-or-init | `auto.SelectStackLocalSource` / `UpsertStackLocalSource` |
| `set-secret` | 117 | 1 (`config set --secret`) | none | none | `stack.SetConfig(ctx, key, auto.ConfigValue{Value: val, Secret: true})` |
| `set-config` | 133 | 1 (`config set [--plaintext]`) | none | none | `stack.SetConfig(ctx, key, auto.ConfigValue{Value: val})` |
| `preview` | 150 | 1 (`preview`) | none | none | `stack.Preview(ctx, optpreview.Diff())` |
| `new-stack` | 163 | 1 (`stack init`) | none | none | `auto.UpsertStackLocalSource` |
| `new-stack-from` | 177 | 1 (`stack init --copy-config-from`) | none | none | `UpsertStackLocalSource` + copy ConfigMap |
| `create-resource` | 190 | 1 (`up -f -y`) | none | none | `stack.Up(ctx, optup.ProgressStreams(os.Stdout))` |
| `remove-resource` | 203 | 1 (`destroy -f -y`) | none | none | `stack.Destroy(ctx)` |
| `cleanup-resource` | 216 | 1 (`stack rm --preserve-config`) | none | none | `stack.Workspace().RemoveStack` |
| `create-multiple-resources` | 230 | 2N (`new-stack-from`, `create-resource`) | none | loop | Loop calling Go funcs |
| `scaffold-backend-stack` | 544 | 1 (`stack init`) | none | describe file | `UpsertStackLocalSource` + template write |
| `check-backend-prereqs` | 641 | 0 (kubectl only) | yq on config | none | Out of scope (kubectl + registry; belongs in `internal/preflight`) |
| `bootstrap-service` | 778 | 0 (calls other recipes) | kubectl image check | running image tag | Composite Go func: `RunPreflight` → `EnsureStack` → `SetConfig` → `Preview` → `Up` → `WaitRollout` |

### CI recipes (ci.justfile)

| Recipe | Line | CLI calls | Parsing | Target |
|--------|------|-----------|---------|--------|
| `deploy-backend-services` | 428 | 0 (calls gcp-pulumi recipes) | yq on services.yaml | Loop calling `ScaffoldStack`, `CheckPrereqs`, `BootstrapService` per service |

### Datastore deploy recipes (arangodb.justfile ~73 calls, postgres ~41, redis ~18, minio ~6, nats ~6, logto ~5)

| Recipe file | Key recipes | CLI calls | Parsing |
|-------------|-------------|-----------|---------|
| arangodb.justfile | `deploy-local-arangodb`, `restore-arangodb`, `deploy-arangodb-operator`, `deploy-arangodb-single`, `deploy-arangodb-backup` | `pulumi up`, `stack output`, `config set-all`, `config rm`, `stack ls`, `refresh` | JSON (`stack ls --json`), stack output capture, exit code checks |
| postgres.justfile | `deploy-postgres-operator`, `deploy-postgres-cluster` | `pulumi up`, `ensure-stack`, `set-config`, `stack output` | stack output for namespace |
| redis.justfile | `deploy-redis` | `pulumi up`, `stack output` | stack output for namespace |
| minio.justfile | `deploy-minio` | `ensure-stack`, `set-secret`, `preview`, `create-resource` | none |
| nats.justfile | `deploy-nats` | `ensure-stack`, `config get`, `preview`, `create-resource` | `config get --path` for validation |
| logto.justfile | `deploy-logto` | `ensure-stack`, `preview`, `create-resource`, `stack output` | stack output for namespace |

**Total**: ~243 `pulumi` CLI invocations across 11 justfiles, ~11k LOC total.

**Fragile parsing hotspots**:
- `pulumi stack ls --json | jq -e 'any(.[]; .name == $s)'` (6 occurrences) — stack existence check.
- `pulumi -C <folder> stack output <key> --stack <stack>` (6 occurrences) — namespace/value reads from namespace-bootstrap.
- `pulumi -C <folder> config get --path <key>` (nats.justfile) — config validation.
- Exit code checks for `stack select` (ensure-stack idempotency).

## Design

### Package structure

`internal/pulumiauto` — Automation API wrappers bound to the registry.

```go
package pulumiauto

import (
    "context"
    "github.com/pulumi/pulumi/sdk/v3/go/auto"
    "github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
    "github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
    "github.com/IBM/fp-go/v2/ioeither"
    "log/slog"
)

// Workspace bound to a registry entry (backend URL, secrets provider, stack name).
type Workspace struct {
    entry    registry.Entry
    backend  string
    secrets  string
    folder   string
    stack    string
    logger   *slog.Logger
}

// EnsureStack selects or initializes a stack (idempotent).
func EnsureStack(ctx context.Context, ws Workspace) IOE.IOEither[error, auto.Stack]

// SetConfig writes a config key (plaintext or secret).
func SetConfig(ctx context.Context, stack auto.Stack, key string, value string, secret bool) IOE.IOEither[error, struct{}]

// Preview runs preview and returns structured diff summary (create/update/delete/replace counts).
func Preview(ctx context.Context, stack auto.Stack) IOE.IOEither[error, PreviewSummary]

type PreviewSummary struct {
    Creates  int
    Updates  int
    Deletes  int
    Replaces int
    Same     int
}

// Up runs update with event streaming to slog.
func Up(ctx context.Context, stack auto.Stack, logger *slog.Logger) IOE.IOEither[error, auto.UpResult]

// StackOutput reads an output key.
func StackOutput(ctx context.Context, stack auto.Stack, key string) IOE.IOEither[error, string]

// BootstrapService: composite preflight → ensure → set-config → preview → up → rollout.
func BootstrapService(ctx context.Context, ws Workspace, folder string, imageTag string) IOE.IOEither[error, DeployResult]
```

### Workspace construction

Bind workspace to registry entry (read from `config/clusters/<cluster>.yaml`):

```go
entry := registry.LoadByStack(stackName)
backend := entry.PulumiState // "gs://pulumi-state-<project>" or "file://pulumi-files/local-state/"
secrets := entry.KmsSecretsProvider // "gcpkms://..." or "passphrase"

ws, err := auto.NewLocalWorkspace(ctx,
    auto.WorkDir(folder),
    auto.Project(auto.Project{Name: project}),
    auto.PulumiHome(os.Getenv("PULUMI_HOME")), // respect existing install
    auto.EnvVars(map[string]string{
        "PULUMI_BACKEND_URL": backend,
        "PULUMI_CONFIG_PASSPHRASE": passphrase, // for local stacks
        "GOOGLE_APPLICATION_CREDENTIALS": keyfile,
    }),
    auto.SecretsProvider(secrets),
)
stack, err := auto.SelectStackLocalSource(ctx, stackName, ws)
// or auto.UpsertStackLocalSource if creating
```

**Config files stay committed**: `Pulumi.<stack>.yaml` in each folder; the Automation API reads these exactly as the CLI does. No programmatic generation; the file commit stays manual.

### Preview with structured diff

Parse `preview` events to count creates/updates/deletes/replaces:

```go
type PreviewSummary struct {
    Creates  int
    Updates  int
    Deletes  int
    Replaces int
    Same     int
}

func Preview(ctx context.Context, stack auto.Stack) IOE.IOEither[error, PreviewSummary] {
    // stack.Preview(ctx, optpreview.Diff(), optpreview.EventStreams(...))
    // Parse event stream for resource steps, count by type
}
```

**Protected resource gate**: refuse `--yes` when preview shows a delete/replace hitting a protected class (PersistentVolumeClaim, StatefulSet, Database). Require explicit operator confirmation.

### Event streaming to slog

Pulumi Automation API emits events on stdout/stderr or via `optup.EventStreams`. Filter and log to slog:

```go
func Up(ctx context.Context, stack auto.Stack, logger *slog.Logger) IOE.IOEither[error, auto.UpResult] {
    eventChan := make(chan auto.EngineEvent)
    go func() {
        for event := range eventChan {
            switch event.Type {
            case "diagnostic":
                logger.Debug("pulumi diagnostic", "msg", event.DiagnosticEvent.Message)
            case "resource-pre":
                logger.Info("resource step", "urn", event.ResourcePreEvent.Metadata.URN, "op", event.ResourcePreEvent.Metadata.Op)
            // Filter out decrypted secret values before logging
            }
        }
    }()
    result, err := stack.Up(ctx, optup.EventStreams(eventChan), optup.ProgressStreams(os.Stdout))
    // return wrapped result
}
```

**Secret leakage risk**: event stream can contain decrypted config values. Filter diagnostic messages containing `secret` before logging.

### Concurrency and locking

**Default: serial**. `deploy-backend-services` deploys one service at a time (loop over `config/services.yaml`). No locks needed.

**Parallel future**: if parallel stack deploys become needed, use per-stack locks:

```go
type StackLock struct {
    mu sync.Mutex
    locks map[string]*sync.Mutex
}

func (sl *StackLock) Acquire(stackName string) {
    sl.mu.Lock()
    if sl.locks[stackName] == nil {
        sl.locks[stackName] = &sync.Mutex{}
    }
    lock := sl.locks[stackName]
    sl.mu.Unlock()
    lock.Lock()
}
```

Pulumi CLI uses GCS-backed state locks; Automation API acquires these same locks. No additional application-level locking needed unless deploying to the same stack concurrently from multiple processes.

### Deterministic ordering

`pulumi-files/resources/initial-resources.txt`, `database-and-storage-resources.txt` list projects in StackReference dependency order:

```
namespace-bootstrap
storage_class
arangodb-operator
arangodb-single
create-arangodb-databases
...
```

Deploy loop reads these files, deploys in order:

```go
func DeployResourceList(ctx context.Context, listFile string, stack string) IOE.IOEither[error, []string] {
    projects := readLines(listFile) // ["namespace-bootstrap", "arangodb-operator", ...]
    return Arr.TraverseIOEither(func(project string) IOE.IOEither[error, string] {
        ws := newWorkspace(project, stack)
        return F.Pipe3(
            EnsureStack(ctx, ws),
            IOE.Chain(func(s auto.Stack) IOE.IOEither[error, auto.UpResult] { return Up(ctx, s, logger) }),
            IOE.Map(func(_ auto.UpResult) string { return project }),
        )
    })(projects)
}
```

## Target flows

### CLI subcommands

- `cluster-ops stack ensure --folder <dir> --stack <name>` → `EnsureStack`
- `cluster-ops stack config set --folder <dir> --stack <name> --key <path> --value <val> [--secret]` → `SetConfig`
- `cluster-ops stack preview --folder <dir> --stack <name>` → `Preview` (prints summary)
- `cluster-ops stack up --folder <dir> --stack <name>` → `Up`
- `cluster-ops stack outputs --folder <dir> --stack <name> --key <name>` → `StackOutput`
- `cluster-ops deploy backend --stack <name> --services <a,b>` → `DeployBackendServices`

### Recipe wrappers

Keep `just gcp-pulumi <cmd>` as thin wrappers:

```justfile
ensure-stack folder stack="":
    go run ./cmd/cluster-ops stack ensure --folder {{ quote(folder) }} --stack {{ quote(stack) }}

bootstrap-service folder stack="" image_tag="":
    go run ./cmd/cluster-ops deploy service --folder {{ quote(folder) }} --stack {{ quote(stack) }} --image-tag {{ quote(image_tag) }}
```

**`Next: <recipe>` echoes stay** in shell recipes (user rule from CLAUDE.md).

## Phases

### Phase 0: Foundation (shared with mandatory categories)

- `internal/registry` (LoadByName/LoadByStack) — done in phase-1 of mandatory categories.
- `internal/preflight` (Result/Status/Run/Print) — done in phase-1 of mandatory categories.
- `internal/waitfor` (wait-poll plan) — done after port-forward plan.
- CLI base in `cmd/cluster-ops/main.go` (urfave/cli v2) — done in phase-1 of mandatory categories.

### Phase 1: Pilot (bootstrap-service + deploy-backend-services)

**Scope**: migrate the two composite recipes that orchestrate stack scaffolding → prerequisite gate → deploy → rollout check.

**Files**:
- `internal/pulumiauto/workspace.go` — `NewWorkspace`, `EnsureStack`.
- `internal/pulumiauto/config.go` — `SetConfig`, `SetSecret`.
- `internal/pulumiauto/preview.go` — `Preview`, `PreviewSummary`.
- `internal/pulumiauto/up.go` — `Up` with event streaming.
- `internal/pulumiauto/output.go` — `StackOutput`.
- `internal/pulumiauto/bootstrap.go` — `BootstrapService` composite flow.
- `cmd/cluster-ops/stack.go` — `stack ensure|config|preview|up|outputs` subcommands.
- `cmd/cluster-ops/deploy.go` — `deploy backend` subcommand (calls `BootstrapService` per service).

**Testing**:
- Unit tests with faked Automation API (interface wrapping `auto.Stack`).
- Integration tests (`//go:build integration`) against local filesystem backend (`pulumi-files/local-state/`, passphrase provider, k3d cluster).
- Shell contract tests (`scripts/test-*.sh`) keep ORDER assertions by mocking `go` on PATH.

**Acceptance**:
- `just ci deploy-backend-services --stack local --services modware-annotation` deploys to k3d using the Go implementation.
- `just gcp-pulumi bootstrap-service --folder modware-stock --stack experiments --image-tag v1.2.3` deploys to kops cluster.
- Preview shows create/update/delete/replace counts.
- Event stream logs to slog (no raw Pulumi TUI output in CI logs).
- `just check` passes (contract tests assert preflight → ensure-stack → preview → update → rollout order).

**Rollback**: keep shell recipes as `*-shell.justfile` backups; rename back if Go impl fails.

### Phase 2: Datastore deploys (arangodb, postgres, redis, minio, nats, logto)

**Scope**: migrate the 6 datastore deploy recipes that read `pulumi stack output` and orchestrate operator → instance → backup flows.

**Files**:
- `internal/pulumiauto/datastore.go` — `DeployArangoDB`, `DeployPostgres`, `DeployRedis`, etc. (composite flows).
- `cmd/cluster-ops/datastore.go` — `datastore deploy arangodb|postgres|redis|minio|nats|logto` subcommands.

**Acceptance**:
- `just arangodb deploy-local-arangodb --stack local` works via Go.
- `pulumi stack output appNamespace` reads stay (no migration needed; shell captures still work).

### Phase 3: Remaining orchestration recipes

**Scope**: migrate `create-multiple-resources`, `scaffold-backend-stack`, `new-stack-from`.

**Acceptance**:
- `just gcp-pulumi create-multiple-resources --stack prod --from-stack experiments --resources-file pulumi-files/resources/initial-resources.txt` deploys in order via Go.

## Acceptance

- `cluster-ops stack ensure|config|preview|up|outputs` subcommands work (replace `just gcp-pulumi <cmd>`).
- `cluster-ops deploy backend --stack <name> --services <a,b>` replaces `just ci deploy-backend-services`.
- Preview shows structured diff summary (create/update/delete/replace counts).
- Event logs filtered to slog (no secret leakage).
- `just check` passes (contract tests assert order).
- Integration tests against local k3d stacks pass.
- Shell recipes become thin wrappers `go run ./cmd/cluster-ops ...`.
- Docs updated: `docs/reference/backend/pulumi-automation.md` (Automation API usage), flag tables in existing reference docs.

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| **CLI version drift** — Automation API SDK v3.266.0 in go.mod vs CLI binary `pulumi_version` in registry (e.g. 3.108.0) | Medium | Pin SDK to match registry version; update both together; version check in `cluster-ops --version` |
| **Plugin install breaks** — Automation API downloads plugins; version skew vs CLI-installed plugins | Low | Automation API uses same plugin cache as CLI (`~/.pulumi/plugins`); no extra install |
| **State lock contention** — parallel deploys to same stack from multiple processes | Low | Serial-by-default; parallel stacks (different names) are fine; same-stack parallel needs app-level lock |
| **Secret leakage in logs** — event stream contains decrypted config values | Medium | Filter diagnostic messages containing `secret` keyword before slog; use `--show-secrets=false` equivalent |
| **Preview diff parsing fragile** — event stream schema changes across Pulumi versions | Medium | Version-pin SDK to tested version; integration tests catch schema changes |
| **Config file writes** — programmatic `SetConfig` writes to `Pulumi.<stack>.yaml`; conflicts with manual edits | Low | Automation API writes atomic; git detects conflicts; docs say "commit before re-running" |
| **PULUMI_HOME mismatch** — SDK uses different Pulumi home than CLI | Low | Respect `PULUMI_HOME` env var in `auto.NewLocalWorkspace` |
| **Passphrase provider env** — `PULUMI_CONFIG_PASSPHRASE` must be set for local stacks | Low | Recipes already set this (`pass show pulumi/local-passphrase`); Automation API reads same env |

## Cost-benefit

### Costs

- **Go code to maintain**: ~800 LOC in `internal/pulumiauto`, ~300 LOC in `cmd/cluster-ops` subcommands (vs ~800 LOC shell recipes).
- **Testing complexity**: Automation API needs interface wrappers for fakes (vs shell contract tests mocking `pulumi` on PATH).
- **Version pinning**: SDK version must match CLI version in registry; two places to update.
- **Learning curve**: operators must learn `cluster-ops` CLI flags (vs `just gcp-pulumi <cmd>` which wraps `pulumi` CLI).
- **CLI still needed**: Automation API calls the CLI binary; no reduction in deploy dependencies.
- **Config files stay**: `Pulumi.<stack>.yaml` remain committed source of truth; no programmatic generation benefit.

### Benefits

- **Structured preview diffs**: create/update/delete/replace counts; refuse `--yes` on protected resource changes.
- **Event filtering**: slog-structured logs, no raw Pulumi TUI in CI; secret filtering.
- **Idempotency in Go**: describe-then-create patterns move from shell+jq to fp-go IOEither.
- **Better composition**: composite flows (`bootstrap-service` = preflight → ensure → preview → up → rollout) as Go functions, not shell recipe chains.
- **Testability**: integration tests against local k3d stacks (passphrase provider, file backend) without mocking `pulumi` binary.
- **Parallel future**: per-stack locks enable parallel deploys when needed (not today).

### Honest assessment

**CLI still required**: Automation API wraps the CLI; the binary must be present, versioned, and in PATH. This is **not** a "remove Pulumi CLI" migration.

**Config files stay committed**: `Pulumi.<stack>.yaml` remain the source of truth because CI deploys (`dictybase/workflows`) clone `cluster-ops` develop and read these files. Programmatic config writes happen, but the file commit is manual. Automation API does not remove this step.

**Version pinning**: registry owns `pulumi_version` (e.g. "3.108.0"). CLI binary must match. SDK in go.mod must match or stay within one minor. Two places to keep in sync. The benefit is **explicit versioning**, not simplification.

**When this is worth it**: if deploy flows keep growing beyond `bootstrap-service` + `deploy-backend-services`, and preview diff parsing / event filtering / parallel stacks become blockers. If the shell recipes stay ~60 and stable, **do not** migrate. The shell works.

## Recipes left in shell

| Recipe | Reason |
|--------|--------|
| `pulumi-gcs-setup` | One-time bootstrap; describe-then-create GCS bucket logic stays shell |
| `pulumi-local-setup` | Thin wrapper `pulumi login file://...`; no benefit in Go |
| `check-backend` | Shell script already exists (`scripts/pulumi/check-backend.sh`); reads PULUMI_* env and probes GCS/KMS; belongs in `internal/preflight` not `pulumiauto` |
| `check-tools` | Shell script (`scripts/pulumi/check-tools.sh`); version probes for `pulumi`, `kubectl`, `kops`; belongs in `internal/preflight` |
| `fork-stack` | Copies `Pulumi.<from>.yaml` to `Pulumi.<to>.yaml` in every project; pure file copy; no Automation API needed |
| One-off admin recipes | `restore-arangodb`, `backup-arangodb`, `export-arangodb-data` — infrequent; shell is fine |

**Do not migrate** recipes that are:
- One-time setup (bootstrap-backend, GCS bucket create).
- Thin CLI wrappers with no orchestration (`just gcp-pulumi preview` → `pulumi preview`).
- File manipulation (`fork-stack` copying YAML files).
- Infrequent admin tasks (restores, backups).

Only migrate:
- Composite flows (bootstrap-service, deploy-backend-services).
- Recipes with fragile parsing (`pulumi stack ls --json | jq`, `stack output` captures).
- Recipes that would benefit from structured events (preview diffs, up progress).
