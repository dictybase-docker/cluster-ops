# Remaining Check/Validate Recipes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Commits:** each task ends with an explicit `git add` + `git commit -m` step (Conventional Commits). Never stage `docs/plans/**`, `.superpowers/**`, ledgers, `.env*` or `.tool-versions*` scratch files.

**Goal:** Migrate the six remaining shell check/validate recipes (`check-tools`, `check-storageclass`, `validate-hardening`, `validate-cluster`, `verify-cluster`, `preflight-create`) to `cluster-ops preflight` subcommands, fixing error conflation, first-miss exit and hardcoded values per the brittleness catalog in the spec.

**Architecture:** Each check becomes one or more `IO.IO[preflight.Result]` builders in `internal/preflight/{toolchain,backend,cluster}`, composed by `preflight.Run` (never short-circuits) and rendered by `preflight.Print` (check-helpers.sh contract). External processes and GCP calls sit behind injected function fields (the `LoginReader` pattern from `backend/pulumi.go`) so unit tests run without binaries or a cluster. Justfile recipes become thin wrappers that keep their fix-up actions and narration echoes.

**Tech Stack:** Go 1.26, fp-go v2 (house stack), client-go fake clientset, urfave/cli v2, testify/require, bash contract tests, just.

**Spec:** [`checks-probes.md`](checks-probes.md) — authority for behavior, output contract, secrets rules and acceptance intent. Its *inventory* is stale (written before phase 1–2 landed); the audit table below supersedes it.

**Status:** Ready to execute — Verified: replayed via execution on `d9309e6..b548c16` (2026-10-09): all 8 tasks + one review fix pass executed task-by-task (TDD, RED→GREEN recorded per task), 379 tests/1 skipped green, `just check` exit=0. Every deviation from this draft became a `Ruling:` line in the run ledger (`Ruling` lines below tasks reflect fixes applied during replay).

---

## Staleness audit (supersedes the spec's inventory)

Already migrated on this branch — **do not re-implement** (evidence: packages + `go run ./cmd/cluster-ops preflight` call sites, 14 lines in `just_modules/`):

| Recipe | Go target | State |
|---|---|---|
| `check-backend-prereqs` | `backend/prereqs.go` (CheckNamespace/CheckSecret/CheckArangoDB/CheckPort) | DONE |
| `check-backend` | `backend/pulumi.go` (CheckEnvVars/CheckManagerKey/CheckBucketVersioning/CheckSecretsProviderKey/CheckPulumiLogin) | DONE |
| `verify-deployer-access` | `backend/deployer.go` (CheckKeyProject/CheckDeploymentPermission/CheckKMSRoundTrip) | DONE |
| `check-deploy-credentials` | `backend/credentials.go` (CheckIAMPermissions/With) | DONE |
| `check-pool` ×4 | `datastore/pool.go` (CheckPool) | DONE |
| `verify` ×5 + `verify-app-credentials` | `datastore/{arangodb,postgres,redis,minio,nats}.go` | DONE |
| forward/reconnect infra | `kube/tunnel.go` (terratest), `kube/portforward.go` (supervisor) | DONE |

**Remaining — this plan's scope (6 recipes):**

| Recipe | Recipe location | Go target | CLI |
|---|---|---|---|
| `check-tools` | `pulumi.justfile:276` → `scripts/pulumi/check-tools.sh` | `preflight/toolchain/toolchain.go` | `preflight tools` |
| `check-storageclass` | `pulumi.justfile:411` | `preflight/backend/storageclass.go` | `preflight storageclass` |
| `validate-hardening` | `cluster.justfile:239` | `preflight/cluster/hardening.go` | `preflight hardening` |
| `validate-cluster` | `cluster.justfile:319` | `preflight/cluster/validate.go` | `preflight validate-cluster` |
| `verify-cluster` | `cluster.justfile:1473` | `preflight/cluster/verify.go` | `preflight verify-cluster` |
| `preflight-create` | `cluster.justfile:1676` | `preflight/cluster/create.go` | `preflight create` |

