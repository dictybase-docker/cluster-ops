# Plan: GCP Resource Converge Migration

**Status**: Proposed

## Table of Contents
- [Goal](#goal)
- [Non-goals](#non-goals)
- [Current state](#current-state)
  - [Existing Go converge implementations](#existing-go-converge-implementations)
  - [Shell mutation inventory](#shell-mutation-inventory)
- [Design](#design)
  - [Converge pattern](#converge-pattern)
  - [IAM policy mutations](#iam-policy-mutations)
  - [Service account key lifecycle](#service-account-key-lifecycle)
  - [Dry-run mode](#dry-run-mode)
- [Migration targets](#migration-targets)
  - [Phase 1: Foundation (completed)](#phase-1-foundation-completed)
  - [Phase 2: Bucket lifecycle](#phase-2-bucket-lifecycle)
  - [Phase 3: IAM bindings](#phase-3-iam-bindings)
  - [Phase 4: Service account setup flows](#phase-4-service-account-setup-flows)
  - [Deferred: gcloud config/auth UX](#deferred-gcloud-configauth-ux)
- [Implementation details](#implementation-details)
  - [Bucket converge](#bucket-converge)
  - [IAM binding converge](#iam-binding-converge)
  - [Cross-project bucket reader grant](#cross-project-bucket-reader-grant)
  - [Recipe invocation changes](#recipe-invocation-changes)
- [Acceptance criteria](#acceptance-criteria)
- [Risks](#risks)
- [Related plans](#related-plans)

## Goal

Migrate GCP resource mutations from shell (`gcloud`/`gsutil`) to idempotent Go converge functions. Every mutation must be safe to re-run: describe-then-create/update, proper error classification (NotFound/AlreadyExists/PermissionDenied), IAM propagation retry where needed, no `|| true` swallowing, and explicit identity binding from the registry.

## Non-goals

- **Authentication UX**: `gcloud auth`, `gcloud config`, `configure-gcloud` remain shell. They configure ambient state; converge logic reads explicit credentials.
- **Read-only queries**: `gcloud compute instances list`, `gcloud storage ls` (inspection/troubleshooting commands) stay shell.
- **Image operations**: `gcloud compute machine-images create/describe` (image.justfile) — low priority, container builds are separate.
- **Pulumi Automation API**: covered by sibling plan.
- **kubectl/kops**: not GCP SDK resources.

## Current state

### Existing Go converge implementations

Four categories already in Go (`internal/gcp/`, `cmd/cluster-ops`):

| Resource | CLI Command | File | Pattern | Notes |
|----------|-------------|------|---------|-------|
| Service account | `sa create` | `service_account.go:32` | Imperative (not fp-go) | Describe → create, role assignment, key creation |
| SA key | `sa create-key` | `service_account.go:156` | Imperative | Key creation only; list existing keys for audit trail |
| KMS keyring+key | `kms create` | `kms.go:32` | **fp-go converge** | `IOE.WithResource`, probe-create pattern, proper grpc status codes |
| API enable/disable | `api enable`/`disable` | `api.go:15,73` | Imperative lists | Batched service enable/disable |
| Kops state bucket | `bucket create` | `kops_state_bucket.go:14` | Imperative | Create bucket, enable versioning |
| Roles/permissions | (lib only) | `roles_permissions.go` | Helpers | Role file parsing |

**Current invocation**: `go build -o bin/cluster-ops && ./bin/cluster-ops <cmd>` (sa.justfile:47, kms.justfile:52, cluster.justfile:186). User rule: switch to `go run ./cmd/cluster-ops/`.

**Code quality gap**: `service_account.go` is imperative (line 32–96); `kms.go` shows the fp-go pattern (`ensureKeyring`/`ensureKey` at 78–134). Plan includes bringing touched code to style.

### Shell mutation inventory

147 gcloud/gsutil calls across 14 files (~11k lines). Breakdown by operation:

#### Service account operations (18 mutations)
| Recipe | File:Line | Operation | Current Idempotency |
|--------|-----------|-----------|---------------------|
| `setup-backup-sa` (arangodb) | arangodb.justfile:1706–1733 | SA create + role bind + key mint | `if describe; then …; gcloud create …` |
| `grant-source-bucket-reader` | arangodb.justfile:2065–2090 | Cross-project SA + bucket IAM bind | `if describe; then …; gcloud create …` |
| `setup-backup-sa` (postgres) | postgres.justfile:187–222 | SA create + role bind + key mint | `if describe; then …; gcloud create …` |
| `setup-source-reader` | postgres.justfile:417–438 | Cross-project SA + bucket IAM bind | `if describe; then …; gcloud create …` |
| `setup-backup-sa` (redis) | redis.justfile:344–370 | SA create + role bind + key mint | `if describe; then …; gcloud create …` |
| `create-deploy-key` (ci) | ci.justfile:396 | SA describe check (guard) | Describe only |
| `sa-accounts-setup` | sa.justfile:190–207 | SA create + key mint | `if describe; gcloud create` |
| `create-hmac` | sa.justfile:139 | HMAC key creation | No converge (generates new key) |

**Pattern**: all follow `if gcloud iam service-accounts describe; then … gcloud create …`. **Gap**: key minting is non-idempotent (creates new key every run if key file missing). **Already Go-backed**: `sa create` (service_account.go:32) is imperative.

#### IAM policy bindings (17 mutations)
| Recipe | File:Line | Operation | Current Idempotency |
|--------|-----------|-----------|---------------------|
| `add-role-to-sa` | role.justfile:51 | Project-level IAM bind | Direct `add-iam-policy-binding` (idempotent at GCP API level) |
| `assign-roles-to-sa` | role.justfile:94 | Batch role assignment | Loop calling `add-iam-policy-binding` |
| `setup-backup-sa` (arangodb) | arangodb.justfile:1719 | Project IAM bind | Direct bind |
| `grant-source-bucket-reader` | arangodb.justfile:2076 | **Bucket-level IAM bind** | `storage buckets add-iam-policy-binding` |
| `setup-backup-sa` (postgres) | postgres.justfile:204,208 | Project IAM bind (2 roles) | Direct bind |
| `setup-source-reader` | postgres.justfile:428 | **Bucket-level IAM bind** | `storage buckets add-iam-policy-binding` |
| `setup-backup-sa` (redis) | redis.justfile:356 | Project IAM bind | Direct bind |

**Pattern**: `gcloud projects add-iam-policy-binding` for project, `gcloud storage buckets add-iam-policy-binding` for buckets. **Idempotency**: GCP API is idempotent (adding existing binding = noop), but no etag handling (race-prone under concurrent updates). **Gap**: no structured logging of what changed.

#### Storage bucket operations (11 mutations)
| Recipe | File:Line | Operation | Current Idempotency |
|--------|-----------|-----------|---------------------|
| `pulumi-gcs-setup` | pulumi.justfile:56–67 | Create bucket, enable versioning, set lifecycle | `if ! describe; then create; update versioning; update lifecycle` |
| `create-state-bucket` | cluster.justfile:186 | Create kops state bucket | Calls Go `bucket create` (kops_state_bucket.go:14) |
| `delete-state-bucket` | cluster.justfile:646 | **Destructive**: recursive delete | Preflight checks (637–644); `storage rm --recursive` |
| `grant-source-bucket-reader` | arangodb.justfile:2049 | Bucket existence check + describe | `if ! describe … >&2 echo …; exit 1` (guard, not mutation) |

**Pattern**: describe-then-create for bucket, update for versioning/lifecycle. **Already Go-backed**: `create-state-bucket` → `bucket create` (Go). **Destructive**: `delete-state-bucket` validates object count, prompts for confirmation.

#### gcloud config/auth (29 ops, **deferred**)
| Recipe | File:Line | Operation | Purpose |
|--------|-----------|-----------|---------|
| `configure-gcloud` | cluster.justfile:1080–1135 | Named config create + auth activate-service-account + set project/zone | Identity activation UX |
| `cleanup-gcloud-config` | cluster.justfile:671–683 | Delete named configs | Cleanup |
| `bootstrap-identities` | cluster.justfile:1220 | Composite: create SAs, activate configs | Orchestration |
| `rotate-to-manager/creator` | cluster.justfile:1267,1304 | Switch active config | Identity rotation |

**Non-goal**: these configure ambient state (OS config files), not cloud resources. Keep shell.

#### Remaining mutations
| Recipe | File:Line | Operation | Category |
|--------|-----------|-----------|----------|
| `create-image-from-instance` | image.justfile:28,34 | Machine image create + describe | Image ops (deferred) |
| `delete-machine-image` | image.justfile:52 | Machine image describe | Image ops (deferred) |
| `delete-backup` | postgres.justfile:516 | **Destructive**: `storage rm -r` | Backup lifecycle (low priority) |
| `kms decrypt` | cluster.justfile:2143 | Decrypt token (read-only, base64 pipe) | Read-only (keep shell) |

**Summary**: 18 SA mutations, 17 IAM bindings, 11 bucket ops, 29 config/auth (deferred), 4 other. **Already Go-backed**: 6 recipes (SA create/create-key, KMS create, API enable/disable, bucket create). **Target for migration**: ~40 mutations (SA setup flows, IAM bindings, bucket lifecycle).

## Design

### Converge pattern

Reusable fp-go pipeline for `get → classify → create|update|noop → verify`:

```go
// Generic converge signature (sketch — real implementations are resource-specific)
type ConvergeParams[T any] struct {
    ResourceName string
    Desired      T
    ProjectID    string
    Logger       *slog.Logger
}

type ConvergeResult struct {
    Action   string // "created" | "updated" | "unchanged"
    Resource any
}

// Pattern: IOE.IOEither[error, ConvergeResult] pipeline
// 1. Describe (probe)
// 2. Classify response (NotFound → create, Found → compare/update, other → fail)
// 3. Create/update/noop
// 4. Verify (re-describe)
// 5. Log action via IOE.ChainFirstIOK

func ConvergeResource[T any](
    params ConvergeParams[T],
    describe func(string) IOE.IOEither[error, T],
    create func(T) IOE.IOEither[error, T],
    update func(T, T) IOE.IOEither[error, T], // (current, desired)
) IOE.IOEither[error, ConvergeResult] {
    return F.Pipe(
        describe(params.ResourceName),
        IOE.Fold(
            classifyError(params, create),        // NotFound → create
            compareAndUpdate(params, update),     // Found → compare
        ),
        IOE.ChainFirstIOK(logAction(params.Logger)),
    )
}

func classifyError[T any](
    params ConvergeParams[T],
    create func(T) IOE.IOEither[error, T],
) func(error) IOE.IOEither[error, ConvergeResult] {
    return func(err error) IOE.IOEither[error, ConvergeResult] {
        if isNotFound(err) {
            return F.Pipe(
                create(params.Desired),
                IOE.Map(func(r T) ConvergeResult {
                    return ConvergeResult{Action: "created", Resource: r}
                }),
            )
        }
        return IOE.Left[ConvergeResult](err)
    }
}

// Error classification (grpc codes for KMS/IAM, googleapi.Error for REST APIs)
func isNotFound(err error) bool {
    if st, ok := status.FromError(err); ok {
        return st.Code() == codes.NotFound
    }
    if apiErr, ok := err.(*googleapi.Error); ok {
        return apiErr.Code == http.StatusNotFound
    }
    return false
}
```

**Key features**:
- **Explicit identity binding**: `ProjectID` from registry or cluster-env, never `$GOOGLE_CLOUD_PROJECT`.
- **Structured error classification**: grpc `codes.NotFound` (KMS/Storage), `googleapi.Error` 404 (IAM/CRM).
- **Retry policy reuse**: existing `R.Monoid.Concat(R.LimitRetries(5), R.ExponentialBackoff(100ms, 2.0))` for IAM propagation (service_account.go:175).
- **Dry-run mode**: `--dry-run` flag skips mutations, prints intended action.

### IAM policy mutations

IAM `setIamPolicy` requires read-modify-write with etag (optimistic concurrency):

```go
// IAM policy binding converge (project-level example; bucket-level similar)
type IAMBindingParams struct {
    ProjectID string
    Member    string // "serviceAccount:foo@project.iam.gserviceaccount.com"
    Role      string // "roles/storage.admin"
    Logger    *slog.Logger
}

func ConvergeProjectIAMBinding(
    params IAMBindingParams,
) IOE.IOEither[error, ConvergeResult] {
    return F.Pipe(
        getProjectIAMPolicy(params.ProjectID),
        IOE.Chain(func(policy *cloudresourcemanager.Policy) IOE.IOEither[error, ConvergeResult] {
            binding := findOrCreateBinding(policy, params.Role)
            if slices.Contains(binding.Members, params.Member) {
                return IOE.Of[error](ConvergeResult{Action: "unchanged"})
            }
            binding.Members = append(binding.Members, params.Member)
            return F.Pipe(
                setProjectIAMPolicy(params.ProjectID, policy), // Uses etag
                IOE.Map(func(_ *cloudresourcemanager.Policy) ConvergeResult {
                    return ConvergeResult{Action: "updated"}
                }),
            )
        }),
        IOE.ChainFirstIOK(logIAMAction(params.Logger)),
    )
}

func getProjectIAMPolicy(projectID string) IOE.IOEither[error, *cloudresourcemanager.Policy] {
    return func() E.Either[error, *cloudresourcemanager.Policy] {
        ctx := context.Background()
        svc, err := cloudresourcemanager.NewService(ctx)
        if err != nil {
            return E.Left[*cloudresourcemanager.Policy](err)
        }
        req := &cloudresourcemanager.GetIamPolicyRequest{}
        policy, err := svc.Projects.GetIamPolicy(projectID, req).Context(ctx).Do()
        return E.FromError(policy, err)
    }
}

// Bucket-level: use storage.BucketsService.GetIamPolicy/SetIamPolicy
// Member can include conditions (IAM condition expressions) — not covered in phase 1
```

**Concurrency**: etag prevents lost updates. If `setIamPolicy` returns 409 (etag mismatch), retry the read-modify-write loop. **Propagation**: IAM bindings propagate instantly (no retry needed); SA **creation** needs propagation retry (existing pattern in service_account.go:175).

### Service account key lifecycle

**Non-idempotency constraint**: `iam.projects.serviceAccounts.keys.create` generates a **new** key every call. Idempotent behavior requires:
1. **List existing keys** before minting.
2. **Reuse existing key file** if present and corresponds to a live key.
3. **Mint only when needed** (file missing or key deleted upstream).
4. **Audit log**: key ID, creation time, key file path.

```go
func ConvergeSAKey(
    saEmail string,
    keyFilePath string,
) IOE.IOEither[error, ConvergeResult] {
    return F.Pipe(
        checkExistingKeyFile(keyFilePath),
        IOE.Chain(func(exists bool) IOE.IOEither[error, ConvergeResult] {
            if exists {
                // Validate key file corresponds to a live key upstream
                return F.Pipe(
                    validateKeyFile(saEmail, keyFilePath),
                    IOE.Map(func(valid bool) ConvergeResult {
                        if valid {
                            return ConvergeResult{Action: "unchanged"}
                        }
                        return ConvergeResult{Action: "invalid"} // Trigger re-mint
                    }),
                )
            }
            // Mint new key
            return F.Pipe(
                createSAKey(saEmail, keyFilePath),
                IOE.Map(func(_ string) ConvergeResult {
                    return ConvergeResult{Action: "created"}
                }),
            )
        }),
    )
}
```

**Security**: keys never echoed to stdout/logs. **Audit trail**: `gcloud iam service-accounts keys list --iam-account <email>` output logged (key IDs, no secrets).

### Dry-run mode

Every converge command accepts `--dry-run`:
- **Describe**: executes (read-only).
- **Create/update**: **skipped**, prints intended action to stdout.
- **Output**: JSON summary of planned changes.

```go
type DryRunAction struct {
    Resource string
    Action   string // "create" | "update" | "unchanged"
    Details  map[string]any
}

// In converge pipeline:
if cliCtx.Bool("dry-run") {
    action := DryRunAction{
        Resource: "bucket",
        Action:   "create",
        Details:  map[string]any{"name": bucketName, "location": location},
    }
    fmt.Fprintln(os.Stderr, "[DRY RUN]", action.Resource, action.Action)
    fmt.Println(toJSON(action))
    return IOE.Of[error](ConvergeResult{Action: "dry-run"})
}
```

## Migration targets

### Phase 1: Foundation (completed)

Shared by all sibling plans (checks/port-forward/wait-poll/gcp-converge):
- `internal/registry`: cluster config loader, `LoadByName/LoadByStack` → `E.Either[error, Entry]`.
- `internal/preflight`: post-converge verification result printer.
- `internal/waitfor`: long-running operation waiter (owned by wait-poll plan).
- Testing: testify/require, GCP client interfaces + fakes, googleapi.Error fixtures, optional `//go:build integration`.
- Code style: fp-go v2, flat `F.Pipe`, typed slog loggers via `IOE.ChainFirstIOK` returning `struct{}{}`.

### Phase 2: Bucket lifecycle

**Scope**: bucket create/update (versioning, lifecycle), bucket delete preflight.

| Recipe | Current (shell) | Target (Go) | Notes |
|--------|-----------------|-------------|-------|
| `pulumi-gcs-setup` | pulumi.justfile:56–67 | `cluster-ops gcp bucket converge` | Bucket + versioning + lifecycle in one converge |
| `create-state-bucket` | cluster.justfile:186 (calls Go) | Extend `bucket create` | Already Go, add versioning/lifecycle |
| `delete-state-bucket` | cluster.justfile:646 | `cluster-ops gcp bucket delete` | Preflight: check object count, require `--confirm yes` |

**Design**:
```go
type BucketConfig struct {
    Name           string
    ProjectID      string
    Location       string
    Versioning     bool
    LifecycleRules []storage.LifecycleRule
}

// CLI: cluster-ops gcp bucket converge --name <bucket> --project <id> --versioning --lifecycle-file <path>
func ConvergeBucket(cfg BucketConfig) IOE.IOEither[error, ConvergeResult] {
    // 1. Describe bucket
    // 2. Create if NotFound
    // 3. Update versioning if changed
    // 4. Update lifecycle if changed
    // 5. Verify + log
}

// CLI: cluster-ops gcp bucket delete --name <bucket> --confirm yes
func DeleteBucket(name, projectID string, confirmed bool) IOE.IOEither[error, struct{}] {
    // Preflight: list objects, count, prompt if not confirmed
    // Destructive: storage.Buckets.Delete (or recursive rm via client)
}
```

**Acceptance**: `just check` contract test mocks `go` on PATH, asserts `cluster-ops gcp bucket converge` called with correct args. Re-run converge = "unchanged" action.

### Phase 3: IAM bindings

**Scope**: project-level + bucket-level IAM bindings.

| Recipe | Current (shell) | Target (Go) | Notes |
|--------|-----------------|-------------|-------|
| `add-role-to-sa` | role.justfile:51 | `cluster-ops gcp iam add-binding` | Project-level, single binding |
| `assign-roles-to-sa` | role.justfile:94 | Batch call to `add-binding` | Loop in shell or Go batch API |
| Bucket IAM bindings (6 calls) | arangodb/postgres.justfile:2076,428 | `cluster-ops gcp bucket add-iam-binding` | Bucket-level |

**Design**:
```go
// CLI: cluster-ops gcp iam add-binding --project <id> --member <member> --role <role>
func ConvergeProjectIAMBinding(
    projectID, member, role string,
) IOE.IOEither[error, ConvergeResult] {
    // Read policy, find/add binding, setIamPolicy with etag
}

// CLI: cluster-ops gcp bucket add-iam-binding --bucket <name> --member <member> --role <role>
func ConvergeBucketIAMBinding(
    bucketName, member, role string,
) IOE.IOEither[error, ConvergeResult] {
    // Bucket policy read-modify-write
}
```

**Etag handling**: retry loop on 409, max 3 attempts. **Conditions**: IAM condition expressions deferred (add `--condition` flag later).

### Phase 4: Service account setup flows

**Scope**: composite recipes (SA create + role bind + key mint).

| Recipe | Current (shell) | Target (Go) | Notes |
|--------|-----------------|-------------|-------|
| `setup-backup-sa` (arangodb) | arangodb.justfile:1706–1733 | `cluster-ops gcp sa setup-backup` | SA + 1 role + key |
| `setup-backup-sa` (postgres) | postgres.justfile:187–222 | Same | SA + 2 roles + key |
| `setup-backup-sa` (redis) | redis.justfile:344–370 | Same | SA + 1 role + key |
| `grant-source-bucket-reader` | arangodb.justfile:2065–2090 | `cluster-ops gcp sa setup-cross-project-reader` | Cross-project SA + bucket IAM + key |
| `setup-source-reader` | postgres.justfile:417–438 | Same | Cross-project |

**Design**:
```go
type BackupSAConfig struct {
    ProjectID   string
    SAName      string
    DisplayName string
    Roles       []string
    KeyFilePath string
}

// CLI: cluster-ops gcp sa setup-backup --project <id> --name <sa> --roles-file <path> --key-file <out>
func SetupBackupSA(cfg BackupSAConfig) IOE.IOEither[error, struct{}] {
    // 1. Converge SA (create if missing)
    // 2. Converge each role binding
    // 3. Converge key (mint if file missing/invalid)
    // Uses existing CreateServiceAccount (refactor to fp-go style)
}
```

**Refactor existing Go code**: `service_account.go:CreateServiceAccount` (imperative) → fp-go `ConvergeSA` pipeline. **Key mint idempotency**: use `ConvergeSAKey` design above.

### Deferred: gcloud config/auth UX

`configure-gcloud`, `cleanup-gcloud-config`, `bootstrap-identities`, `rotate-to-manager/creator` remain shell. They configure ambient `~/.config/gcloud/`, not cloud resources. Converge commands read explicit credentials (`--credentials <path>` or `GOOGLE_APPLICATION_CREDENTIALS`).

## Implementation details

### Bucket converge

```go
package gcp

import (
    "context"
    "fmt"
    "cloud.google.com/go/storage"
    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
    "google.golang.org/api/option"
)

type BucketConvergeParams struct {
    Name           string
    ProjectID      string
    Location       string
    Versioning     bool
    LifecycleRules []storage.LifecycleRule
    CredFile       string
}

func ConvergeBucket(params BucketConvergeParams) IOE.IOEither[error, ConvergeResult] {
    acquire := newStorageClient(params.CredFile)
    use := ensureBucket(params)
    return F.Pipe1(use, IOE.WithResource(acquire, closeStorageClient))
}

func ensureBucket(
    params BucketConvergeParams,
) func(*storage.Client) IOE.IOEither[error, ConvergeResult] {
    return func(client *storage.Client) IOE.IOEither[error, ConvergeResult] {
        bucket := client.Bucket(params.Name)
        return F.Pipe(
            describeBucket(bucket),
            IOE.Fold(
                func(err error) IOE.IOEither[error, ConvergeResult] {
                    if isNotFound(err) {
                        return createBucket(client, params)
                    }
                    return IOE.Left[ConvergeResult](err)
                },
                func(attrs *storage.BucketAttrs) IOE.IOEither[error, ConvergeResult] {
                    return updateBucketIfNeeded(bucket, attrs, params)
                },
            ),
        )
    }
}

func createBucket(
    client *storage.Client,
    params BucketConvergeParams,
) IOE.IOEither[error, ConvergeResult] {
    return func() E.Either[error, ConvergeResult] {
        ctx := context.Background()
        attrs := &storage.BucketAttrs{
            Location:       params.Location,
            VersioningEnabled: params.Versioning,
            Lifecycle:      storage.Lifecycle{Rules: params.LifecycleRules},
        }
        bucket := client.Bucket(params.Name)
        err := bucket.Create(ctx, params.ProjectID, attrs)
        if err != nil {
            return E.Left[ConvergeResult](err)
        }
        return E.Of[error](ConvergeResult{Action: "created", Resource: params.Name})
    }
}

func updateBucketIfNeeded(
    bucket *storage.BucketHandle,
    current *storage.BucketAttrs,
    params BucketConvergeParams,
) IOE.IOEither[error, ConvergeResult] {
    needsUpdate := current.VersioningEnabled != params.Versioning ||
        !lifecycleEqual(current.Lifecycle.Rules, params.LifecycleRules)
    if !needsUpdate {
        return IOE.Of[error](ConvergeResult{Action: "unchanged"})
    }
    return func() E.Either[error, ConvergeResult] {
        ctx := context.Background()
        update := storage.BucketAttrsToUpdate{
            VersioningEnabled: &params.Versioning,
            Lifecycle:         &storage.Lifecycle{Rules: params.LifecycleRules},
        }
        _, err := bucket.Update(ctx, update)
        if err != nil {
            return E.Left[ConvergeResult](err)
        }
        return E.Of[error](ConvergeResult{Action: "updated"})
    }
}
```

**CLI integration**: `cmd/cluster-ops/gcp_bucket.go` parses flags, calls `ConvergeBucket`.

### IAM binding converge

```go
func ConvergeProjectIAMBinding(
    projectID, member, role string,
    credFile string,
) IOE.IOEither[error, ConvergeResult] {
    return F.Pipe(
        getProjectIAMPolicy(projectID, credFile),
        IOE.Chain(func(policy *cloudresourcemanager.Policy) IOE.IOEither[error, ConvergeResult] {
            binding := findOrCreateBinding(policy, role)
            if slices.Contains(binding.Members, member) {
                return IOE.Of[error](ConvergeResult{Action: "unchanged"})
            }
            binding.Members = append(binding.Members, member)
            return F.Pipe(
                setProjectIAMPolicyWithRetry(projectID, policy, credFile),
                IOE.Map(func(_ *cloudresourcemanager.Policy) ConvergeResult {
                    return ConvergeResult{Action: "updated"}
                }),
            )
        }),
    )
}

func setProjectIAMPolicyWithRetry(
    projectID string,
    policy *cloudresourcemanager.Policy,
    credFile string,
) IOE.IOEither[error, *cloudresourcemanager.Policy] {
    retryPolicy := R.Monoid.Concat(
        R.LimitRetries[error](3),
        R.ExponentialBackoff[error](100*time.Millisecond, 2.0),
    )
    op := setProjectIAMPolicy(projectID, policy, credFile)
    return IOE.Retrying(
        retryPolicy,
        func(err error) bool {
            // Retry on 409 (etag conflict) or 503 (transient)
            if apiErr, ok := err.(*googleapi.Error); ok {
                return apiErr.Code == http.StatusConflict || apiErr.Code == http.StatusServiceUnavailable
            }
            return false
        },
        op,
    )
}
```

**Bucket IAM**: similar, use `storage.BucketsService.GetIamPolicy/SetIamPolicy`.

### Cross-project bucket reader grant

`grant-source-bucket-reader` (arangodb.justfile:2049–2090) is a composite: create SA in source project, grant `roles/storage.objectViewer` on source bucket, mint key, write to secret in target cluster. Splits into:
1. SA converge in source project (existing `cluster-ops gcp sa create`).
2. Bucket IAM binding in source project (`cluster-ops gcp bucket add-iam-binding`).
3. Key converge (existing `cluster-ops gcp sa create-key`).
4. Secret write (kubectl, stays shell or uses `internal/k8s` helpers from checks plan).

**Recipe update**:
```bash
# Before (shell):
if gcloud iam service-accounts describe "$SA_EMAIL" --project "$SOURCE_PROJECT" >/dev/null 2>&1; then
    echo "SA exists"
else
    gcloud iam service-accounts create "$SA_NAME" …
fi
gcloud storage buckets add-iam-policy-binding "gs://${SOURCE_BUCKET}" …
gcloud iam service-accounts keys create "$KEY_FILE" …

# After (Go):
go run ./cmd/cluster-ops/ gcp sa create --project "$SOURCE_PROJECT" --name "$SA_NAME" \
    --display-name "…" --description "…"
go run ./cmd/cluster-ops/ gcp bucket add-iam-binding --bucket "$SOURCE_BUCKET" \
    --member "serviceAccount:$SA_EMAIL" --role "roles/storage.objectViewer"
go run ./cmd/cluster-ops/ gcp sa create-key --project "$SOURCE_PROJECT" --sa-name "$SA_NAME" \
    --output-file "$KEY_FILE"
kubectl create secret generic … --from-file=credentials="$KEY_FILE"
```

**Identity binding**: recipes must pass `--project` explicitly (from registry or cluster-env), not rely on ambient `$GOOGLE_CLOUD_PROJECT`.

### Recipe invocation changes

**Current**: `go build -o bin/cluster-ops && ./bin/cluster-ops <cmd>` (sa.justfile:47, kms.justfile:52).  
**Target**: `go run ./cmd/cluster-ops/ <cmd>` per user rule.

**Contract test impact**: mocks on PATH must handle `go run ./cmd/cluster-ops/` prefix. Update `scripts/test-contract-*.sh` mocks:
```bash
# Mock go command (test-contract-gcp.sh)
go() {
    if [[ "$1" == "run" && "$2" == "./cmd/cluster-ops/" ]]; then
        shift 2  # Remove "run ./cmd/cluster-ops/"
        echo "[MOCK] cluster-ops $@" >&2
        # Assert expected subcommand/flags
    fi
}
```

## Acceptance criteria

1. **Idempotent re-run**: running any converge command twice produces "unchanged" on second run (verified by capturing action output).
2. **Error classification**: NotFound → create, AlreadyExists → noop, PermissionDenied → fail fast (no retry).
3. **IAM etag handling**: concurrent policy updates (simulated by two parallel runs) produce correct final state (both bindings present).
4. **Dry-run mode**: `--dry-run` prints intended action JSON, performs zero mutations (verified by fake client call recording).
5. **Key mint idempotency**: re-running SA setup with existing key file = "unchanged" (no new key minted).
6. **Preflight validation**: `delete-state-bucket` with non-empty bucket + `--confirm no` → error, zero mutation.
7. **Contract tests pass**: `just check` (calls `scripts/test-contract-*.sh`) asserts correct `go run ./cmd/cluster-ops/` invocations.
8. **Integration tests**: optional `//go:build integration` suite against sandbox project (`$GCP_SANDBOX_PROJECT`) creates/deletes real resources, verifies converge behavior.
9. **Explicit identity binding**: zero uses of ambient `$GOOGLE_CLOUD_PROJECT` / default gcloud config in Go code (enforced by grep in CI).
10. **Code style**: all new code fp-go v2 compliant (flat `F.Pipe`, `IOE.ChainFirstIOK` for logging, `struct{}{}` returns, predicates via `Pred.ContraMap`). Existing imperative code touched by migration refactored to style.

## Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| **IAM lockout** | Incorrect policy update removes own SA permissions | Dry-run mode + manual review before first run; integration tests in sandbox |
| **Etag race** | Concurrent `setIamPolicy` calls → lost update | Retry logic on 409; batch mutations in single transaction where possible |
| **Key sprawl** | Non-idempotent key minting → multiple keys per SA | Implement key file validation + list-keys check before minting |
| **Bucket data loss** | `delete-state-bucket` preflight failure → accidental deletion | Multi-stage preflight (object count check, `--confirm yes` required, audit log) |
| **Propagation delay** | SA created but IAM binding fails (propagation not done) | Reuse existing retry policy (service_account.go:175) for SA creation; bindings don't need retry |
| **Mixed shell/Go state** | Mid-migration, some recipes shell, some Go → inconsistent behavior | Phase migrations by **complete workflows** (e.g. all backup SA setup in one phase) |
| **Contract test coverage gaps** | Mocks miss real flag patterns → CI passes, prod fails | Manual integration test run in sandbox before merging each phase |

**Rollback strategy**: each phase is independently revertible (justfile recipes keep shell fallback until phase complete). If Go converge fails in prod, `git revert` the CLI command addition, recipes call shell `gcloud` again.

## Related plans

- `checks-probes.md` — check/probe recipes (healthchecks, resource existence, version verification)
- `port-forward.md` — port-forward lifecycle (setup, teardown, probe-before-reuse)
- `wait-poll.md` — wait loops, poll intervals, long-running operation retry
- `pulumi-automation.md` — Pulumi Automation API (stack converge, config set, preview/up)
- `checks-phase1-implementation.md` — Phase 1 executable plan (foundation + backend services gate)
