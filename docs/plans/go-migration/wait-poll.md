# Plan: Go Migration — Wait/Poll Loops

**Status**: Proposed

## Table of Contents
- [Goal](#goal)
- [Non-goals](#non-goals)
- [Current state](#current-state)
- [Design](#design)
  - [Package structure](#package-structure)
  - [Condition types](#condition-types)
  - [Retry and backoff](#retry-and-backoff)
  - [Terminal failure detection](#terminal-failure-detection)
  - [CLI interface](#cli-interface)
- [Inventory](#inventory)
  - [Pod readiness waits](#pod-readiness-waits)
  - [Job completion waits](#job-completion-waits)
  - [Resource deletion waits](#resource-deletion-waits)
  - [Deployment rollout waits](#deployment-rollout-waits)
  - [Custom resource status waits](#custom-resource-status-waits)
  - [CRD establishment waits](#crd-establishment-waits)
  - [GCP operation waits](#gcp-operation-waits)
  - [IAM propagation sleeps](#iam-propagation-sleeps)
  - [Node readiness waits](#node-readiness-waits)
  - [StorageClass readiness](#storageclass-readiness)
- [Phasing](#phasing)
  - [Phase 1: Foundation and pod/job waits](#phase-1-foundation-and-podjob-waits)
  - [Phase 2: Custom resources and operators](#phase-2-custom-resources-and-operators)
  - [Phase 3: GCP operations](#phase-3-gcp-operations)
- [Acceptance criteria](#acceptance-criteria)
- [Contract test changes](#contract-test-changes)
- [Documentation updates](#documentation-updates)
- [Risks and rollback](#risks-and-rollback)
- [Related plans](#related-plans)

## Goal

Eliminate brittle shell-based wait/poll loops by migrating them to Go functions in `internal/waitfor`. Replace bare `sleep` calls, fragile `until`/`while` loops, and timeout-less waits with robust, testable implementations using `k8s.io/apimachinery/pkg/util/wait` poll utilities, watch-based waits, and GCP SDK long-running operation `.Wait()`.

## Non-goals

- Do NOT migrate port-forward readiness loops — those belong to the port-forward plan (`port-forward.md`).
- Do NOT eliminate informational sleeps or user-requested delays — only brittle condition waits.
- Do NOT change timeout/interval defaults unless a current default creates operational risk (e.g., infinite loops).

## Current state

**62 total wait/poll occurrences** across `just_modules/*.justfile`, `Justfile`, and `scripts/*.sh`.

**Brittle patterns:**
- **Bare `sleep N`** with no condition check (IAM propagation: `cluster.justfile:92`, node check: `cluster.justfile:1453`).
- **Fixed-count retry loops** (`for i in $(seq 1 "$RETRIES"); do ... sleep "$INTERVAL"; done`) with no terminal-failure detection (pod readiness, Job waits).
- **Text-based success detection** (`grep -qi "not found"`, `status = "READY"`).
- **Duplicate implementations** — identical `_wait-ready` recipe in `arangodb.justfile:67` and `postgres.justfile:53`.
- **No observedGeneration checks** — deployment rollout status may read stale data.
- **Swallowed CrashLoopBackOff** — pod readiness waits do not fail fast on terminal container states.
- **Timeout-less waits** — some `kubectl wait` calls lack explicit timeout.

**Current sleep counts by file:**
- `arangodb.justfile`: 6
- `postgres.justfile`: 6
- `minio.justfile`: 3
- `pulumi.justfile`: 2
- `cluster.justfile`: 2
- `redis.justfile`: 2
- `nats.justfile`: 1
- `image.justfile`: 1

## Design

### Package structure

`internal/waitfor` owns all Kubernetes and GCP wait logic. Provides poll-based and watch-based wait functions.

**Core types:**
```go
package waitfor

import (
    "context"
    "time"
    E "github.com/IBM/fp-go/v2/either"
    IOE "github.com/IBM/fp-go/v2/ioeither"
    "k8s.io/apimachinery/pkg/util/wait"
    "k8s.io/client-go/kubernetes"
)

type WaitConfig struct {
    Timeout  time.Duration
    Interval time.Duration
    Logger   *slog.Logger
}

type PodReadyCondition struct {
    Namespace string
    Selector  string
    Count     int
}

type JobCondition struct {
    Namespace string
    Name      string
}

type DeploymentRolloutCondition struct {
    Namespace string
    Name      string
}
```

**Wait strategies:**
1. **Poll-based** (simple conditions, infrequent checks): `wait.PollUntilContextTimeout(ctx, interval, timeout, immediate, conditionFunc)`.
2. **Watch-based** (real-time, operator CRs): `watchtools.UntilWithSync(ctx, lw, &obj, preconditionFunc, conditionFunc)` via informers.
3. **GCP operations**: SDK's `operation.Wait(ctx)` (long-running ops) or classified-retry for IAM propagation (see `internal/gcp/service_account.go:118` retry pattern).

**Function signatures:**
```go
// Poll until N pods matching selector are Running with all containers ready.
// Fails fast on CrashLoopBackOff, ImagePullBackOff.
func PodsReady(
    ctx context.Context,
    clients *kube.Clients,
    cfg WaitConfig,
    cond PodReadyCondition,
) IOE.IOEither[error, struct{}]

// Poll until Job succeeds. Fails fast if Job.status.failed > 0.
// Tails logs on failure.
func JobComplete(
    ctx context.Context,
    clients *kube.Clients,
    cfg WaitConfig,
    cond JobCondition,
) IOE.IOEither[error, struct{}]

// Poll until resources matching kind/selector are deleted.
// Missing CRD counts as gone.
func ResourcesGone(
    ctx context.Context,
    clients *kube.Clients,
    cfg WaitConfig,
    namespace string,
    kind string,
    selector string,
) IOE.IOEither[error, struct{}]

// Poll until Deployment observedGeneration == metadata.generation
// and all replicas are updated/available.
func DeploymentRollout(
    ctx context.Context,
    clients *kube.Clients,
    cfg WaitConfig,
    cond DeploymentRolloutCondition,
) IOE.IOEither[error, struct{}]

// Watch custom resource (unstructured) until status.<path> == <value>.
// For ArangoDeployment, CNPG Cluster. Uses dynamic client.
func CustomResourceStatus(
    ctx context.Context,
    clients *kube.Clients,
    cfg WaitConfig,
    gvk schema.GroupVersionKind,
    namespace string,
    name string,
    statusPath string,      // e.g., "state"
    expectedValue string,   // e.g., "Running"
) IOE.IOEither[error, struct{}]

// Poll until CRD is Established.
func CRDEstablished(
    ctx context.Context,
    apiextClient apiextensionsclientset.Interface,
    cfg WaitConfig,
    crdName string,
) IOE.IOEither[error, struct{}]

// Poll until StorageClasses exist with expected provisioner.
func StorageClassesReady(
    ctx context.Context,
    clients *kube.Clients,
    cfg WaitConfig,
    classNames []string,
    provisioner string,
) IOE.IOEither[error, struct{}]

// Poll until N nodes matching selector are Ready with expected label/taint.
func NodesReady(
    ctx context.Context,
    clients *kube.Clients,
    cfg WaitConfig,
    selector string,
    count int,
) IOE.IOEither[error, struct{}]
```

### Condition types

| Condition | Success Criteria | Terminal Failures |
|-----------|------------------|-------------------|
| PodsReady | `phase == "Running"` AND all `containerStatuses[].ready == true` AND count ≥ target | `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, pod evicted |
| JobComplete | `status.succeeded ≥ 1` OR Job deleted after seen | `status.failed ≥ 1` (tail logs, exit immediately) |
| ResourcesGone | `len(items) == 0` OR CRD/namespace not found | N/A |
| DeploymentRollout | `observedGeneration == metadata.generation` AND `status.updatedReplicas == spec.replicas` AND `status.availableReplicas == spec.replicas` | `progressDeadlineExceeded` condition |
| CustomResourceStatus | Unstructured `status.<path>` matches expected value | Implementation-specific (ArangoDeployment `.status.state == "Failed"`, CNPG Cluster `.status.phase == "Failed"`) |
| CRDEstablished | CRD `.status.conditions[]` contains `type: Established, status: True` | N/A |
| StorageClassesReady | All named classes exist with matching `.provisioner` | N/A |
| NodesReady | N nodes with `.status.conditions[]` containing `type: Ready, status: True` | N/A |

### Retry and backoff

**IAM propagation** (existing pattern in `internal/gcp/service_account.go:118`):
```go
import R "github.com/IBM/fp-go/v2/retry"

iamPropagationPolicy := R.Monoid.Concat(
    R.LimitRetries(5),
    R.ExponentialBackoff(2 * time.Second),
)

IOE.Retrying(iamPropagationPolicy, action.Run, retryOnSAMissing)()
```

**Classified transient errors** (from infra checklist):
- GCP 409 Conflict (concurrent writes)
- GCP 503 Service Unavailable
- K8s `IsConflict` (optimistic lock failure)
- IAM "service account does not exist" within 30s of creation

**Non-retryable errors:**
- Job failed
- CrashLoopBackOff
- ImagePullBackOff
- Validation errors

### Terminal failure detection

Exit codes:
- **0**: condition met
- **1**: timeout exceeded
- **2**: terminal failure (CrashLoopBackOff, Job failed, etc.) — fail fast
- **3**: API error (connection refused, forbidden, not found cluster)

Progress logging (every poll or on status change):
```
Waiting for 3 pod(s) in dev (app=arangodb): 1/3 ready (30s elapsed)
Waiting for job reset-root-password in dev: active (60s elapsed)
Pod arangodb-0 in CrashLoopBackOff: container db exited with code 1 (reason: Error, message: ...)
Job reset-root-password in dev failed after 2m15s. Last 100 log lines:
...
```

### CLI interface

`cmd/cluster-ops/main.go` — `urfave/cli` v2:
```
cluster-ops wait pods-ready --namespace dev --selector app=arangodb --count 3 --timeout 15m
cluster-ops wait job-complete --namespace dev --job reset-root-password --timeout 10m
cluster-ops wait deployment-rollout --namespace dev --name order-api-server --timeout 5m
cluster-ops wait resources-gone --namespace dev --kind arangodeployment --timeout 3m
cluster-ops wait crd-established --crd arangodeployments.database.arangodb.com --timeout 2m
cluster-ops wait custom-resource --namespace dev --name arangodb \
    --group database.arangodb.com --version v1 --kind ArangoDeployment \
    --status-path state --expected-value Running --timeout 10m
cluster-ops wait storage-classes --classes dictycr-balanced,dictycr-fast \
    --provisioner rancher.io/local-path --timeout 1m
cluster-ops wait nodes-ready --selector kops.k8s.io/instancegroup=stateful-db \
    --count 3 --timeout 5m
```

All subcommands accept `--kubeconfig` and `--context` (passed to `kube.NewClients`).

Recipes call `go run ./cmd/cluster-ops wait ...` (per user rule; `just check` mocks `go` for contract tests).

## Inventory

### Pod readiness waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `arangodb._wait-ready` | `arangodb.justfile:67` | N pods Running, all containers ready | 900s (90×10s) | 10s | No CrashLoopBackOff detection; duplicate code |
| `postgres._wait-ready` | `postgres.justfile:53` | N pods Running, all containers ready | 900s (90×10s) | 10s | Identical to arangodb; no terminal-failure detection |

**Target:** `cluster-ops wait pods-ready`

**SDK:** `k8s.io/client-go/kubernetes.CoreV1().Pods(ns).List`, poll-based with `wait.PollUntilContextTimeout`.

**Migration:**
- Replace both `_wait-ready` recipes with `go run ./cmd/cluster-ops wait pods-ready --namespace "$NS" --selector "$SEL" --count "$WANT" --timeout 15m`.
- Add terminal-failure detection: inspect `pod.status.containerStatuses[].state.waiting.reason` for `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`; fail with exit code 2 and container message.

### Job completion waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `arangodb._wait-job` | `arangodb.justfile:170` | Job `.status.succeeded ≥ 1` | 900s (90×10s) | 10s | Tails logs on failure (good); seen-then-deleted logic fragile |
| `arangodb._wait-job` (callers) | `arangodb.justfile:1185, 1271, 1533` | reset-root-password, create-arangodb-databases, backup Jobs | 900s | 10s | N/A |

**Target:** `cluster-ops wait job-complete`

**SDK:** `k8s.io/client-go/kubernetes.BatchV1().Jobs(ns).Get`, poll-based.

**Migration:**
- Replace `just arangodb _wait-job --namespace "$NS" --job "$JOB"` with `go run ./cmd/cluster-ops wait job-complete --namespace "$NS" --job "$JOB" --timeout 15m`.
- Keep log-tailing on failure; use `Pods(ns).List(metav1.ListOptions{LabelSelector: "job-name=<job>"})` to find pod, then `CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{TailLines: int64Ptr(100)})`.
- Seen-then-deleted: track `seen` state; if Job not found after seen, exit 0 (TTL cleanup).

### Resource deletion waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `arangodb._wait-gone` | `arangodb.justfile:821` | Resource list length == 0 | 180s (36×5s) | 5s | Grep for "doesn't have a resource type" fragile; works but text-based |
| `postgres._wait-gone` (inline) | `postgres.justfile:846` | Pods with label `cnpg.io/cluster=<cluster>` gone | 60s (30×2s) | 2s | No explicit wait function; inline `for` loop with bare `sleep 2` |

**Target:** `cluster-ops wait resources-gone`

**SDK:** Dynamic client for CRDs, typed client for core resources. Poll-based.

**Migration:**
- Replace `just arangodb _wait-gone --namespace "$NS" --kind "$KIND"` with `go run ./cmd/cluster-ops wait resources-gone --namespace "$NS" --kind "$KIND" --timeout 3m`.
- Use `kube.Classify(err)` to detect `NotFound` reason; missing CRD / namespace / resource type all count as gone (exit 0).
- Replace postgres inline loop (`postgres.justfile:846–849`) with `cluster-ops wait resources-gone --namespace "$NS" --kind pod --selector "cnpg.io/cluster=$CLUSTER" --timeout 1m`.

### Deployment rollout waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `logto.deploy-logto` | `logto.justfile:145` | `kubectl rollout status deployment/logto` | 300s | N/A (kubectl default) | No observedGeneration check; relies on kubectl heuristic |
| `pulumi.verify-rollout` | `pulumi.justfile:811, 829` | `kubectl rollout status deploy/<name>` | None | N/A | No timeout; can hang indefinitely |

**Target:** `cluster-ops wait deployment-rollout`

**SDK:** `k8s.io/client-go/kubernetes.AppsV1().Deployments(ns).Get`, poll-based.

**Migration:**
- Replace `kubectl rollout status deployment/logto -n "$NS" --timeout=300s` with `go run ./cmd/cluster-ops wait deployment-rollout --namespace "$NS" --name logto --timeout 5m`.
- Check `deployment.status.observedGeneration == deployment.metadata.generation` (stale-data guard).
- Check `deployment.status.updatedReplicas == deployment.spec.replicas` AND `deployment.status.availableReplicas == deployment.spec.replicas`.
- Fail fast if `.status.conditions[]` contains `type: Progressing, status: False, reason: ProgressDeadlineExceeded`.

### Custom resource status waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| ArangoDeployment ready | (implied, not explicit wait) | `.status.state == "Running"` | None | N/A | Recipes deploy then immediately proceed; no wait implemented |
| CNPG Cluster ready | (implied, not explicit wait) | `.status.phase == "Cluster in healthy state"` | None | N/A | No wait; recipes assume eventual consistency |
| ArangoBackup complete | (not found in current recipes) | `.status.state == "Ready"` | N/A | N/A | Not currently automated |

**Target:** `cluster-ops wait custom-resource`

**SDK:** `k8s.io/client-go/dynamic` for unstructured access. Watch-based preferred (informer), poll-based acceptable.

**Migration:**
- **Not in current recipes** — these waits are missing and needed. Phase 2 adds them.
- After `pulumi up` deploys ArangoDeployment, add: `go run ./cmd/cluster-ops wait custom-resource --namespace "$NS" --name arangodb --group database.arangodb.com --version v1 --kind ArangoDeployment --status-path state --expected-value Running --timeout 10m`.
- After CNPG Cluster create, add: `go run ./cmd/cluster-ops wait custom-resource --namespace "$NS" --name "$CLUSTER" --group postgresql.cnpg.io --version v1 --kind Cluster --status-path phase --expected-value "Cluster in healthy state" --timeout 10m`.
- Use `unstructured.Unstructured` + `status, found, err := unstructured.NestedString(obj.Object, "status", pathParts...)`.

### CRD establishment waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `arangodb.deploy-arangodb-operator` | `arangodb.justfile:1074` | Comment says "Checking the ArangoDeployment CRD is registered..." but no wait implemented | N/A | N/A | Missing implementation; recipes assume CRD available immediately after Helm install |

**Target:** `cluster-ops wait crd-established`

**SDK:** `k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset.ApiextensionsV1().CustomResourceDefinitions().Get`, poll-based.

**Migration:**
- Add after Helm release (operator installs): `go run ./cmd/cluster-ops wait crd-established --crd arangodeployments.database.arangodb.com --timeout 2m`.
- Check `crd.status.conditions[]` contains `{type: Established, status: True}`.

### GCP operation waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `image.create-image-from-instance` | `image.justfile:38` | GCP machine image `.status == "READY"` | Infinite | 10s | `while read status` on streaming describe; text-based; no timeout |

**Target:** `cluster-ops gcp wait-machine-image` (or inline in Go if only one caller)

**SDK:** `google.golang.org/api/compute/v1` — machine image insert returns an operation; call `operation.Wait(ctx)` or poll `computeService.GlobalOperations.Get(project, op.Name)` until `.Status == "DONE"`.

**Migration:**
- Replace `while read status` loop with GCP SDK operation wait.
- Current recipe uses `gcloud compute machine-images create --async` + `describe --format 'value(status)'` streaming — switch to Go SDK, synchronous wait.
- Check `operation.Error` for failure; machine image creation can fail (quota, disk locked, etc.).

### IAM propagation sleeps

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `cluster.init-gcp-project` | `cluster.justfile:92` | IAM API enabled + service accounts visible | 10s | N/A | Bare `sleep 10` after `enable-apis`; no verification |

**Target:** Remove sleep; use classified retry (existing `internal/gcp/service_account.go:118` pattern)

**SDK:** Already implemented via `IOE.Retrying(iamPropagationPolicy, action.Run, retryOnSAMissing)()` in `service_account.go`.

**Migration:**
- Recipe `init-gcp-project` calls `gcp-api enable-apis` then `sleep 10` then `gcp-sa create-sa`.
- `create-sa` already retries IAM binding on SA-not-found (see `service_account.go:118`).
- **Remove `sleep 10`** from `cluster.justfile:92`. The Go code's retry policy handles propagation delay.
- Acceptance: propagation delay absorbed by retry; no bare sleep.

### Node readiness waits

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `cluster._wait-stateful-db-nodes` | `cluster.justfile:1428–1453` | N nodes with `kops.k8s.io/instancegroup=stateful-db` Ready | ~60s (30×2s) | 2s | Inline loop; no helper; bare `sleep 2` |

**Target:** `cluster-ops wait nodes-ready`

**SDK:** `k8s.io/client-go/kubernetes.CoreV1().Nodes().List`, poll-based.

**Migration:**
- Replace inline loop with `go run ./cmd/cluster-ops wait nodes-ready --selector kops.k8s.io/instancegroup=stateful-db --count 3 --timeout 5m`.
- Check `node.status.conditions[]` contains `{type: Ready, status: True}`.
- Recipe then labels/taints nodes (mutates); wait must complete first (order preserved).

### StorageClass readiness

| Recipe | File:Line | What Awaited | Timeout | Interval | Brittleness |
|--------|-----------|--------------|---------|----------|-------------|
| `pulumi.verify-storageclass` | `pulumi.justfile:397` | `until just gcp-pulumi check-storageclass ...` succeeds | 60s (12×5s) | 5s | Nested `just` call in `until`; works but shell-heavy |

**Target:** `cluster-ops wait storage-classes`

**SDK:** `k8s.io/client-go/kubernetes.StorageV1().StorageClasses().List`, poll-based.

**Migration:**
- Replace `until just gcp-pulumi check-storageclass --classes "$classes" --provisioner "$provisioner"; do sleep 5; done` with `go run ./cmd/cluster-ops wait storage-classes --classes dictycr-balanced,dictycr-fast --provisioner pd.csi.storage.gke.io --timeout 1m`.
- Check each class exists and `.provisioner == expected`.
- Keep `check-storageclass` recipe for manual verification; wait command for automation.

## Phasing

### Phase 1: Foundation and pod/job waits

**Scope:**
- `internal/waitfor` package structure
- `PodsReady`, `JobComplete`, `ResourcesGone` functions
- CLI subcommands `pods-ready`, `job-complete`, `resources-gone`
- Migrate: `arangodb._wait-ready`, `postgres._wait-ready`, `arangodb._wait-job`, `postgres.justfile:846–849` inline pod wait
- Remove bare `sleep 10` from `cluster.justfile:92` (IAM propagation)

**Acceptance:**
- Zero `_wait-ready` or `_wait-job` shell recipes remain in `arangodb.justfile`, `postgres.justfile`.
- CrashLoopBackOff detected within one poll interval; test with failing container.
- Job failure tails logs; test with failing Job.
- Contract test `scripts/test-arangodb-recipes.sh` mocks `go run ./cmd/cluster-ops wait pods-ready`.

### Phase 2: Custom resources and operators

**Scope:**
- `DeploymentRollout`, `CustomResourceStatus`, `CRDEstablished` functions
- CLI subcommands `deployment-rollout`, `custom-resource`, `crd-established`
- Migrate: `logto.justfile:145`, `pulumi.justfile:811/829`, add waits for ArangoDeployment/CNPG Cluster after `pulumi up`

**Acceptance:**
- No `kubectl rollout status` in recipes (migrated to Go).
- ArangoDeployment `.status.state == "Running"` verified before databases created.
- CNPG Cluster `.status.phase == "Cluster in healthy state"` verified before restore.
- observedGeneration checked; test by patching deployment mid-rollout.

### Phase 3: GCP operations

**Scope:**
- `NodesReady`, `StorageClassesReady` functions
- CLI subcommands `nodes-ready`, `storage-classes`
- GCP machine image wait (inline or CLI subcommand)
- Migrate: `cluster.justfile:1428–1453`, `pulumi.justfile:397`, `image.justfile:38`

**Acceptance:**
- No bare `sleep N` in node/StorageClass waits.
- GCP machine image creation uses SDK operation wait; no text-based `while read status`.
- `pulumi.verify-storageclass` loop replaced with `cluster-ops wait storage-classes`.

## Acceptance criteria

- **No bare `sleep N` remains** in migrated recipes (except informational delays like "waiting for user to verify X in GCP Console" — none exist in inventory).
- **No duplicate `_wait-*` recipes** — `arangodb._wait-ready` and `postgres._wait-ready` merged into one Go function.
- **CrashLoopBackOff reported within one poll** — test: deploy pod with `imagePullPolicy: Never` + nonexistent image; wait exits within 10s with reason.
- **Job failure tails logs** — test: Job with `exit 1`; wait prints last 100 lines, exits code 2.
- **observedGeneration checked** — test: patch deployment during rollout; wait does not report success on stale generation.
- **`just check` passes** — all contract tests green with mocked `go`.
- **Timeout enforced** — test: condition never met; wait exits code 1 after timeout with "timed out after Ns" message.
- **Progress logged** — test: capture stderr; verify "Waiting for X: Y/Z ready (Ns elapsed)" appears.

## Contract test changes

Existing shell contract tests (`scripts/test-*.sh`, run by `just check` at line 264) mock `go` on PATH:
```bash
go() {
    case "$*" in
        "run ./cmd/cluster-ops wait pods-ready"*) echo "3/3 pod(s) ready in dev (app=arangodb)." ;;
        "run ./cmd/cluster-ops wait job-complete"*) echo "Job reset-root-password completed in dev." ;;
        *) command go "$@" ;;
    esac
}
```

**New assertions:**
- ORDER: `cluster-ops wait pods-ready` must appear before database creation (e.g., `scripts/test-backend-services.sh` line 81).
- OUTPUT: `grep -q "pod(s) ready"` in captured recipe output.
- FAILURE PATH: stub `go` with `exit 2` for CrashLoopBackOff; recipe must propagate error.

## Documentation updates

| File | Change |
|------|--------|
| `docs/reference/kubernetes/waiting.md` | New file: Wait function catalog (CLI flags table, condition types, exit codes, examples) |
| `docs/guides/backend-deploy.md` | Update: `pulumi up` → wait for ArangoDeployment ready → create databases (order change) |
| `docs/STYLE.md` | No change (wait commands are one-liners, fit existing guide format) |

## Risks and rollback

| Risk | Mitigation | Rollback |
|------|------------|----------|
| Watch-based waits timeout on large clusters (informer cache slow) | Phase 2 starts with poll-based; add watch optimization later if needed | Switch to poll |
| Terminal failure detection false positive (pod restart during startup misclassified as CrashLoopBackOff) | Check `restartCount` + `state.waiting.reason`; only fail if reason is terminal AND restartCount > 2 | Relax threshold |
| GCP operation wait blocks indefinitely on stale operation | Pass `context.WithTimeout` to `operation.Wait(ctx)` | Add timeout |
| Contract test mocking fragile (argument order changes break stub) | Use `case "$*" in ... "wait pods-ready"*` (prefix match, not exact) | Fix test |
| Dynamic client for CRs breaks on GVK typo | Validate GVK exists via discovery before polling | Fail fast with clear error |

**Rollback per phase:**
- Phase 1: revert recipe changes; keep `internal/waitfor` + CLI as dormant code.
- Phase 2: revert `pulumi up` additions; keep `kubectl rollout status` for deployment waits.
- Phase 3: revert `cluster.justfile:1428` and `pulumi.justfile:397`; keep shell loops.

## Related plans

- `checks-probes.md` — Checks/probes (separate category; no overlap)
- `port-forward.md` — Port-forward flows (owns port-forward readiness loops; cross-reference here but do not migrate)
- `gcp-converge.md` — GCP describe-then-create (separate category; GCP operation waits in this plan)
- `pulumi-automation.md` — Pulumi Automation API (optional; does not block wait migration)
- `checks-phase1-implementation.md` — Phase-1 foundation (depends on this plan's foundation being implemented first OR shared foundation merged)