Design deltas vs the spec (decided here; the spec's code snippets are historical):
- CLI names mirror recipe names (`preflight validate-cluster`), not the spec's `preflight cluster|create` sketch; `pulumi-backend` is the naming precedent.
- `VerifyDeployerAccess`/`CheckDeployCredentials` landed in `backend/`, not `cluster/`. The six new checks still split `toolchain` / `backend` / new `internal/preflight/cluster` as tabled.
- Output for migrated recipes uses the shared `preflight.Print` PASS/FAIL/WARN/INFO contract (spec non-goal: keep the check-helpers format). `validate-hardening`'s `✓/✗` layout is replaced by the shared contract — exit code and loudness unchanged; tests pin Detail wording, not the old layout.
- `verify-cluster` keeps kubeconfig auto-export in the **justfile wrapper** (a fix-up action, not a check). Kube-dependent check groups that cannot run report a FAIL Result `not run — kubeconfig missing (run just gcp-cluster export-kubeconfig)`; the kops group still runs (it needs no kubeconfig). Nothing silently skips.

## Global Constraints

- fp-go house stack only (`Option`/`Either`/`IO`/`IOEither`, alias `IOE` = `ioeither`); `F.Pipe`, no inside-out `f(g(x))`, no `combinator(args)(value)` at call sites; `IOE.TryCatchError` holds raw effects only, wrapping in `IOE.MapLeft`, projection in `IOE.Map`.
- Checks are **read-only** — no create/update/delete in any check function. Fix-up actions stay in justfile wrappers.
- **Never short-circuit**: `preflight.Run` returns one Result per check; failures listed together; exit 1 after the summary.
- Secrets never appear in `Result.Detail`/`Fix`, never in argv (auth via struct fields / env); test fixtures comment `// TEST CREDENTIAL, not real`.
- No error detection by rendered string (`err.Error()` matching); classify by error type (`errors.Is/As`, `kube.Classify`). Displaying `err` text inside Detail is fine.
- Output contract: `preflight.Print` lines; `CHECK_COLOR` (1 default, 0 plain); cluster recipes set `CHECK_LABEL_WIDTH=24`. Detail wording matches the current shell output wherever a string already exists (operators and contract tests rely on it).
- Every user-supplied justfile interpolation landing in shell renders through `quote()` (bare `{{ quote(x) }}`, never `"{{ quote(x) }}"`).
- Contract tests mock `go` on PATH **running the forwarded child / recording argv** and assert preflight runs **before** any mutation; behavior assertions live in Go tests (client-go bypasses PATH mocks).
- Rollback: old shell recipes renamed `_name-shell`, not deleted (deleted one release later).
- Gate before done: `just check` zero failures + the Final gate commands (Task 8 step 5).

## Review Focus

Input classes the tasks' tests do not exercise — reviewers check each deliberately; each is pinned to a task step:
1. `asdf current` / `gcloud version` output drift — parsing must degrade to "version unknown"/named pin mismatch, never crash (Task 2 step 1 `TestCheckToolsVersionUnknown`; Task 7 step 1 `TestCreatePinsMismatch`).
2. Metrics-server installed but metrics API not serving — distinct FAIL `not reporting (wait 60s and retry)`, not an infra error (Task 4 step 1 `TestHardeningMetricsNotReporting`).
3. kops binary missing / `kops validate cluster` exceeding `--waittime` — classified Results with named causes, not a hang or generic error (Task 5 step 1 `TestKopsValidateTimeout` / `TestKopsValidateBinaryMissing`).
4. Multiple simultaneous failures across composite groups (kops down + hardening missing + HA short) — every failure listed, one exit 1 (Task 6 step 1 `TestVerifyClusterReportsEveryGroup`).
5. GCP probes slow-but-false (bucket exists but permission denied; image describe forbidden) — classified as forbidden/other with the URI in Detail, never "missing" (Task 7 step 1 `TestCreateProbeForbidden`).

---

## Task 1: Baseline

**Files:** none

- [ ] **Step 1: Confirm the starting point.** `git log --oneline -1` → `47f086a docs(plans): index the review-fixes plan in the migration README` (or a descendant without these fixes).
- [ ] **Step 2: Confirm the Go side is green.** `go build ./... && gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → `DONE 329 tests, 1 skipped`.
- [ ] **Step 3: Confirm the scope.** Run `rg --no-filename -o 'go run ./cmd/cluster-ops preflight' just_modules/ | wc -l` → `14` (the migrated call sites). Run `rg -ln 'check-tools.sh|check-helpers.sh' just_modules/{pulumi,cluster}.justfile` → the six target recipes still run shell.

---

## Task 2: `check-tools` → `preflight tools`

**Files:** Create `internal/preflight/toolchain/toolchain.go`, `internal/preflight/toolchain/toolchain_test.go`; modify `cmd/cluster-ops/preflight.go`, `just_modules/pulumi.justfile:276-279`; create `docs/reference/pulumi/tools.md` (flag table + parity notes)

Behavior source of truth: `scripts/pulumi/check-tools.sh` — 5 tools (`pulumi`, `gcloud`, `kubectl`, `jq`, `yq`); missing → FAIL `not found on PATH`; version per-tool (below); empty/`null` version → Detail `version unknown`; summary `All required tools present.` / `N tool(s) missing. Install with: just install-tool --name <tool> --version <version>`.

**Interfaces:**
- Consumes: `preflight.Result`, `preflight.StatusPass`, `preflight.StatusFail`, `IO.IO[preflight.Result]` (`internal/preflight/result.go`).
- Produces (exact):
  ```go
  // ToolsConfig injects PATH lookup and version capture (the LoginReader pattern).
  type ToolsConfig struct {
      LookPath func(bin string) (string, error)
      Version  func(bin string) (string, error) // returns the raw version output
  }
  // DefaultToolsConfig wires exec.LookPath and the real version commands:
  //   pulumi → `pulumi version` (first line)
  //   gcloud → `gcloud version`, extract token after "Google Cloud SDK " on the
  //            "Google Cloud SDK <ver>" line (first match)
  //   kubectl → `kubectl version --client -o json`, read .clientVersion.gitVersion
  //   jq     → `jq --version` (first line)
  //   yq     → `yq --version` (first line)
  func DefaultToolsConfig() ToolsConfig
  // CheckTools returns 5 checks, one per tool, in the shell's order.
  func CheckTools(cfg ToolsConfig) []IO.IO[preflight.Result]
  ```
  The kubectl JSON path is parsed with `encoding/json` into a minimal struct (`ClientVersion struct{ GitVersion string \`json:"gitVersion"\` }`), never jq. Gcloud parsing: split output lines, first line with prefix `Google Cloud SDK ` → last space-separated token. This is display parsing only — never error detection.

- [ ] **Step 1: Write the failing tests** (`toolchain_test.go`, package `toolchain`):

```go
package toolchain

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
)

func runAll(cfg ToolsConfig) []preflight.Result {
	return preflight.Run(CheckTools(cfg))
}

func fakeTools(versions map[string]string, missing map[string]bool) ToolsConfig {
	return ToolsConfig{
		LookPath: func(bin string) (string, error) {
			if missing[bin] {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/" + bin, nil
		},
		Version: func(bin string) (string, error) {
			if v, ok := versions[bin]; ok {
				return v, nil
			}
			return "", errors.New("no version")
		},
	}
}

func TestCheckToolsAllPresent(t *testing.T) {
	got := runAll(fakeTools(map[string]string{
		"pulumi": "3.255.0\n", "gcloud": "Google Cloud SDK 451.0.0\n",
		"kubectl": `{"clientVersion":{"gitVersion":"v1.28.8"}}`, "jq": "jq-1.7\n", "yq": "yq (https://github.com/mikefarah/yq/) version v4.44.1\n",
	}, nil))
	require.Len(t, got, 5)
	for _, r := range got {
		require.Equal(t, preflight.StatusPass, r.Status, "%s: %s", r.Name, r.Detail)
	}
	require.Equal(t, "pulumi 3.255.0", got[0].Detail)
	require.Equal(t, "gcloud 451.0.0", got[1].Detail)
	require.Equal(t, "kubectl v1.28.8", got[2].Detail)
	require.Equal(t, "jq jq-1.7", got[3].Detail)
	require.Contains(t, got[4].Detail, "v4.44.1")
}

func TestCheckToolsMissingBinary(t *testing.T) {
	got := runAll(fakeTools(nil, map[string]bool{"kubectl": true}))
	var kubectl preflight.Result
	for _, r := range got {
		if r.Name == "kubectl" {
			kubectl = r
		}
	}
	require.Equal(t, preflight.StatusFail, kubectl.Status)
	require.Equal(t, "not found on PATH", kubectl.Detail)
	require.Len(t, got, 5, "never short-circuit: every tool is reported")
}

func TestCheckToolsVersionUnknown(t *testing.T) {
	got := runAll(fakeTools(map[string]string{"jq": ""}, nil))
	require.Equal(t, preflight.StatusPass, got[3].Status)
	require.Equal(t, "jq version unknown", got[3].Detail)
}

func TestCheckToolsVersionReaders(t *testing.T) {
	cfg := DefaultToolsConfig()
	require.NotNil(t, cfg.LookPath)
	require.NotNil(t, cfg.Version)
}
```

- [ ] **Step 2: Run and confirm they fail.** `gotestsum --format-hide-empty-pkg --format testname -- -count=1 ./internal/preflight/toolchain/...` → Expected: build failure `undefined: ToolsConfig`, `undefined: CheckTools`, `undefined: DefaultToolsConfig`.
- [ ] **Step 3: Implement** `toolchain.go` exactly per the Produces block. Detail strings: pass `"<label> <version>"` or `"<label> version unknown"`; fail `"<label> not found on PATH"`. Group summary is wrapper concern (step 6), not a Result.
- [ ] **Step 4: Run and confirm they pass.** → `DONE 4 tests`. Then `go test -count=1 ./internal/preflight/...` green.
- [ ] **Step 5: CLI.** In `cmd/cluster-ops/preflight.go` add subcommand `{Name: "tools", Usage: "Local toolchain binaries and versions", Action: toolsAction}`. `toolsAction` mirrors the existing action shape: build checks → `preflight.Run` → `preflight.Print(os.Stdout, results)` → `exitOrWrap`/exit 1 when `preflight.Failed(results)`.
- [ ] **Step 6: Recipe wrapper.** `just_modules/pulumi.justfile:276` — replace the `check-tools` body with `go run ./cmd/cluster-ops preflight tools` (keep the recipe doc comment; add the summary line `All required tools present.` / `N tool(s) missing. Install with: just install-tool --name <tool> --version <version>` as the wrapper's exit-code echo). Rename the old recipe `check-tools-shell` (full old body preserved).
- [ ] **Step 7: Contract + lint.** `bash scripts/test-datastore-recipes.sh` still `PASSED`; `just lint-recipes` → `no recipe findings`; `golangci-lint run ./internal/preflight/... ./cmd/cluster-ops/...` → `0 issues.`
- [ ] **Step 8: Commit.**
  `git add internal/preflight/toolchain/ cmd/cluster-ops/preflight.go just_modules/pulumi.justfile docs/reference/pulumi/tools.md`
  `git commit -m "feat(preflight): migrate check-tools to the toolchain check"`

---

## Task 3: `check-storageclass` → `preflight storageclass`

**Files:** Create `internal/preflight/backend/storageclass.go`, `internal/preflight/backend/storageclass_test.go`; modify `cmd/cluster-ops/preflight.go`, `just_modules/pulumi.justfile:411-460`; modify `docs/reference/pulumi/storage-class.md`

Behavior source: `pulumi.justfile:411-460` — per class: missing → FAIL `StorageClass <sc> missing — is it declared in storage_class/Pulumi.<stack>.yaml?`; provisioner match → PASS `StorageClass <sc> uses <found>` / FAIL `StorageClass <sc> uses <found>, expected <want>`; default-class report → INFO `default StorageClass: <comma-names>` or INFO `no default StorageClass set — every PVC must name one explicitly`; list failure → single FAIL `cannot reach the cluster — check KUBECONFIG`. Class list is comma-separated, whitespace trimmed, empty entries skipped (same as `IFS=',' read -ra`).

**Interfaces:**
- Consumes: `kube.Clients`, `preflight.Result` (+ `preflight.StatusInfo`).
- Produces (exact):
  ```go
  type StorageClassConfig struct {
      Clients     kube.Clients
      Classes     []string // already split; wrappers split the --classes CSV
      Provisioner string
  }
  // CheckStorageClass: one Result per class + one trailing Info Result for the
  // default class (annotation storageclass.kubernetes.io/is-default-class=true,
  // comma-joined names in first-seen order). List error → one FAIL Result only.
  func CheckStorageClass(cfg StorageClassConfig) []IO.IO[preflight.Result]
  ```

- [ ] **Step 1: Write the failing tests** (`storageclass_test.go`, package `backend`), fixtures via `k8sfake.NewSimpleClientset` with `storagev1.StorageClass` objects (Provisioner + the default-class annotation):
  `TestStorageClassAllMatch` (2 classes → 2 pass + 1 info `default StorageClass: dictycr-balanced`) ·
  `TestStorageClassMissing` (exact missing Detail above) ·
  `TestStorageClassWrongProvisioner` (exact mismatch Detail) ·
  `TestStorageClassNoDefault` (Info `no default StorageClass set — every PVC must name one explicitly`) ·
  `TestStorageClassListError` (reactor `PrependReactor("list", "storageclasses", …)` returning an error → exactly 1 FAIL `cannot reach the cluster — check KUBECONFIG`) ·
  `TestStorageClassNeverShortCircuits` (one missing + one wrong → both FAILs present).
- [ ] **Step 2: Run → Expected build failure `undefined: StorageClassConfig`.**
- [ ] **Step 3: Implement** per Produces; list via `clients.Kube.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})`, then in-process name matching (no jq-style filtering).
- [ ] **Step 4: Tests pass** (`DONE 6 tests`), `./internal/preflight/...` green.
- [ ] **Step 5: CLI** `preflight storageclass` — flags `--classes` (default `dictycr-balanced`), `--provisioner` (default `pd.csi.storage.gke.io`), `kubeconfigFlag()`; wrapper splits CSV with `strings.Split(..., ",")` + trim.
- [ ] **Step 6: Wrapper** `check-storageclass classes="dictycr-balanced" provisioner="pd.csi.storage.gke.io"` body →
  `go run ./cmd/cluster-ops preflight storageclass --classes {{ quote(classes) }} --provisioner {{ quote(provisioner) }}`
  (bare `quote()` — no surrounding double quotes). Failing summary `PVCs will stay Pending. See docs/reference/pulumi/storage-class.md` becomes the wrapper's exit echo. Rename old recipe `check-storageclass-shell`.
- [ ] **Step 7: Contract + lint + docs** — `just lint-recipes` clean; `docs/reference/pulumi/storage-class.md` flag table → `cluster-ops preflight storageclass --help`; `just docs docs-lint` PASS.
- [ ] **Step 8: Commit.**
  `git add internal/preflight/backend/storageclass.go internal/preflight/backend/storageclass_test.go cmd/cluster-ops/preflight.go just_modules/pulumi.justfile docs/reference/pulumi/storage-class.md`
  `git commit -m "feat(preflight): migrate check-storageclass to Go"`

---

## Task 4: custodian seams + `validate-hardening` → `preflight hardening`

**Files:** Modify `internal/custodian/validate.go` (+ `validate_test.go`); create `internal/preflight/cluster/hardening.go`, `internal/preflight/cluster/hardening_test.go`; modify `cmd/cluster-ops/preflight.go`, `just_modules/cluster.justfile:239-310`; create `docs/reference/cluster/hardening.md`

Shell behavior (`cluster.justfile:239-310`): 5 components — cluster-autoscaler pods Running (`app=cluster-autoscaler`, kube-system), node-problem-detector DaemonSet `numberReady == desiredNumberScheduled`, metrics-server **reporting** (`kubectl top nodes`), cert-manager pods Running (`app.kubernetes.io/instance=cert-manager` in kube-system **or** `cert-manager`), node-local-dns pods Running (`k8s-app=node-local-dns`, kube-system).

**Interfaces:**
- Consumes: `preflight.Result`, `kube.Clients`; `custodian.HACheck{Name, Status, Message string}` with `Status ∈ {"pass","fail","warn","error"}` (see `internal/custodian/validate.go:33-39`).
- Produces (exact):
  ```go
  // internal/custodian/validate.go — refactor 4 pod-based checks to accept
  // kubernetes.Interface (was *kubernetes.Clientset) and export them; checkMetricsServer,
  // checkControlPlaneZoneSpread, checkBatchNodeTaints and ValidateHA stay as they are
  // (ValidateHA keeps its 7 checks and signature).
  func CheckClusterAutoscaler(ctx context.Context, cs kubernetes.Interface) HACheck
  func CheckNodeProblemDetector(ctx context.Context, cs kubernetes.Interface) HACheck
  func CheckCertManager(ctx context.Context, cs kubernetes.Interface) HACheck
  func CheckNodeLocalDNS(ctx context.Context, cs kubernetes.Interface) HACheck

  // internal/preflight/cluster/hardening.go
  // MetricsProbe reports whether the metrics API is serving (kubectl top parity).
  type MetricsProbe func(ctx context.Context) error
  // RestMetricsProbe probes GET /apis/metrics.k8s.io/v1beta1/nodes through
  // clients.Rest (transport error or 404 → error; served, even empty → nil).
  func RestMetricsProbe(clients kube.Clients) MetricsProbe
  type HardeningConfig struct {
      Clients kube.Clients
      Metrics MetricsProbe
  }
  // CheckHardening → 5 Results: 4 mapped custodian checks (HACheck.Status:
  // pass→StatusPass, fail→StatusFail, warn→StatusWarn, error→StatusFail;
  // Detail = HACheck.Message) + 1 metrics Result.
  func CheckHardening(cfg HardeningConfig) []IO.IO[preflight.Result]
  ```
  Detail wording parity: pass `Running` (pods) / `Running (n/n nodes)` (NPD); fail `Not found or not Running` / `n/m nodes ready`; metrics pass `Reporting metrics`, fail `not reporting (wait 60s and retry)` (probe error) or `metrics API error: <err>` (probe transport/404).

- [ ] **Step 1: Write the failing tests.**
  `internal/custodian/validate_test.go`: `TestExportedChecksUseInterface` — compile-time + behavior with `k8sfake.NewSimpleClientset` (autoscaler pod Running → `HACheck{Status:"pass"}`; NPD DaemonSet 1/2 ready → `Status:"fail"`, Message `1/2 nodes ready`).
  `internal/preflight/cluster/hardening_test.go`:

```go
package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestHardeningAllRunning(t *testing.T) {
	// fixtures: autoscaler pod Running; NPD DaemonSet numberReady==desired;
	// cert-manager pod Running (kube-system); node-local-dns pod Running.
	got := preflight.Run(CheckHardening(HardeningConfig{
		Clients: kube.Clients{Kube: k8sfake.NewSimpleClientset(/* fixtures */)},
		Metrics: func(context.Context) error { return nil },
	}))
	require.Len(t, got, 5)
	for _, r := range got {
		require.Equal(t, preflight.StatusPass, r.Status, "%s: %s", r.Name, r.Detail)
	}
}

func TestHardeningReportsEveryFailure(t *testing.T) {
	// fixtures: no autoscaler; NPD 1/2 ready; cert-manager + node-local-dns OK.
	got := preflight.Run(CheckHardening(HardeningConfig{
		Clients: kube.Clients{Kube: k8sfake.NewSimpleClientset(/* fixtures */)},
		Metrics: func(context.Context) error { return nil },
	}))
	var fails int
	for _, r := range got {
		if r.Status == preflight.StatusFail {
			fails++
		}
	}
	require.Equal(t, 2, fails, "autoscaler + NPD failures both listed")
}

func TestHardeningMetricsNotReporting(t *testing.T) {
	got := preflight.Run(CheckHardening(HardeningConfig{
		Clients: kube.Clients{Kube: k8sfake.NewSimpleClientset(/* all fixtures OK */)},
		Metrics: func(context.Context) error { return errors.New("metrics not reporting") },
	}))
	require.Equal(t, preflight.StatusFail, got[4].Status)
	require.Equal(t, "not reporting (wait 60s and retry)", got[4].Detail)
}

func TestHardeningMetricsAPIError(t *testing.T) {
	got := preflight.Run(CheckHardening(HardeningConfig{
		Clients: kube.Clients{Kube: k8sfake.NewSimpleClientset(/* all fixtures OK */)},
		Metrics: func(context.Context) error { return errors.New("connection refused") },
	}))
	require.Equal(t, "metrics API error: connection refused", got[4].Detail)
}
```

  (Fill fixture literals like the `datastore` package tests do; probe-error wording is decided by `CheckHardening` from the probe error's kind: `errMetricsNotReporting` sentinel via `errors.Is` → "not reporting…", anything else → "metrics API error: …". `RestMetricsProbe` wraps non-2xx/transport into those two classes without string matching.)

- [ ] **Step 2: Run → Expected build failure** (`kubernetes.Interface` mismatch in custodian tests / `undefined: CheckHardening`).
- [ ] **Step 3: Refactor custodian** per Produces (4 exports + `kubernetes.Interface`); `go build ./internal/custodian/` clean; `ValidateHA` behavior unchanged (its 7-check list untouched).
- [ ] **Step 4: Implement `CheckHardening` + `RestMetricsProbe`** per Produces (mapping table above).
- [ ] **Step 5: Tests pass**; `go test -race -count=1 ./internal/custodian/ ./internal/preflight/...` green.
- [ ] **Step 6: CLI + wrapper.** Subcommand `preflight hardening` (kubeconfig flag) → `CheckHardening(HardeningConfig{Clients: clients, Metrics: cluster.RestMetricsProbe(clients)})`. Wrapper `validate-hardening` body → `go run ./cmd/cluster-ops preflight hardening`; summary echoes (`All 5 hardening components running.` / `Some components failed. See Phase 7 for troubleshooting.`) become the wrapper's exit-code echoes. Rename old recipe `validate-hardening-shell`.
- [ ] **Step 7: Lint + docs** — `golangci-lint run ./internal/custodian/... ./internal/preflight/... ./cmd/cluster-ops/...` → `0 issues.`; `docs/reference/cluster/hardening.md` created per docs/STYLE.md (Behavior + Flags table).
- [ ] **Step 8: Commit.**
  `git add internal/custodian/ internal/preflight/cluster/ cmd/cluster-ops/preflight.go just_modules/cluster.justfile docs/reference/cluster/hardening.md`
  `git commit -m "feat(preflight): migrate validate-hardening with exported custodian checks"`

---

## Task 5: `validate-cluster` → `preflight validate-cluster`

**Files:** Modify `internal/kops/exec.go` (+test); create `internal/preflight/cluster/validate.go`, `internal/preflight/cluster/validate_test.go`; modify `cmd/cluster-ops/preflight.go`, `just_modules/cluster.justfile:319-337`; create `docs/reference/cluster/validate.md`

Shell behavior (`cluster.justfile:319-337`): derive `kn` (`--kops-name` else `KOPS_CLUSTER_NAME` else `<cluster>-k8s.local`) and `st` (`--state` else `KOPS_STATE_STORE` else `gs://kops-state-<cluster>`, trailing `/` stripped), run `kops validate cluster --name=<kn> --state=<st> --wait <waittime>m`. `internal/kops/exec.go` today: `runKopsIOE(args ...string) IOE.IOEither[error, string]`, `runKopsCaptureIOE(args ...string) IOE.IOEither[error, string]`, `runKopsCapture(args ...string) (string, error)` — all unexported.

**Interfaces:**
- Consumes: `internal/kops` capture runner; `preflight.Result`; `errors.Is/As` classification (no string matching).
- Produces (exact):
  ```go
  // internal/kops/exec.go — new exported wrapper (extend, don't duplicate):
  // ValidateClusterIOE runs `kops validate cluster --name=<name> --state=<state>
  // --wait <m>m` and returns captured stdout+stderr on success; on failure the
  // error wraps the exec error (errors.Is(exec.ErrNotFound) preserved for
  // missing binary; *exec.ExitError preserved for non-zero exits).
  func ValidateClusterIOE(name, state string, waitMinutes int) IOE.IOEither[error, string]

  // internal/preflight/cluster/validate.go
  type KopsValidator func(name, state string, waitMinutes int) (string, error)
  type ValidateConfig struct {
      Cluster   string // CLUSTER_NAME
      KopsName  string // raw override (may be empty)
      State     string // raw override (may be empty)
      WaitTime  int    // minutes, recipe default 20
      Validate  KopsValidator // production: func(n,s string,m int)(string,error){ return kops.ValidateClusterIOE(n,s,m)() either→(out,err) }
  }
  func DeriveKopsName(cluster, override string) string // override → os.Getenv("KOPS_CLUSTER_NAME") → cluster+"-k8s.local"
  func DeriveState(cluster, override string) string    // override → os.Getenv("KOPS_STATE_STORE") → "gs://kops-state-"+cluster; TrimSuffix "/"
  func CheckKopsValidate(cfg ValidateConfig) IO.IO[preflight.Result]
  ```
  Result mapping (one Result `Name: "kops validate cluster"`): success → PASS `cluster <kn> is ready`; `errors.Is(err, exec.ErrNotFound)` → FAIL `kops not found on PATH — run just prepare-tools`; `errors.Is(err, context.DeadlineExceeded)` → FAIL `timed out after <waittime>m waiting for <kn>`; `*exec.ExitError` → FAIL `kops validate cluster failed: <err>` with `Fix: run 'kops validate cluster --name=<kn> --state=<st>' for details`; other → FAIL `kops validate cluster error: <err>`. env lookup goes through `ValidateConfig` fields resolved **before** the Pipe (boundary struct seed; point-free seed rule).

- [ ] **Step 1: Write the failing tests** (`validate_test.go`, package `cluster`):
  `TestDeriveKopsName` (override > env > `<cluster>-k8s.local`; use `t.Setenv`) ·
  `TestDeriveState` (override > env > `gs://kops-state-<cluster>`; `gs://x/` → `gs://x`) ·
  `TestKopsValidatePass` (validator returns `Your cluster kn is ready\n` → PASS, Detail `cluster kn is ready`) ·
  `TestKopsValidateTimeout` (validator returns `context.DeadlineExceeded` → FAIL `timed out after 20m waiting for kn`) ·
  `TestKopsValidateBinaryMissing` (validator returns `exec.ErrNotFound`-wrapped → FAIL `kops not found on PATH — run just prepare-tools`) ·
  `TestKopsValidateExitError` (validator returns `&exec.ExitError{ProcessState: …}` from `exec.Command("false").Run()` — mirror `cmd/cluster-ops/forward_test.go`'s `TestExitCodeFromChild` technique — → FAIL Detail starts with `kops validate cluster failed: `).
- [ ] **Step 2: Run → Expected build failure `undefined: ValidateConfig`.**
- [ ] **Step 3: Extend `internal/kops`** with `ValidateClusterIOE` per Produces (tests there: args assembly `TestValidateClusterArgs` via a captured runner; missing binary classification preserved). Note: `internal/kops` tests need `kops` discoverable via asdf parent `.tool-versions` — run inside the repo tree.
- [ ] **Step 4: Implement `CheckKopsValidate`** (raw call in `IOE.TryCatchError`, classification in `MapLeft` using `errors.Is/As` only).
- [ ] **Step 5: Tests pass.**
- [ ] **Step 6: CLI** `preflight validate-cluster` — flags `--cluster/--kops-name/--state/--waittime` (defaults `""/""/""/20`, env fallbacks `CLUSTER_NAME`/`KOPS_CLUSTER_NAME`/`KOPS_STATE_STORE` resolved at the action edge into the `ValidateConfig` seed). Wrapper `validate-cluster` → `go run ./cmd/cluster-ops preflight validate-cluster --cluster {{ quote(cluster) }} --kops-name {{ quote(kops_name) }} --state {{ quote(state) }} --waittime {{ quote(waittime) }}`; rename old recipe `validate-cluster-shell`.
- [ ] **Step 7: Lint + docs** — `golangci-lint run ./internal/kops/... ./internal/preflight/... ./cmd/cluster-ops/...` → `0 issues.`; `docs/reference/cluster/validate.md` created.
- [ ] **Step 8: Commit.**
  `git add internal/kops/ internal/preflight/cluster/validate.go internal/preflight/cluster/validate_test.go cmd/cluster-ops/preflight.go just_modules/cluster.justfile docs/reference/cluster/validate.md`
  `git commit -m "feat(preflight): migrate validate-cluster with classified kops errors"`

---

## Task 6: `verify-cluster` composite → `preflight verify-cluster`

**Files:** Create `internal/preflight/cluster/verify.go`, `internal/preflight/cluster/verify_test.go`; modify `cmd/cluster-ops/preflight.go`, `just_modules/cluster.justfile:1473-1517`; create `docs/reference/cluster/verify.md`

Shell behavior (`cluster.justfile:1473-1517`): `validate-cluster` (with `--waittime`), kubeconfig existence + `export-kubeconfig` fix-up, `validate-kops-ha`, `validate-hardening`, `All validations passed.`. Go composite runs the **checks** read-only; kubeconfig auto-export stays in the wrapper.

**Interfaces:**
- Consumes: `CheckKopsValidate` (Task 5), `CheckHardening` (Task 4), custodian HA checks (Task 4 exports cover 4; HA also needs `checkControlPlaneZoneSpread` + `checkBatchNodeTaints` → export those two the same way: `custodian.CheckControlPlaneZoneSpread/CheckBatchNodeTaints(ctx, kubernetes.Interface) HACheck`), `kube.NewClients`.
- Produces (exact):
  ```go
  // HAChecker runs the 7-check HA suite against an existing client.
  type HAChecker func(ctx context.Context, cs kubernetes.Interface) []custodian.HACheck
  // HAValidator adapts custodian: RunHAChecks(ctx, cs) = the 7 checks in
  // ValidateHA's order (export custodian.RunHAChecks(ctx, cs) in Task 4 step 3
  // as the single seam; ValidateHA delegates to it).
  type VerifyConfig struct {
      Kubeconfig string
      Validate   ValidateConfig   // kops group — runs regardless of kubeconfig
      Hardening  HardeningConfig  // Metrics probe may be nil → skipped-as-FAIL below
      HA         HAChecker        // production: custodian.RunHAChecks
  }
  // VerifyCluster: group 1 = CheckKopsValidate (1 Result); group 2 = HA
  // (7 HAChecks → 7 Results, same mapping as Task 4); group 3 = CheckHardening
  // (5 Results). Kube-dependent groups whose client cannot be built report one
  // FAIL each: "not run — kubeconfig missing (run just gcp-cluster export-kubeconfig)"
  // — never silently skipped, never short-circuiting the kops group.
  func VerifyCluster(cfg VerifyConfig) []IO.IO[preflight.Result]
  ```

- [ ] **Step 1: Write the failing tests** (`verify_test.go`, package `cluster`):
  `TestVerifyClusterAllPass` (validator OK + fake clientset with HA/hardening fixtures + nil-error metrics probe → 13 Results all pass) ·
  `TestVerifyClusterReportsEveryGroup` (validator error + autoscaler absent + HA zone-spread fail fixture → FAILs from all three groups present in one run) ·
  `TestVerifyClusterKubeconfigMissing` (empty `Kubeconfig` + fake `kube.NewClients` error path: pass `Kubeconfig: "/nonexistent"`) → kops group Result still produced (validator stub returns pass), HA group = 1 FAIL `not run — kubeconfig missing (run just gcp-cluster export-kubeconfig)`, hardening group = same 1 FAIL (13 Results total).
  Build `VerifyConfig.HA`/`Hardening` in tests with stub `HAChecker` + fake clientset so no live cluster is needed.
- [ ] **Step 2: Run → Expected build failure `undefined: VerifyCluster`.**
- [ ] **Step 3: Implement** per Produces: `kube.NewClients(kubeconfig, "")` failure → the two kube-dependent groups emit their `not run` FAILs; success → groups build their Results. Kops group never depends on the kube client.
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: CLI + wrapper.** Subcommand `preflight verify-cluster` (flags `--cluster/--kops-name/--state/--waittime` + kubeconfig flag). Wrapper `verify-cluster`: keep the existing `export-kubeconfig` block verbatim (fix-up action), keep `=== 1/3 … ===` echoes, replace the three `just gcp-cluster …` invocations with one `go run ./cmd/cluster-ops preflight verify-cluster …` after the kubeconfig block; `All validations passed.` becomes the wrapper's success echo. Rename old recipe `verify-cluster-shell`.
- [ ] **Step 6: Lint + docs** — lint clean; `docs/reference/cluster/verify.md` created.
- [ ] **Step 7: Commit.**
  `git add internal/preflight/cluster/verify.go internal/preflight/cluster/verify_test.go cmd/cluster-ops/preflight.go just_modules/cluster.justfile docs/reference/cluster/verify.md`
  `git commit -m "feat(preflight): compose verify-cluster from the three cluster checks"`

---

## Task 7: `preflight-create` → `preflight create`

**Files:** Create `internal/preflight/cluster/create.go`, `internal/preflight/cluster/create_test.go`; modify `cmd/cluster-ops/preflight.go`, `just_modules/cluster.justfile:1676-1885`; create `docs/reference/kops/preflight-create.md`

Behavior source of truth: `cluster.justfile:1676-1885` (read it in full while implementing; the Detail strings below are the recipe's verbatim). Derivations: `kn` = `--kops-name`/`KOPS_CLUSTER_NAME`/`<c>-k8s.local`; `bn` = `--bucket-name`/`BUCKET_NAME`/`kops-state-<c>` (strip `gs://` and trailing `/`); `bucket_uri` = `gs://<bn>`; `st` = `--state`/`KOPS_STATE_STORE`/`bucket_uri`; `ssh_pub` = `--ssh-key`/`SSH_KEY`/`<invocation_dir>/credentials/<p>/k8sVM.pub`.

**Interfaces:**
- Consumes: `toolchain.CheckTools` (Task 2), `preflight.Result`, `exec`/file probes behind injection.
- Produces (exact):
  ```go
  // CreateProbes injects every external read (function fields — the LoginReader pattern).
  type CreateProbes struct {
      LookPath   func(bin string) (string, error)            // exec.LookPath
      RunCapture func(name string, args ...string) (string, error) // runs and captures (asdf current <tool>, gcloud config get-value …)
      Stat       func(path string) (fs.FileInfo, error)      // os.Stat
      ReadFile   func(path string) ([]byte, error)           // os.ReadFile (key JSON, manifests)
      DescribeImage func(ctx context.Context, project, image string) (exists bool, err error)
      DescribeImageFamily func(ctx context.Context, project, family string) (resolved string, err error)
      ListBucket func(ctx context.Context, uri string) (exists bool, err error)      // 404→(false,nil); forbidden→(false, errForbidden)
      ListBucketPath func(ctx context.Context, uri string) (exists bool, err error)  // <bucket>/<kn>/ semantics as the recipe
      ListInstances func(ctx context.Context, filter string) ([]string, error)       // gcloud compute instances list --filter="name ~ <c>"
  }
  // CreateEnv is the resolved operator environment (fields, not ambient reads).
  type CreateEnv struct {
      Cluster, Project, KopsName, State, BucketURI, SSHPub string
      ActiveConfig, ExpectedSA string // <p>-kops-cluster-creator / kops-cluster-creator@<p>.iam.gserviceaccount.com
      PinFile string                  // ${ASDF_DEFAULT_TOOL_VERSIONS_FILENAME:-.tool-versions}
      Root     string                 // invocation directory (manifest bundle + git checks)
  }
  type CreateConfig struct {
      Env    CreateEnv
      Probes CreateProbes
  }
  // CheckCreate → one Result per probe below, all reported (never short-circuit).
  func CheckCreate(cfg CreateConfig) []IO.IO[preflight.Result]
  ```
- [ ] **Step 1: Write the failing tests** (fixtures `// TEST CREDENTIAL, not real`) — one test per group, exact Detail asserted:
  1. `TestCreateEnvMissing` — `CLUSTER_NAME`+`PROJECT_ID` unset → 2 FAILs (`unset — enter cluster shell first: just cluster-env --env <env> --cluster <name>`); later groups still iterate but each FAILs fast with `not run — CLUSTER_NAME/PROJECT_ID unset` (mirrors the recipe's early exit while keeping the report complete).
  2. `TestCreateToolchainAndPins` — missing binary → `toolchain binaries: one or more tools missing on PATH — run 'just prepare-tools'` (reuse `toolchain.CheckTools` mapped into one group Result); pin mismatch → FAIL `tool pin kubectl installed '1.27.0' != pinned '1.28.8' in .tool-versions — run 'just prepare-tools'`; all match → PASS `toolchain pins: all tools match pinned versions in .tool-versions`.
  3. `TestCreateCredentials` — unset → `GOOGLE_APPLICATION_CREDENTIALS unset`; missing file → `key file not found: <path>`; wrong project → `key project (<p>) != PROJECT_ID (<p2>)`; wrong SA → `SA is '<e>', expected '<expected>' — rotate first with 'just gcp-cluster rotate-to-creator'`; OK → PASS `<email>`.
  4. `TestCreateShellIsolation` — unset → `CLOUDSDK_ACTIVE_CONFIG_NAME unset — enter cluster shell first: just cluster-env --env <env> --cluster <name>`; wrong name → `CLOUDSDK_ACTIVE_CONFIG_NAME is '<a>', expected '<e>'`; wrong gcloud project/account → `gcloud project is '<a>', expected '<e>'` / `gcloud account is '<a>', expected '<e>'`; OK → PASS `<cfg> (<account>)`.
  5. `TestCreateSSHKey` — missing pub → `SSH public key: not found: <path>`; wrong parent dir → `SSH key isolation: parent directory (<a>) != expected (credentials/<p>)`; missing private → `SSH private key: matching private key not found: <path>`; OK → PASS `SSH keypair: <path>`.
  6. `TestCreateManifestBundle` — missing files → `manifests: missing <path> — run 'just gcp-cluster bootstrap-bundle' first` (each file its own Result); project/name/configBase mismatch → `manifest project: cluster.yaml project (<a>) != PROJECT_ID (<p>)` etc.; `${VAR}` placeholders → `manifest template: unresolved ${VAR} placeholders found in bundle`; image family → `image family: '<img>' is an image family — pin exact image '<proj>/<resolved>'`; image missing → `image not found: image '<img>' not found in GCP project '<proj>'`; taints → `GCE taints: spec.taints found in <igs> — GCE rejects autoscaler taint labels containing slashes; use nodeLabels instead`; git dirty → `git status: uncommitted modifications in config/kops/<c>/ — review and commit first` (three variants: unstaged/staged/untracked); OK → PASS `manifest bundle: config/kops/<c>/ (<name>)` + PASS `node image: <img>` per image + PASS `git status: manifests committed and clean in Git`.
  7. `TestCreateStateStore` — the 5-outcome table (exact Details):
     | probe result | Result |
     |---|---|
     | bucket exists, `<kn>/` in state, instances running | FAIL `state store: cluster '<kn>' already has running VMs in GCP — use 'just gcp-cluster apply-cluster' for updates, or 'delete-cluster' first` |
     | bucket exists, `<kn>/` in state, no instances | PASS `state store: bucket exists (<uri>), manifests in state, compute not yet provisioned (clean to create/resume)` |
     | bucket exists, `<kn>/` absent | PASS `state store: bucket exists (<uri>), no active cluster (clean for creation)` |
     | bucket exists, path check errors | FAIL `state store: error checking cluster in <uri>: <err>` |
     | bucket absent (404) | PASS `state store: bucket does not exist yet (<uri> will be created by create-cluster)` |
     | bucket query errors | FAIL `state store: error querying bucket <uri>: <err>` |
  8. `TestCreateProbeForbidden` — `ListBucket` returns `errForbidden` → FAIL `state store: error querying bucket <uri>: <err>` (never "does not exist"; Review Focus 5).
  9. `TestCreateAllGreen` — every probe green → zero `StatusFail`; summary handled by wrapper echo `All pre-flight checks passed. Ready for create-cluster.`
- [ ] **Step 2: Run → Expected build failure `undefined: CreateProbes`.**
- [ ] **Step 3: Implement** `CheckCreate` per Produces (probe order = recipe order; raw probes in `IOE.TryCatchError`, classification in `MapLeft`; sentinel `errForbidden` matched via `errors.Is`, never by message).
- [ ] **Step 4: Tests pass.**
- [ ] **Step 5: CLI** `preflight create` — flags `--cluster/--project/--kops-name/--state/--bucket-name/--ssh-key` with the derivation chain above resolved at the action edge into `CreateEnv`; `CHECK_LABEL_WIDTH=24`. Production `CreateProbes` wiring: `exec.LookPath`/`exec.Command(...).CombinedOutput` (`asdf current <tool>`, `gcloud config get-value account|project`), `os.Stat`/`os.ReadFile`, GCP image + GCS probes via the existing `internal/gcp` clients (extend `internal/gcp` with `DescribeImage`/`ListBucket`-style readers modeled on `CheckBucketVersioning`).
- [ ] **Step 6: Wrapper** `preflight-create` → `go run ./cmd/cluster-ops preflight create …` (params via `quote()`); keep the trailing `just gcp-cluster _cluster-config-report --cluster "${c}"` and summary echoes in the wrapper. Rename old recipe `preflight-create-shell`. `create-cluster` stays unchanged (it already invokes `just gcp-cluster preflight-create`).
- [ ] **Step 7: Lint + docs** — lint clean; `docs/reference/kops/preflight-create.md` created.
- [ ] **Step 8: Commit.**
  `git add internal/preflight/cluster/create.go internal/preflight/cluster/create_test.go internal/gcp/ cmd/cluster-ops/preflight.go just_modules/cluster.justfile docs/reference/kops/preflight-create.md`
  `git commit -m "feat(preflight): migrate preflight-create with injectable probes"`

---

## Task 8: Contract tests + docs + final gate

**Files:** Modify `scripts/test-bootstrap-bundle.sh`; create `scripts/test-cluster-validation.sh`; modify `Justfile` (add `test-cluster-validation` to `check` deps); modify `docs/plans/go-migration/README.md` (status column only)

- [ ] **Step 1: Contract tests.**
  `scripts/test-bootstrap-bundle.sh`: mock `go` on PATH with the argv recorder from `scripts/test-datastore-recipes.sh` and assert `preflight create` is invoked **before** `kops create cluster` (the spec's order contract).
  New `scripts/test-cluster-validation.sh`: assert `just gcp-cluster verify-cluster` invokes `preflight verify-cluster` (argv recorded) after the kubeconfig block and propagates exit codes (`MOCK_GO_EXIT=0` → recipe 0; `MOCK_GO_EXIT=1` → recipe non-zero). Same shape as `test-datastore-recipes.sh` sections 1–2. Wire `test-cluster-validation` into `Justfile` `check` deps (next to `test-datastore-recipes`) via a thin recipe.
- [ ] **Step 2: Run contract suite.** `bash scripts/test-bootstrap-bundle.sh && bash scripts/test-cluster-validation.sh && bash scripts/test-datastore-recipes.sh` → each prints its `PASSED` line; `just lint-recipes` → `no recipe findings`.
- [ ] **Step 3: Docs sync.** Every migrated recipe's `docs/reference/<area>/*.md` flag table points at `cluster-ops preflight <name> --help`; `just docs docs-lint` → `PASS` (capture `echo "exit=$?"`).
- [ ] **Step 4: Acceptance spot-checks** (the spec's rg gates): `rg 'us-central1' internal/` → only test fixtures; `rg 'curl.*-u.*\$' internal/` → nothing; `rg -n 'kubectl get|gcloud ' internal/preflight/` → nothing (all probes via seams).
- [ ] **Step 5: Final gate.**
  - `go build ./... && go vet ./... && go vet -tags=integration ./internal/...` → no output
  - `go test -race -count=1 ./internal/preflight/... ./internal/custodian/ ./internal/kops/ ./cmd/cluster-ops/` → all `ok`
  - `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → `DONE <N> tests, 1 skipped`, no failures
  - `golangci-lint run --build-tags=integration ./internal/... ./cmd/cluster-ops/...` → `0 issues.`
  - `just check; echo "exit=$?"` → every contract script prints its `PASSED` line, `exit=0` (worktree needs the gitignored `.env.prod.dcr-kube1` **and** `.tool-versions.prod.dcr-kube1` at the repo root — review-fixes ledger note)
- [ ] **Step 6: Commit.**
  `git add scripts/test-bootstrap-bundle.sh scripts/test-cluster-validation.sh Justfile docs/plans/go-migration/README.md`
  `git commit -m "test(recipes): assert preflight order for cluster validation recipes"`
  (Never stage `docs/plans/**` beyond the README status column; leave plan documents uncommitted per operator instruction.)

---

## Deferred (not in this plan)

- k3d/live-cluster integration suite (`//go:build integration`); the spec's "output matches shell line-for-line on experiments/prod" acceptance (no cluster in scope here).
- Replay verification of THIS plan on a copy of `47f086a` (the header's prerequisite before execution).
- `scripts/test-fix-messages.sh` (Result.Fix names real recipes) — spec risk #5 mitigation.
- `logto verify`, `cluster-status`, `drift-manifests`, `registry-show` stay shell per the spec's non-goals.
- Deleting the `_name-shell` renamed recipes one release after migration (spec risk #6).
- Further `internal/custodian` consolidation beyond the exported checks this plan needs.
