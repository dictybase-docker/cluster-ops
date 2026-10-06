# Go Migration Phase 1 — Backend Deploy Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Commits:** The human or main agent must run `/ai-commit-gen:commit` (subagents cannot invoke skills) after each task's `git add` + `git commit -m` step.

**Goal:** Port the four backend deploy-gate checks (`check-backend-prereqs`, `verify-deployer-access`, `check-deploy-credentials`, `check-backend`) from shell to a `cluster-ops preflight` command, fixing every identified shell bug, on a shared foundation (`internal/preflight`, `internal/kube`, `internal/registry`) for the later phases.

**Architecture:** Each check is an `IO.IO[preflight.Result]`; `preflight.Run` runs every check and `Print` keeps the `check-helpers.sh` output format. Kubernetes access goes through client-go (`kubernetes.Interface`, fake clientset in tests); the ArangoDB probe uses a terratest service tunnel on a free local port behind a `Fatal`-safe adapter; GCP calls use the official SDKs, injected where the logic needs unit tests. Recipes keep their names and flags and become thin `go run ./cmd/cluster-ops preflight …` wrappers, so callers and the guide do not change.

**Tech Stack:** Go 1.26, fp-go v2 (`github.com/IBM/fp-go/v2` v2.3.146), client-go v0.37.1, terratest v1.0.1 (`modules/k8s`, `logger`, `testing`), `cloud.google.com/go/{kms,storage}`, `google.golang.org/api/{iam,cloudresourcemanager}/v1`, urfave/cli v2, testify/require, gotestsum, golangci-lint.

**Verified:** Tasks 3–11 were executed test-first on a clean copy of the branch: each test fails before its implementation and passes after; `go build ./...`, `go vet`, 71 unit tests and `golangci-lint` (0 issues) are green. Tasks 12–15 were applied and every contract script in `just check` passes (except the pre-existing `test-register-cluster` environment issue — see Task 2). The Task 16 integration tests pass against a throwaway k3d cluster (k3s v1.28.8).

---

## File Structure

**Created:**
- `internal/registry/entry.go` — `Entry`, `LoadByName`, `LoadByStack`
- `internal/registry/kms.go` — `KMSKey`, `ParseKMSKey`
- `internal/registry/entry_test.go` — registry load and URI parse tests
- `internal/preflight/result.go` — `Result`, `Status`, `Run`, `Print`, `Failed`
- `internal/preflight/result_test.go` — no short-circuit, `Failed`, `Print` plain/colour format
- `internal/kube/client.go` — `Clients`, `NewClients`, `Reason`, `Classify`
- `internal/kube/client_test.go` — `Classify` table (incl. wrapped errors)
- `internal/kube/tunnel.go` — `OpenServiceTunnel`, `Tunnel`, terratest adapter (`runT`, `parsePort`)
- `internal/kube/tunnel_test.go` — `Fatal`/`FailNow`/`Errorf` → error, non-sentinel re-panic, port parsing
- `internal/preflight/backend/prereqs.go` — `PrereqsChecks`, namespace/Secret/database/port checks, `ProbeDatabase`
- `internal/preflight/backend/prereqs_test.go` — fake-clientset checks; shared fixture constants
- `internal/preflight/backend/probe_test.go` — cause naming, HTTP status mapping, decoy-port regression
- `internal/preflight/backend/deployer.go` — `DeployerChecks`, key project, cluster access, KMS round-trip, `KopsExport`
- `internal/preflight/backend/deployer_test.go` — fake exporter/clients, key-project cases, kubeconfig cleanup
- `internal/preflight/backend/credentials.go` — `CheckIAMPermissions(With)`, `ParseRoles`
- `internal/preflight/backend/credentials_test.go` — batching, every-missing listing, dedupe, API error
- `internal/preflight/backend/pulumi.go` — `PulumiChecks`, env/key/bucket/KMS/login checks, `ParseWhoami`
- `internal/preflight/backend/pulumi_test.go` — env naming, skips, exact login match, whoami parsing
- `internal/preflight/backend/integration_test.go` — `//go:build integration`, real API server + tunnel
- `cmd/cluster-ops/preflight.go` — `preflightCommand`, actions, `reportGate`
- `cmd/cluster-ops/preflight_test.go` — env defaults, input errors, exit code
- `scripts/test-pulumi-backend.sh` — contract test for `check-backend`

**Modified:**
- `go.mod` / `go.sum` — `github.com/gruntwork-io/terratest v1.0.1` (Task 6)
- `cmd/cluster-ops/main.go` — register `preflightCommand()`
- `just_modules/pulumi.justfile` — `check-backend-prereqs` (drops `--arango-port`) and `check-backend` become wrappers
- `just_modules/cluster.justfile` — `verify-deployer-access` becomes a wrapper
- `just_modules/ci.justfile` — `check-deploy-credentials` becomes a wrapper
- `scripts/test-backend-prereqs.sh` — mock `go` on PATH; flag pass-through, exit propagation
- `scripts/test-ci-recipes.sh` — sections 1–2 only (sections 3–6 unchanged)
- `Justfile` — `test-pulumi-backend` (in `check:`), opt-in `test-preflight-integration` (not in `check:`)
- `docs/reference/backend/prerequisites.md`, `docs/reference/backend/ci-credentials.md`, `docs/reference/pulumi/check-backend.md` — Behavior/Failures text

**Deleted:**
- `scripts/pulumi/check-backend.sh` — replaced by `cluster-ops preflight pulumi-backend` (`scripts/lib/check-helpers.sh` stays; other recipes source it)

**Unchanged on purpose:**
- `scripts/test-backend-bootstrap.sh`, `scripts/test-backend-services.sh` — they mock the nested `just` calls, so the rewired recipes pass them as-is
- `docs/backend-service-deploy.md` — recipe names and flags are unchanged

---

## Task 1: Create git-bug Issue

**Files:** None (git-bug only)

- [ ] Run `/git-bug new plan "Go migration phase 1: backend deploy gate" "Port check-backend-prereqs, verify-deployer-access, check-deploy-credentials, and check-backend to Go with shared preflight/kube/registry foundation. Fixes: local port collision (terratest tunnel), password in argv (in-process http.Request), kubectl error conflation (kube.Classify), KMS URI hardcoded location, IAM role check exits on first miss. Adds regression tests for each bug. Deliverables: cluster-ops preflight <subcommand>, recipes become thin wrappers, contract tests rewritten for PATH mocks of go run, integration test against k3d."`
- [ ] Capture issue ID (appears in output as `Created bug <id>`)

**Expected:** Issue created, ID noted for Task 22 (close step).

---

## Task 2: Baseline Green Check

**Files:** None (verification only)

- [ ] Run `just check` to confirm the branch is green before making changes
- [ ] Expected: all contract tests pass, lint clean, build succeeds
- [ ] Note: `scripts/test-register-cluster.sh` section 3 needs the gitignored `.env.prod.dcr-kube1` file. Run from the main checkout or copy that file into the worktree.
- [ ] Expected: exit code 0

**Expected:** Baseline green; ready for implementation.

---

## Task 3: Create `internal/registry` — Entry and KMS URI parsing

**Files:** Create `internal/registry/entry_test.go`, `internal/registry/entry.go`, `internal/registry/kms.go`

One source of truth for cluster identity: every gate reads project, namespace, kops state and the KMS key from `config/clusters/<name>.yaml`, never ambient gcloud config. `ParseKMSKey` takes the location from the URI — the shell hardcoded `locations/us-central1` (`just_modules/cluster.justfile:2132`).

- [ ] **Step 1: Write the failing test.** Write `internal/registry/entry_test.go`:

```go
package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/stretchr/testify/require"
)

const testCluster = "dcr-kube1"

func TestLoadByName(t *testing.T) {
	dir := t.TempDir()
	content := `cluster: dcr-kube1
stack: prod
gcp_project: dcr-kube1
namespace: prod
kms_secrets_provider: gcpkms://projects/dcr-kube1/locations/us-central1/keyRings/dcr-kube1/cryptoKeys/dcr-kube1
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dcr-kube1.yaml"), []byte(content), 0o600))

	result := registry.LoadByName(dir, testCluster)
	require.True(t, E.IsRight(result))
	entry := F.Pipe1(result, E.GetOrElse(func(_ error) registry.Entry { return registry.Entry{} }))
	require.Equal(t, testCluster, entry.Cluster)
	require.Equal(t, "prod", entry.Stack)
}

func TestLoadByStack(t *testing.T) {
	dir := t.TempDir()
	content := `cluster: dcr-kube1
stack: prod
gcp_project: dcr-kube1
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dcr-kube1.yaml"), []byte(content), 0o600))

	result := registry.LoadByStack(dir, "prod")
	require.True(t, E.IsRight(result))
	entry := F.Pipe1(result, E.GetOrElse(func(_ error) registry.Entry { return registry.Entry{} }))
	require.Equal(t, testCluster, entry.Cluster)
}

func TestLoadByStackNotFound(t *testing.T) {
	dir := t.TempDir()
	result := registry.LoadByStack(dir, "missing")
	require.True(t, E.IsLeft(result))
}

func TestParseKMSKey(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantErr  bool
		expected registry.KMSKey
	}{
		{
			name: "valid URI",
			uri:  "gcpkms://projects/dcr-kube1/locations/us-central1/keyRings/dcr-kube1/cryptoKeys/dcr-kube1",
			expected: registry.KMSKey{
				Project:  testCluster,
				Location: "us-central1",
				KeyRing:  testCluster,
				Key:      testCluster,
			},
		},
		{
			name: "different location",
			uri:  "gcpkms://projects/foo/locations/us-east1/keyRings/bar/cryptoKeys/baz",
			expected: registry.KMSKey{
				Project:  "foo",
				Location: "us-east1",
				KeyRing:  "bar",
				Key:      "baz",
			},
		},
		{
			name:    "malformed URI",
			uri:     "gcpkms://invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := registry.ParseKMSKey(tt.uri)
			if tt.wantErr {
				require.True(t, E.IsLeft(result))
			} else {
				require.True(t, E.IsRight(result))
				key := F.Pipe1(result, E.GetOrElse(func(_ error) registry.KMSKey { return registry.KMSKey{} }))
				require.Equal(t, tt.expected, key)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/registry/...`

Expected: FAIL — `no non-test Go files in …/internal/registry` (`[build failed]`)

- [ ] **Step 3: Write the implementation.** Write `internal/registry/entry.go`:

```go
package registry

import (
	"fmt"
	"os"
	"path/filepath"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	"gopkg.in/yaml.v3"
)

// Entry represents a cluster registry entry from config/clusters/<name>.yaml
type Entry struct {
	Cluster            string `yaml:"cluster"`
	Stack              string `yaml:"stack"`
	KopsState          string `yaml:"kops_state"`
	GCPProject         string `yaml:"gcp_project"`
	KMSSecretsProvider string `yaml:"kms_secrets_provider"`
	PulumiState        string `yaml:"pulumi_state"`
	Namespace          string `yaml:"namespace"`
	CIEnv              string `yaml:"ci_env"`
	KopsVersion        string `yaml:"kops_version"`
	KubectlVersion     string `yaml:"kubectl_version"`
	PulumiVersion      string `yaml:"pulumi_version"`
}

// LoadByName loads the registry entry for the given cluster name.
func LoadByName(dir, name string) E.Either[error, Entry] {
	path := filepath.Join(dir, name+".yaml")
	return loadEntry(path)
}

// LoadByStack loads the registry entry matching the given stack name.
func LoadByStack(dir, stack string) E.Either[error, Entry] {
	return F.Pipe1(
		findEntryByStack(dir, stack),
		E.Chain(loadEntry),
	)
}

func findEntryByStack(dir, stack string) E.Either[error, string] {
	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return E.Left[string](fmt.Errorf("failed to list registry entries: %w", err))
	}
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var e Entry
		if err := yaml.Unmarshal(data, &e); err != nil {
			continue
		}
		if e.Stack == stack {
			return E.Right[error](path)
		}
	}
	return E.Left[string](fmt.Errorf("no registry entry with stack '%s' (checked %s/*.yaml)", stack, dir))
}

func loadEntry(path string) E.Either[error, Entry] {
	data, err := os.ReadFile(path)
	if err != nil {
		return E.Left[Entry](fmt.Errorf(
			"no registry entry %s — create it with just gcp-cluster register-cluster: %w", path, err,
		))
	}
	var e Entry
	if err := yaml.Unmarshal(data, &e); err != nil {
		return E.Left[Entry](fmt.Errorf("failed to parse registry entry %s: %w", path, err))
	}
	return E.Right[error](e)
}
```

- [ ] **Step 4: Write the implementation.** Write `internal/registry/kms.go`:

```go
package registry

import (
	"fmt"
	"regexp"

	E "github.com/IBM/fp-go/v2/either"
)

// KMSKey represents a parsed GCP KMS key URI.
type KMSKey struct {
	Project  string
	Location string
	KeyRing  string
	Key      string
}

var kmsURIPattern = regexp.MustCompile(
	`^gcpkms://projects/([^/]+)/locations/([^/]+)/keyRings/([^/]+)/cryptoKeys/([^/?]+)`,
)

// ParseKMSKey parses a GCP KMS URI into its components.
// Format: gcpkms://projects/PROJECT/locations/LOCATION/keyRings/RING/cryptoKeys/KEY
func ParseKMSKey(uri string) E.Either[error, KMSKey] {
	matches := kmsURIPattern.FindStringSubmatch(uri)
	if matches == nil {
		return E.Left[KMSKey](fmt.Errorf("malformed KMS URI: %s", uri))
	}
	return E.Right[error](KMSKey{
		Project:  matches[1],
		Location: matches[2],
		KeyRing:  matches[3],
		Key:      matches[4],
	})
}
```

- [ ] **Step 5: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/registry/...`

Expected: `DONE 7 tests` with no failures.

- [ ] **Step 6: Lint.**

Run: `golangci-lint run ./internal/registry/...`

Expected: `0 issues.`

- [ ] **Step 7: Commit.**

```bash
git add internal/registry/entry_test.go internal/registry/entry.go internal/registry/kms.go
git commit -m "feat(registry): load cluster registry entries and parse gcpkms URIs"
```


---

## Task 4: Create `internal/preflight` — Result, Run, Print, Failed

**Files:** Create `internal/preflight/result_test.go`, `internal/preflight/result.go`

The gate contract shared by every check: `Run` executes **every** check (no short-circuit — the operator sees every failure at once), `Print` emits the same `PASS`/`FAIL`/`INFO` lines as `scripts/lib/check-helpers.sh` (honours `CHECK_COLOR`), `Failed` drives exit 1.

- [ ] **Step 1: Write the failing test.** Write `internal/preflight/result_test.go`:

```go
package preflight_test

import (
	"bytes"
	"testing"

	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
)

func TestRun_NoShortCircuit(t *testing.T) {
	order := []string{}
	checks := []IO.IO[preflight.Result]{
		func() preflight.Result {
			order = append(order, "check1")
			return preflight.Result{Name: "check1", Status: preflight.StatusFail}
		},
		func() preflight.Result {
			order = append(order, "check2")
			return preflight.Result{Name: "check2", Status: preflight.StatusPass}
		},
		func() preflight.Result {
			order = append(order, "check3")
			return preflight.Result{Name: "check3", Status: preflight.StatusFail}
		},
	}

	results := preflight.Run(checks)
	require.Len(t, results, 3, "Run must execute all checks, not short-circuit")
	require.Equal(t, []string{"check1", "check2", "check3"}, order)
	require.Equal(t, preflight.StatusFail, results[0].Status)
	require.Equal(t, preflight.StatusPass, results[1].Status)
	require.Equal(t, preflight.StatusFail, results[2].Status)
}

func TestFailed(t *testing.T) {
	require.False(t, preflight.Failed([]preflight.Result{}))
	require.False(t, preflight.Failed([]preflight.Result{
		{Status: preflight.StatusPass},
		{Status: preflight.StatusInfo},
	}))
	require.True(t, preflight.Failed([]preflight.Result{
		{Status: preflight.StatusPass},
		{Status: preflight.StatusFail},
	}))
}

func TestPrint_PlainFormat(t *testing.T) {
	t.Setenv("CHECK_COLOR", "0")
	var buf bytes.Buffer
	results := []preflight.Result{
		{Name: "namespace prod", Detail: "exists", Status: preflight.StatusPass},
		{
			Name:   "secret order",
			Detail: "not found",
			Fix:    "create it with the ArangoDB credentials",
			Status: preflight.StatusFail,
		},
		{Name: "port 9250", Detail: "in range", Status: preflight.StatusPass},
	}
	preflight.Print(&buf, results)
	output := buf.String()

	// Plain format: "PASS  ", "FAIL  ", no ANSI codes
	require.Contains(t, output, "PASS  namespace prod exists\n")
	require.Contains(t, output, "FAIL  secret order not found — create it with the ArangoDB credentials\n")
	require.Contains(t, output, "PASS  port 9250 in range\n")
	require.NotContains(t, output, "\033[") // No ANSI codes
}

func TestPrint_ColorFormat(t *testing.T) {
	t.Setenv("CHECK_COLOR", "1")
	var buf bytes.Buffer
	results := []preflight.Result{
		{Name: "test", Detail: "ok", Status: preflight.StatusPass},
	}
	preflight.Print(&buf, results)
	output := buf.String()

	// Color format: ANSI codes present
	require.Contains(t, output, "\033[") // ANSI codes present
}

func TestPrint_NoFix(t *testing.T) {
	t.Setenv("CHECK_COLOR", "0")
	var buf bytes.Buffer
	results := []preflight.Result{
		{Name: "port 9250", Detail: "in range", Status: preflight.StatusPass, Fix: ""}, // No Fix
	}
	preflight.Print(&buf, results)
	output := buf.String()

	// No " — " suffix when Fix is empty
	require.Equal(t, "PASS  port 9250 in range\n", output)
	require.NotContains(t, output, " — ")
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/...`

Expected: FAIL — `no non-test Go files in …/internal/preflight` (`[build failed]`)

- [ ] **Step 3: Write the implementation.** Write `internal/preflight/result.go`:

```go
package preflight

import (
	"fmt"
	"io"
	"os"

	IO "github.com/IBM/fp-go/v2/io"
)

// Status represents a check result status.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusWarn Status = "warn"
	StatusInfo Status = "info"
)

// Result represents a single preflight check result.
type Result struct {
	Name   string
	Detail string
	Fix    string // Only for StatusFail
	Status Status
}

// Run executes all checks and returns their results.
// Never short-circuits — always runs every check.
func Run(checks []IO.IO[Result]) []Result {
	results := make([]Result, len(checks))
	for i, check := range checks {
		results[i] = check()
	}
	return results
}

// Print writes results to w in the same format as scripts/lib/check-helpers.sh.
// Honors CHECK_COLOR env var: "0" disables ANSI colors.
func Print(w io.Writer, results []Result) {
	useColor := os.Getenv("CHECK_COLOR") != "0"
	for _, r := range results {
		prefix := formatPrefix(r.Status, useColor)
		if r.Fix != "" {
			fmt.Fprintf(w, "%s%s %s — %s\n", prefix, r.Name, r.Detail, r.Fix)
		} else {
			fmt.Fprintf(w, "%s%s %s\n", prefix, r.Name, r.Detail)
		}
	}
}

// Failed reports whether any result has StatusFail.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == StatusFail {
			return true
		}
	}
	return false
}

func formatPrefix(status Status, useColor bool) string {
	switch status {
	case StatusPass:
		if useColor {
			return "\033[32mPASS\033[0m  "
		}
		return "PASS  "
	case StatusFail:
		if useColor {
			return "\033[31mFAIL\033[0m  "
		}
		return "FAIL  "
	case StatusWarn:
		if useColor {
			return "\033[33mWARN\033[0m  "
		}
		return "WARN  "
	case StatusInfo:
		if useColor {
			return "\033[34mINFO\033[0m  "
		}
		return "INFO  "
	default:
		return "UNKN  "
	}
}
```

- [ ] **Step 4: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/...`

Expected: `DONE 5 tests` with no failures.

- [ ] **Step 5: Lint.**

Run: `golangci-lint run ./internal/preflight/...`

Expected: `0 issues.`

- [ ] **Step 6: Commit.**

```bash
git add internal/preflight/result_test.go internal/preflight/result.go
git commit -m "feat(preflight): add check results, runner and check-helpers printer"
```


---

## Task 5: Create `internal/kube` — NewClients and Classify

**Files:** Create `internal/kube/client_test.go`, `internal/kube/client.go`

`Clients.Kube` is a `kubernetes.Interface`, so tests use `k8s.io/client-go/kubernetes/fake`. `Classify` maps API errors to not-found / forbidden / unauthorized / unreachable — the input that lets checks name the real cause. The shell's `kubectl get … >/dev/null 2>&1` reported all four as missing (`just_modules/pulumi.justfile:686,697`).

- [ ] **Step 1: Write the failing test.** Write `internal/kube/client_test.go`:

```go
package kube_test

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected kube.Reason
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: kube.ReasonOther,
		},
		{
			name:     "NotFound",
			err:      apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "namespaces"}, "test"),
			expected: kube.ReasonNotFound,
		},
		{
			name: "Forbidden",
			err: apierrors.NewForbidden(
				schema.GroupResource{Resource: "deployments"}, "test", errors.New("denied"),
			),
			expected: kube.ReasonForbidden,
		},
		{
			name:     "Unauthorized",
			err:      apierrors.NewUnauthorized("no token"),
			expected: kube.ReasonUnauthorized,
		},
		{
			name:     "connection refused",
			err:      &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")},
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "connection refused (substring)",
			err:      fmt.Errorf("dial tcp: connection refused"),
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "no such host",
			err:      fmt.Errorf("lookup failed: no such host"),
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "i/o timeout",
			err:      fmt.Errorf("context deadline exceeded: i/o timeout"),
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "other error",
			err:      fmt.Errorf("some other error"),
			expected: kube.ReasonOther,
		},
		{
			name:     "wrapped NotFound",
			err:      fmt.Errorf("outer: %w", apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "foo")),
			expected: kube.ReasonNotFound,
		},
		{
			name:     "wrapped Forbidden",
			err:      fmt.Errorf("outer: %w", apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "bar", nil)),
			expected: kube.ReasonForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := kube.Classify(tt.err)
			require.Equal(t, tt.expected, reason, "Classify(%v)", tt.err)
		})
	}
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/kube/...`

Expected: FAIL — `no non-test Go files in …/internal/kube` (`[build failed]`)

- [ ] **Step 3: Write the implementation.** Write `internal/kube/client.go`:

```go
package kube

import (
	"fmt"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients holds Kubernetes clients and configuration.
type Clients struct {
	Kube kubernetes.Interface
	Rest *rest.Config
}

// Reason represents classified Kubernetes error reasons.
type Reason int

const (
	ReasonNotFound Reason = iota
	ReasonForbidden
	ReasonUnauthorized
	ReasonUnreachable
	ReasonOther
)

// NewClients creates Kubernetes clients from kubeconfig.
// Empty kubeconfig uses default loading rules.
// Empty context uses current-context from kubeconfig.
func NewClients(kubeconfig, context string) E.Either[error, Clients] {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		configOverrides.CurrentContext = context
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		configOverrides,
	)

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return E.Left[Clients](fmt.Errorf("failed to load kubeconfig: %w", err))
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return E.Left[Clients](fmt.Errorf("failed to create kubernetes client: %w", err))
	}

	return E.Right[error](Clients{
		Kube: clientset,
		Rest: config,
	})
}

// Classify maps a Kubernetes error to a Reason for distinct Fix text.
func Classify(err error) Reason {
	if err == nil {
		return ReasonOther
	}
	if errors.IsNotFound(err) {
		return ReasonNotFound
	}
	if errors.IsForbidden(err) {
		return ReasonForbidden
	}
	if errors.IsUnauthorized(err) {
		return ReasonUnauthorized
	}
	// Unreachable: connection refused, timeout, DNS failure
	// client-go doesn't expose a specific check, so inspect message
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") {
		return ReasonUnreachable
	}
	return ReasonOther
}
```

- [ ] **Step 4: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/kube/...`

Expected: `DONE 12 tests` with no failures.

- [ ] **Step 5: Lint.**

Run: `golangci-lint run ./internal/kube/...`

Expected: `0 issues.`

- [ ] **Step 6: Commit.**

```bash
git add internal/kube/client_test.go internal/kube/client.go
git commit -m "feat(kube): add client construction and API error classification"
```


---

## Task 6: Create `internal/kube` — terratest service tunnel

**Files:** Create `internal/kube/tunnel_test.go`, `internal/kube/tunnel.go`, modify `go.mod`, `go.sum`

Replaces `kubectl port-forward` on fixed port 18529 + `nc -z` (false pass whenever anything else holds that port). terratest v1.0.1 facts the adapter is built around: `Tunnel.ForwardPortE` internally calls the non-E `GetService`, which reports failure through `t.Fatal`; there is no context and no reconnect; `Close` closes a channel and panics on reuse. The adapter turns `Fatal`/`FailNow` into an ordinary error (a naive recover returns a nil tunnel **and a nil error** — the tests pin that), records `Error`/`Errorf` as failure, and re-raises any other panic.

- [ ] **Step 1: Write the failing test.** Write `internal/kube/tunnel_test.go`:

```go
package kube

import (
	"errors"
	"testing"

	ttesting "github.com/gruntwork-io/terratest/modules/testing"
	"github.com/stretchr/testify/require"
)

// terratest's Tunnel.ForwardPortE calls the non-E GetService internally, which
// reports failure through t.Fatal. These tests pin the adapter contract that
// turns those calls into ordinary errors instead of crashing the CLI.

const adapterName = "tunnel"

var errTest = errors.New("forward failed")

func TestRunTFatalBecomesError(t *testing.T) {
	got, err := runT(
		&testingAdapter{name: adapterName},
		func(tt ttesting.TestingT) (*int, error) {
			tt.Fatalf("service %s not found", "arangodb")
			return new(int), nil
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "service arangodb not found")
	require.Nil(t, got)
}

func TestRunTFailNowBecomesError(t *testing.T) {
	_, err := runT(
		&testingAdapter{name: adapterName},
		func(tt ttesting.TestingT) (int, error) {
			tt.FailNow()
			return 1, nil
		},
	)
	require.Error(t, err)
}

func TestRunTRecordedErrorFailsTheCall(t *testing.T) {
	_, err := runT(
		&testingAdapter{name: adapterName},
		func(tt ttesting.TestingT) (int, error) {
			tt.Errorf("pod %s not ready", "arangodb-0")
			return 1, nil
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "pod arangodb-0 not ready")
}

func TestRunTReturnsFnError(t *testing.T) {
	_, err := runT(
		&testingAdapter{name: adapterName},
		func(_ ttesting.TestingT) (int, error) {
			return 0, errTest
		},
	)
	require.ErrorIs(t, err, errTest)
}

func TestRunTSuccess(t *testing.T) {
	got, err := runT(
		&testingAdapter{name: adapterName},
		func(_ ttesting.TestingT) (int, error) {
			return 42, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, 42, got)
}

func TestRunTRepanicsNonSentinel(t *testing.T) {
	require.PanicsWithValue(t, "real bug", func() {
		_, _ = runT(
			&testingAdapter{name: adapterName},
			func(_ ttesting.TestingT) (int, error) {
				panic("real bug")
			},
		)
	})
}

func TestParsePort(t *testing.T) {
	port, err := parsePort("localhost:43121")
	require.NoError(t, err)
	require.Equal(t, 43121, port)

	_, err = parsePort("localhost")
	require.Error(t, err)

	_, err = parsePort("localhost:http")
	require.Error(t, err)
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/kube/...`

Expected: FAIL — `[setup failed]` — the test imports `github.com/gruntwork-io/terratest/modules/testing`, which `go.mod` does not provide yet.

- [ ] **Step 3: Write the implementation.** Write `internal/kube/tunnel.go`:

```go
package kube

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	ttk8s "github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	ttesting "github.com/gruntwork-io/terratest/modules/testing"
)

// Tunnel wraps a terratest tunnel. Close it exactly once (via
// IOE.WithResource): terratest's Close closes a channel and panics on reuse.
type Tunnel struct {
	tt        *ttk8s.Tunnel
	LocalPort int
}

// Close stops the tunnel.
func (t *Tunnel) Close() IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		t.tt.Close()
		return struct{}{}, nil
	})
}

// OpenServiceTunnel port-forwards a free local port to remotePort of a
// Service. terratest resolves the Service to an available pod and blocks
// until the forward is ready.
func OpenServiceTunnel(
	clients Clients,
	namespace, service string,
	remotePort int,
) IOE.IOEither[error, *Tunnel] {
	return IOE.TryCatchError(func() (*Tunnel, error) {
		opts := &ttk8s.KubectlOptions{Namespace: namespace, RestConfig: clients.Rest}
		tt, err := runT(
			&testingAdapter{name: "tunnel " + namespace + "/" + service},
			func(t ttesting.TestingT) (*ttk8s.Tunnel, error) {
				tunnel := ttk8s.NewTunnelWithLogger(
					opts,
					ttk8s.ResourceTypeService,
					service,
					0, // free local port: never collides with a stale forward
					remotePort,
					logger.Discard,
				)
				return tunnel, tunnel.ForwardPortE(t)
			},
		)
		if err != nil {
			return nil, err
		}
		port, err := parsePort(tt.Endpoint())
		if err != nil {
			tt.Close()
			return nil, err
		}
		return &Tunnel{tt: tt, LocalPort: port}, nil
	})
}

// fatalErr is the sentinel panic value raised by Fatal/Fatalf/FailNow.
type fatalErr struct{ msg string }

// testingAdapter satisfies terratest's TestingT outside `go test`.
// Fatal/FailNow panic with fatalErr (runT recovers it); Error/Errorf are
// recorded and fail the call, matching the list-every-failure contract.
type testingAdapter struct {
	name   string
	errors []string
}

func (t *testingAdapter) Fail()             { t.errors = append(t.errors, t.name+": failed") }
func (t *testingAdapter) FailNow()          { panic(fatalErr{t.name + ": failed"}) }
func (t *testingAdapter) Fatal(args ...any) { panic(fatalErr{fmt.Sprint(args...)}) }
func (t *testingAdapter) Fatalf(format string, args ...any) {
	panic(fatalErr{fmt.Sprintf(format, args...)})
}
func (t *testingAdapter) Error(args ...any) { t.errors = append(t.errors, fmt.Sprint(args...)) }
func (t *testingAdapter) Errorf(format string, args ...any) {
	t.errors = append(t.errors, fmt.Sprintf(format, args...))
}
func (t *testingAdapter) Name() string { return t.name }
func (t *testingAdapter) Helper()      {}

// runT runs fn with the adapter. A fatalErr panic becomes the returned error;
// any other panic is a real bug and is re-raised.
func runT[A any](
	adapter *testingAdapter,
	fn func(ttesting.TestingT) (A, error),
) (result A, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		fe, ok := r.(fatalErr)
		if !ok {
			panic(r)
		}
		var zero A
		result, err = zero, errors.New(fe.msg)
	}()
	result, err = fn(adapter)
	if err == nil && len(adapter.errors) > 0 {
		var zero A
		return zero, errors.New(strings.Join(adapter.errors, "; "))
	}
	return result, err
}

// parsePort extracts the port from terratest's "localhost:PORT" endpoint.
func parsePort(endpoint string) (int, error) {
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return 0, fmt.Errorf("tunnel endpoint %q: %w", endpoint, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("tunnel endpoint %q: %w", endpoint, err)
	}
	return n, nil
}
```

- [ ] **Step 4: Add the terratest dependency now that code imports it.**

Run: `go get github.com/gruntwork-io/terratest@v1.0.1 && go mod tidy && grep -E 'terratest v|k8s.io/client-go v' go.mod`

Expected: `github.com/gruntwork-io/terratest v1.0.1` and `k8s.io/client-go v0.37.1` (unchanged).

Do this only after an import exists: `go mod tidy` deletes an unused requirement, and Go then resolves `modules/logger`/`modules/testing` to newer split-out modules that fail to build.

- [ ] **Step 5: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/kube/...`

Expected: `DONE 19 tests` with no failures.

- [ ] **Step 6: Lint.**

Run: `golangci-lint run ./internal/kube/...`

Expected: `0 issues.`

- [ ] **Step 7: Commit.**

```bash
git add internal/kube/tunnel_test.go internal/kube/tunnel.go go.mod go.sum
git commit -m "feat(kube): add terratest service tunnel with a Fatal-safe adapter"
```


---

## Task 7: Create backend-prereqs checks (namespace, Secret, database, port)

**Files:** Create `internal/preflight/backend/prereqs_test.go`, `internal/preflight/backend/probe_test.go`, `internal/preflight/backend/prereqs.go`

Fixes three shell bugs, each pinned by a test: lookup failures name the cause (`lookupFailure` over `kube.Classify`); the database probe sends credentials in the request header (`curl -u` exposed the password in `ps`, `pulumi.justfile:725`); the probe only talks to the tunnel's free port, so a decoy on 18529 cannot pass a missing database. Secret keys are read from `Secret.Data`, so key names containing dots work (`jsonpath={.data.${k}}` broke on them). `probe_test.go` shares the fixture constants declared in `prereqs_test.go`.

- [ ] **Step 1: Write the failing test.** Write `internal/preflight/backend/prereqs_test.go`:

```go
package backend_test

import (
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Fixture names shared by the backend_test files.
const (
	testNamespace  = "prod"
	testSecretName = "order"
	testUserKey    = "user"
	testPassKey    = "password"
)

func TestCheckNamespace(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
	}
	fakeClient := fake.NewSimpleClientset(ns)
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:   clients,
		Namespace: testNamespace,
	}

	result := backend.CheckNamespace(cfg)()
	require.Equal(t, preflight.StatusPass, result.Status)
	require.Contains(t, result.Name, "namespace prod")
}

func TestCheckNamespaceMissing(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:   clients,
		Namespace: "missing",
	}

	result := backend.CheckNamespace(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "not found")
}

func TestCheckSecret(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			testUserKey: []byte("root"),
			testPassKey: []byte("secret"),
		},
	}
	fakeClient := fake.NewSimpleClientset(secret)
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:    clients,
		Namespace:  testNamespace,
		SecretName: testSecretName,
		UserKey:    testUserKey,
		PassKey:    testPassKey,
	}

	result := backend.CheckSecret(cfg)()
	require.Equal(t, preflight.StatusPass, result.Status)
}

func TestCheckSecretMissingKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			testUserKey: []byte("root"),
			// password key missing
		},
	}
	fakeClient := fake.NewSimpleClientset(secret)
	clients := kube.Clients{Kube: fakeClient}

	cfg := backend.PrereqsConfig{
		Clients:    clients,
		Namespace:  testNamespace,
		SecretName: testSecretName,
		UserKey:    testUserKey,
		PassKey:    testPassKey,
	}

	result := backend.CheckSecret(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "missing keys")
}

func TestCheckPort(t *testing.T) {
	tests := []struct {
		port   int
		expect preflight.Status
	}{
		{9250, preflight.StatusPass},
		{1024, preflight.StatusPass},
		{65535, preflight.StatusPass},
		{1023, preflight.StatusFail},
		{65536, preflight.StatusFail},
	}

	for _, tt := range tests {
		cfg := backend.PrereqsConfig{Port: tt.port}
		result := backend.CheckPort(cfg)()
		require.Equal(t, tt.expect, result.Status, "port %d", tt.port)
	}
}
```

- [ ] **Step 2: Write the failing test.** Write `internal/preflight/backend/probe_test.go`:

```go
package backend_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func failingGet(err error) backend.PrereqsConfig {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("get", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
	return backend.PrereqsConfig{
		Clients:    kube.Clients{Kube: cs},
		Namespace:  testNamespace,
		SecretName: testSecretName,
		UserKey:    testUserKey,
		PassKey:    testPassKey,
	}
}

// The shell gate reported RBAC denials and an unreachable API server as
// "MISSING"; each cause must now be named.
func TestLookupFailuresNameTheCause(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, testNamespace, errors.New("rbac"))
	unreachable := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}

	for _, tc := range []struct {
		name  string
		err   error
		check func(backend.PrereqsConfig) IO.IO[preflight.Result]
		want  string
	}{
		{"namespace forbidden", forbidden, backend.CheckNamespace, "access denied"},
		{"namespace unreachable", unreachable, backend.CheckNamespace, "API server unreachable"},
		{"secret forbidden", forbidden, backend.CheckSecret, "access denied"},
		{"secret unreachable", unreachable, backend.CheckSecret, "API server unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.check(failingGet(tc.err))()
			require.Equal(t, preflight.StatusFail, result.Status)
			require.Contains(t, result.Detail, tc.want)
			require.NotEqual(t, "not found", result.Detail)
		})
	}
}

func arangoStub(t *testing.T, status int, gotAuth *string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_db/order/_api/database/current", r.URL.Path)
		*gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestProbeDatabaseStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		ok     bool
		want   string
	}{
		{http.StatusOK, true, "database exists"},
		{http.StatusUnauthorized, false, "credentials rejected (401)"},
		{http.StatusNotFound, false, "database order not found (404)"},
		{http.StatusInternalServerError, false, "unexpected HTTP 500"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var auth string
			got := backend.ProbeDatabase(arangoStub(t, tc.status, &auth), testSecretName, "root", "s3cret")()
			require.Equal(t, tc.ok, E.IsRight(got))
			require.Contains(t, F.Pipe1(got, E.Fold(error.Error, F.Identity[string])), tc.want)
			require.Equal(t, "Basic cm9vdDpzM2NyZXQ=", auth, "credentials go in the header, not argv")
		})
	}
}

// Regression: the shell gate port-forwarded to fixed port 18529 and accepted
// any listener there. A decoy answering 200 on 18529 must not turn a missing
// database into a pass — the probe only talks to the address it is given.
func TestProbeDatabaseIgnoresDecoyOnOldPort(t *testing.T) {
	decoy, err := net.Listen("tcp", "127.0.0.1:18529")
	if err != nil {
		t.Skipf("port 18529 busy, cannot stage decoy: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.Serve(decoy) }()
	t.Cleanup(func() { _ = srv.Close() })

	var auth string
	got := backend.ProbeDatabase(arangoStub(t, http.StatusNotFound, &auth), testSecretName, "root", "s3cret")()
	require.True(t, E.IsLeft(got), "missing database must fail even with a decoy on 18529")
}
```

- [ ] **Step 3: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: FAIL — `no non-test Go files in …/internal/preflight/backend` (`[build failed]`)

- [ ] **Step 4: Write the implementation.** Write `internal/preflight/backend/prereqs.go`:

```go
package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	Arr "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	arangoPort         = 8529
	arangoProbeTimeout = 10 * time.Second
)

// PrereqsConfig holds parameters for backend prerequisite checks.
type PrereqsConfig struct {
	Clients       kube.Clients
	Namespace     string
	SecretName    string
	UserKey       string
	PassKey       string
	ArangoService string
	AppName       string
	Port          int
}

// PrereqsChecks is the backend-prereqs gate: namespace, Secret keys, the
// database through the app's Service, and the port range. Each check runs
// even when another fails.
func PrereqsChecks(cfg PrereqsConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		CheckNamespace(cfg),
		CheckSecret(cfg),
		CheckArangoDB(cfg),
		CheckPort(cfg),
	}
}

// CheckNamespace verifies the namespace exists.
func CheckNamespace(cfg PrereqsConfig) IO.IO[preflight.Result] {
	name := "namespace " + cfg.Namespace
	return F.Pipe1(
		IOE.TryCatchError(func() (struct{}, error) {
			_, err := cfg.Clients.Kube.CoreV1().Namespaces().Get(
				context.Background(), cfg.Namespace, metav1.GetOptions{},
			)
			return struct{}{}, err
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(lookupFailure(name, err, "run namespace-bootstrap for this cluster"))
			},
			func(_ struct{}) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{Name: name, Detail: "exists", Status: preflight.StatusPass})
			},
		),
	)
}

// CheckSecret verifies the Secret exists and holds both credential keys.
func CheckSecret(cfg PrereqsConfig) IO.IO[preflight.Result] {
	name := fmt.Sprintf("secret %s keys %s/%s", cfg.SecretName, cfg.UserKey, cfg.PassKey)
	return F.Pipe1(
		getSecretData(cfg),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(lookupFailure(name, err, "create it with the ArangoDB credentials"))
			},
			func(data map[string][]byte) IO.IO[preflight.Result] {
				return IO.Of(secretKeysResult(name, data, cfg.UserKey, cfg.PassKey))
			},
		),
	)
}

func secretKeysResult(name string, data map[string][]byte, keys ...string) preflight.Result {
	missing := F.Pipe1(keys, Arr.Filter(func(k string) bool { return len(data[k]) == 0 }))
	if len(missing) > 0 {
		return preflight.Result{
			Name:   name,
			Detail: "missing keys: " + strings.Join(missing, ", "),
			Fix:    "key names must match the stack config",
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{Name: name, Detail: "present", Status: preflight.StatusPass}
}

// lookupFailure turns a failed Kubernetes read into a result naming the real
// cause — the shell version reported every failure as missing.
func lookupFailure(name string, err error, fixMissing string) preflight.Result {
	switch kube.Classify(err) {
	case kube.ReasonNotFound:
		return preflight.Result{Name: name, Detail: "not found", Fix: fixMissing, Status: preflight.StatusFail}
	case kube.ReasonForbidden, kube.ReasonUnauthorized:
		return preflight.Result{
			Name:   name,
			Detail: "access denied: " + err.Error(),
			Fix:    "grant the current kube identity read access",
			Status: preflight.StatusFail,
		}
	case kube.ReasonUnreachable:
		return preflight.Result{
			Name:   name,
			Detail: "API server unreachable: " + err.Error(),
			Fix:    "check KUBECONFIG and cluster connectivity",
			Status: preflight.StatusFail,
		}
	default:
		return preflight.Result{Name: name, Detail: err.Error(), Fix: fixMissing, Status: preflight.StatusFail}
	}
}

func getSecretData(cfg PrereqsConfig) IOE.IOEither[error, map[string][]byte] {
	return IOE.TryCatchError(func() (map[string][]byte, error) {
		secret, err := cfg.Clients.Kube.CoreV1().Secrets(cfg.Namespace).Get(
			context.Background(), cfg.SecretName, metav1.GetOptions{},
		)
		if err != nil {
			return nil, err
		}
		return secret.Data, nil
	})
}

// CheckArangoDB probes the application database through the same Service the
// app connects to, over a tunnel on a free local port.
func CheckArangoDB(cfg PrereqsConfig) IO.IO[preflight.Result] {
	name := fmt.Sprintf("database %s via svc/%s", cfg.AppName, cfg.ArangoService)
	return F.Pipe2(
		getSecretData(cfg),
		IOE.Chain(func(data map[string][]byte) IOE.IOEither[error, string] {
			return F.Pipe1(
				func(t *kube.Tunnel) IOE.IOEither[error, string] {
					return ProbeDatabase(
						fmt.Sprintf("http://localhost:%d", t.LocalPort),
						cfg.AppName,
						string(data[cfg.UserKey]),
						string(data[cfg.PassKey]),
					)
				},
				IOE.WithResource[string](
					kube.OpenServiceTunnel(cfg.Clients, cfg.Namespace, cfg.ArangoService, arangoPort),
					func(t *kube.Tunnel) IOE.IOEither[error, struct{}] { return t.Close() },
				),
			)
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: err.Error(),
					Fix:    "check the Service and its pods; apply the create-arangodb-databases stack",
					Status: preflight.StatusFail,
				})
			},
			func(detail string) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{Name: name, Detail: detail, Status: preflight.StatusPass})
			},
		),
	)
}

// ProbeDatabase asks ArangoDB at baseURL whether app exists and the
// credentials work. Credentials travel in the request header, never argv.
func ProbeDatabase(baseURL, app, user, pass string) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		req, err := http.NewRequestWithContext(
			context.Background(), http.MethodGet,
			baseURL+"/_db/"+app+"/_api/database/current", nil,
		)
		if err != nil {
			return "", err
		}
		req.SetBasicAuth(user, pass)
		resp, err := (&http.Client{Timeout: arangoProbeTimeout}).Do(req)
		if err != nil {
			return "", fmt.Errorf("HTTP probe: %w", err)
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			return "credentials valid, database exists", nil
		case http.StatusUnauthorized:
			return "", errors.New("credentials rejected (401) — the Secret does not match the ArangoDB user")
		case http.StatusNotFound:
			return "", fmt.Errorf("database %s not found (404)", app)
		default:
			return "", fmt.Errorf("unexpected HTTP %d", resp.StatusCode)
		}
	})
}

// CheckPort validates the port is in the 1024-65535 range.
func CheckPort(cfg PrereqsConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if cfg.Port < 1024 || cfg.Port > 65535 {
			return preflight.Result{
				Name:   fmt.Sprintf("port %d", cfg.Port),
				Detail: "out of range",
				Fix:    "port must be 1024-65535",
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name:   fmt.Sprintf("port %d", cfg.Port),
			Detail: "in range",
			Status: preflight.StatusPass,
		}
	}
}
```

- [ ] **Step 5: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: `DONE 16 tests` with no failures.

- [ ] **Step 6: Lint.**

Run: `golangci-lint run ./internal/preflight/backend/...`

Expected: `0 issues.`

- [ ] **Step 7: Commit.**

```bash
git add internal/preflight/backend/prereqs_test.go internal/preflight/backend/probe_test.go internal/preflight/backend/prereqs.go
git commit -m "feat(preflight): add backend-prereqs checks"
```


---

## Task 8: Create deployer-access checks (key project, cluster access, KMS)

**Files:** Create `internal/preflight/backend/deployer_test.go`, `internal/preflight/backend/deployer.go`

`DeployerChecks` keeps the shell's three probes and its first guard: the key's `project_id` must match the registry `gcp_project`. kops runs **once** per gate (`CheckDeploymentPermission` owns the export; the temp kubeconfig is removed by `IOE.WithResource`), and the deployer key is bound to the kops subprocess only — no process-wide `GOOGLE_APPLICATION_CREDENTIALS`. A failed export fails that one check; the KMS round-trip still runs. See [Known limitations](#known-limitations) for what the permission probe can and cannot prove.

- [ ] **Step 1: Write the failing test.** Write `internal/preflight/backend/deployer_test.go`:

```go
package backend_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/stretchr/testify/require"
	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// ssarReactor answers every SelfSubjectAccessReview with the given verdict.
func ssarReactor(allowed bool) k8stesting.ReactionFunc {
	return func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectAccessReview{
			Status: authv1.SubjectAccessReviewStatus{Allowed: allowed},
		}, nil
	}
}

// tempKubeconfig is a fake exporter that writes a real temp file, so tests
// can assert the check removes it afterwards.
func tempKubeconfig(t *testing.T, path *string) backend.KubeconfigExporter {
	t.Helper()
	return func(_ backend.DeployerConfig) IOE.IOEither[error, string] {
		return IOE.TryCatchError(func() (string, error) {
			f, err := os.CreateTemp(t.TempDir(), "kubeconfig-*")
			if err != nil {
				return "", err
			}
			*path = f.Name()
			return f.Name(), f.Close()
		})
	}
}

func fakeClients(allowed bool) backend.ClientsFactory {
	return func(_ string) IOE.IOEither[error, kube.Clients] {
		cs := fake.NewSimpleClientset()
		cs.PrependReactor("create", "selfsubjectaccessreviews", ssarReactor(allowed))
		return IOE.Of[error](kube.Clients{Kube: cs})
	}
}

func deployerCfg() backend.DeployerConfig {
	return backend.DeployerConfig{Entry: registry.Entry{Namespace: testNamespace, GCPProject: "dcr-kube1"}}
}

func TestCheckDeploymentPermissionAllowed(t *testing.T) {
	var kubeconfig string
	result := backend.CheckDeploymentPermission(deployerCfg(), tempKubeconfig(t, &kubeconfig), fakeClients(true))()
	require.Equal(t, preflight.StatusPass, result.Status)
	require.NoFileExists(t, kubeconfig, "exported kubeconfig must be removed")
}

func TestCheckDeploymentPermissionDenied(t *testing.T) {
	var kubeconfig string
	result := backend.CheckDeploymentPermission(deployerCfg(), tempKubeconfig(t, &kubeconfig), fakeClients(false))()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Equal(t, "denied", result.Detail)
	require.NoFileExists(t, kubeconfig)
}

func TestCheckDeploymentPermissionExportFails(t *testing.T) {
	failing := func(_ backend.DeployerConfig) IOE.IOEither[error, string] {
		return IOE.Left[string](errors.New("kops export kubeconfig from gs://kops-state: exit status 1"))
	}
	result := backend.CheckDeploymentPermission(deployerCfg(), failing, fakeClients(true))()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "kops export kubeconfig")
}

func writeKey(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployer.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestCheckKeyProjectMatches(t *testing.T) {
	cfg := deployerCfg()
	cfg.SAKeyPath = writeKey(t, `{"project_id": "dcr-kube1", "type": "service_account"}`)
	require.Equal(t, preflight.StatusPass, backend.CheckKeyProject(cfg)().Status)
}

func TestCheckKeyProjectMismatchIsNamed(t *testing.T) {
	cfg := deployerCfg()
	cfg.SAKeyPath = writeKey(t, `{"project_id": "dcr-experiments", "type": "service_account"}`)
	result := backend.CheckKeyProject(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "does not match registry gcp_project 'dcr-kube1'")
}

func TestCheckKeyProjectNotAKey(t *testing.T) {
	cfg := deployerCfg()
	cfg.SAKeyPath = writeKey(t, `{"hello": "world"}`)
	result := backend.CheckKeyProject(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "is not a GCP service account key JSON")
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: FAIL — `undefined: backend.KubeconfigExporter`, `undefined: backend.DeployerConfig`, `undefined: backend.ClientsFactory`.

- [ ] **Step 3: Write the implementation.** Write `internal/preflight/backend/deployer.go`:

```go
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"google.golang.org/api/option"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// deployerKeyCheck names the key-project check.
const deployerKeyCheck = "deployer key"

// DeployerConfig holds parameters for deployer access checks.
type DeployerConfig struct {
	Entry     registry.Entry
	SAKeyPath string
}

// KubeconfigExporter writes a kubeconfig for the registry cluster and
// returns its path. KopsExport is the production implementation.
type KubeconfigExporter func(DeployerConfig) IOE.IOEither[error, string]

// ClientsFactory builds Kubernetes clients from a kubeconfig path.
type ClientsFactory func(kubeconfig string) IOE.IOEither[error, kube.Clients]

// DeployerChecks is the deployer-access gate: key project, cluster access
// (one kops export, then the deployments permission), and the KMS round-trip.
// Each check runs even when another fails.
func DeployerChecks(cfg DeployerConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		CheckKeyProject(cfg),
		CheckDeploymentPermission(cfg, KopsExport, KubeClients),
		CheckKMSRoundTrip(cfg),
	}
}

// CheckKeyProject refuses a key minted in another project: a key from one
// project cannot deploy to another cluster's project.
func CheckKeyProject(cfg DeployerConfig) IO.IO[preflight.Result] {
	return F.Pipe1(
		IOE.TryCatchError(func() (string, error) {
			raw, err := os.ReadFile(cfg.SAKeyPath)
			if err != nil {
				return "", err
			}
			var key struct {
				ProjectID string `json:"project_id"`
			}
			if err := json.Unmarshal(raw, &key); err != nil || key.ProjectID == "" {
				return "", fmt.Errorf("%s is not a GCP service account key JSON", cfg.SAKeyPath)
			}
			return key.ProjectID, nil
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   deployerKeyCheck,
					Detail: err.Error(),
					Fix:    "pass the deployer key JSON with --sa-key",
					Status: preflight.StatusFail,
				})
			},
			func(project string) IO.IO[preflight.Result] {
				return IO.Of(keyProjectResult(project, cfg.Entry.GCPProject))
			},
		),
	)
}

func keyProjectResult(keyProject, registryProject string) preflight.Result {
	if keyProject != registryProject {
		return preflight.Result{
			Name: deployerKeyCheck,
			Detail: fmt.Sprintf(
				"key project '%s' does not match registry gcp_project '%s'",
				keyProject, registryProject,
			),
			Fix:    fmt.Sprintf("mint or reuse a key from the '%s' project only", registryProject),
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{
		Name:   deployerKeyCheck,
		Detail: "project " + keyProject,
		Status: preflight.StatusPass,
	}
}

// CheckDeploymentPermission exports a kubeconfig once, then asks the API
// server whether that identity may update deployments in the registry
// namespace. The kubeconfig file is removed when the check ends.
//
// Note: KopsExport uses `kops export kubeconfig --admin` (same as the shell
// recipe it replaces), so this proves the kops state is readable with the
// key; the admin identity itself is always allowed.
func CheckDeploymentPermission(
	cfg DeployerConfig,
	export KubeconfigExporter,
	newClients ClientsFactory,
) IO.IO[preflight.Result] {
	name := "update deployments in " + cfg.Entry.Namespace
	return F.Pipe2(
		F.Flow2(newClients, IOE.Chain(canUpdateDeployments(cfg.Entry.Namespace))),
		IOE.WithResource[bool](export(cfg), removeFile),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: err.Error(),
					Fix:    "grant the deployer SA the roles in gcs-files/roles-permissions/deployer-roles.txt",
					Status: preflight.StatusFail,
				})
			},
			func(allowed bool) IO.IO[preflight.Result] {
				return IO.Of(permissionResult(name, allowed))
			},
		),
	)
}

func permissionResult(name string, allowed bool) preflight.Result {
	if !allowed {
		return preflight.Result{
			Name:   name,
			Detail: "denied",
			Fix:    "grant the deployer SA the roles in gcs-files/roles-permissions/deployer-roles.txt",
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{Name: name, Detail: "allowed", Status: preflight.StatusPass}
}

func canUpdateDeployments(namespace string) func(kube.Clients) IOE.IOEither[error, bool] {
	return func(clients kube.Clients) IOE.IOEither[error, bool] {
		return IOE.TryCatchError(func() (bool, error) {
			resp, err := clients.Kube.AuthorizationV1().SelfSubjectAccessReviews().Create(
				context.Background(),
				&authv1.SelfSubjectAccessReview{
					Spec: authv1.SelfSubjectAccessReviewSpec{
						ResourceAttributes: &authv1.ResourceAttributes{
							Namespace: namespace,
							Verb:      "update",
							Group:     "apps",
							Resource:  "deployments",
						},
					},
				},
				metav1.CreateOptions{},
			)
			if err != nil {
				return false, fmt.Errorf("access review: %w", err)
			}
			return resp.Status.Allowed, nil
		})
	}
}

// KopsExport runs `kops export kubeconfig --admin` against the registry
// state bucket with the deployer key, into a private temp file.
func KopsExport(cfg DeployerConfig) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		file, err := os.CreateTemp("", "kubeconfig-*")
		if err != nil {
			return "", err
		}
		path := file.Name()
		if err := file.Close(); err != nil {
			return "", errors.Join(err, os.Remove(path))
		}
		//nolint:gosec // G204: kops args come from the cluster registry, not user input
		cmd := exec.Command(
			"kops", "export", "kubeconfig", "--admin",
			"--name", cfg.Entry.Cluster+"-k8s.local",
			"--state", cfg.Entry.KopsState,
			"--kubeconfig", path,
		)
		// Bind the deployer identity to this subprocess only.
		cmd.Env = append(os.Environ(), "GOOGLE_APPLICATION_CREDENTIALS="+cfg.SAKeyPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", errors.Join(
				fmt.Errorf("kops export kubeconfig from %s: %w: %s", cfg.Entry.KopsState, err, out),
				os.Remove(path),
			)
		}
		return path, nil
	})
}

// KubeClients is the production ClientsFactory.
func KubeClients(kubeconfig string) IOE.IOEither[error, kube.Clients] {
	return IOE.FromEither(kube.NewClients(kubeconfig, ""))
}

func removeFile(path string) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, os.Remove(path)
	})
}

// CheckKMSRoundTrip verifies the deployer SA can encrypt/decrypt with the KMS key.
func CheckKMSRoundTrip(cfg DeployerConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		result := F.Pipe1(
			registry.ParseKMSKey(cfg.Entry.KMSSecretsProvider),
			E.Chain(func(key registry.KMSKey) E.Either[error, preflight.Result] {
				return kmsRoundTrip(key, cfg.SAKeyPath)()
			}),
		)

		return F.Pipe1(
			result,
			E.GetOrElse(func(err error) preflight.Result {
				return preflight.Result{
					Name:   "KMS encrypt/decrypt",
					Detail: err.Error(),
					Fix:    "grant deployer SA roles/cloudkms.cryptoKeyEncrypterDecrypter on the registry key",
					Status: preflight.StatusFail,
				}
			}),
		)
	}
}

func kmsRoundTrip(key registry.KMSKey, saKeyPath string) IOE.IOEither[error, preflight.Result] {
	return F.Pipe1(
		performRoundTrip(key),
		IOE.WithResource[preflight.Result](
			newKMSClient(saKeyPath),
			closeKMSClient,
		),
	)
}

func newKMSClient(saKeyPath string) IOE.IOEither[error, *kms.KeyManagementClient] {
	return IOE.TryCatchError(func() (*kms.KeyManagementClient, error) {
		return kms.NewKeyManagementClient(
			context.Background(),
			option.WithAuthCredentialsFile(option.ServiceAccount, saKeyPath),
		)
	})
}

func closeKMSClient(c *kms.KeyManagementClient) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, c.Close()
	})
}

func performRoundTrip(key registry.KMSKey) func(*kms.KeyManagementClient) IOE.IOEither[error, preflight.Result] {
	return func(client *kms.KeyManagementClient) IOE.IOEither[error, preflight.Result] {
		return IOE.TryCatchError(func() (preflight.Result, error) {
			plaintext := []byte(fmt.Sprintf("probe-%d", time.Now().Unix()))
			keyName := fmt.Sprintf(
				"projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
				key.Project, key.Location, key.KeyRing, key.Key,
			)

			encResp, err := client.Encrypt(context.Background(), &kmspb.EncryptRequest{
				Name:      keyName,
				Plaintext: plaintext,
			})
			if err != nil {
				return preflight.Result{}, fmt.Errorf("KMS encrypt failed: %w", err)
			}

			decResp, err := client.Decrypt(context.Background(), &kmspb.DecryptRequest{
				Name:       keyName,
				Ciphertext: encResp.Ciphertext,
			})
			if err != nil {
				return preflight.Result{}, fmt.Errorf("KMS decrypt failed: %w", err)
			}

			if string(decResp.Plaintext) != string(plaintext) {
				return preflight.Result{}, fmt.Errorf("KMS round-trip mismatch")
			}

			return preflight.Result{
				Name:   "KMS encrypt/decrypt",
				Detail: fmt.Sprintf("key %s", key.Key),
				Status: preflight.StatusPass,
			}, nil
		})
	}
}
```

- [ ] **Step 4: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: `DONE 22 tests` with no failures.

- [ ] **Step 5: Lint.**

Run: `golangci-lint run ./internal/preflight/backend/...`

Expected: `0 issues.`

- [ ] **Step 6: Commit.**

```bash
git add internal/preflight/backend/deployer_test.go internal/preflight/backend/deployer.go
git commit -m "feat(preflight): add deployer-access checks"
```


---

## Task 9: Create the IAM permission check (deploy-credentials)

**Files:** Create `internal/preflight/backend/credentials_test.go`, `internal/preflight/backend/credentials.go`

Replaces `gcloud projects get-iam-policy | grep -qx <role>` (`just_modules/ci.justfile:63`), which missed roles granted through groups or inherited from the folder/org and stopped at the first miss. Roles from `deployer-roles.txt` expand to permissions (`roles.get`), which are tested **as the deployer key** with `projects.testIamPermissions` in batches of 100; every missing permission is listed. Both GCP calls are injectable (`RoleExpander`, `PermissionTester`), so batching and reporting are unit-tested.

- [ ] **Step 1: Write the failing test.** Write `internal/preflight/backend/credentials_test.go`:

```go
package backend_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
)

func rolesFile(t *testing.T, content string) backend.CredentialsConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployer-roles.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return backend.CredentialsConfig{RolesFilePath: path}
}

// expandTo returns a RoleExpander granting n numbered permissions per role.
func expandTo(n int) backend.RoleExpander {
	return func(role string) IOE.IOEither[error, []string] {
		perms := make([]string, 0, n)
		for i := range n {
			perms = append(perms, fmt.Sprintf("%s.perm%03d", role, i))
		}
		return IOE.Of[error](perms)
	}
}

// holdAllBut is a PermissionTester that records batch sizes and grants
// everything except the listed permissions.
func holdAllBut(batchSizes *[]int, denied ...string) backend.PermissionTester {
	return func(batch []string) IOE.IOEither[error, []string] {
		*batchSizes = append(*batchSizes, len(batch))
		granted := []string{}
		for _, p := range batch {
			if !slices.Contains(denied, p) {
				granted = append(granted, p)
			}
		}
		return IOE.Of[error](granted)
	}
}

func TestParseRolesSkipsBlanksAndComments(t *testing.T) {
	got := backend.ParseRoles("roles/a\n\n# comment\n  roles/b  \n")
	require.Equal(t, []string{"roles/a", "roles/b"}, got)
}

func TestIAMListsEveryMissingPermissionAcrossBatches(t *testing.T) {
	var sizes []int
	cfg := rolesFile(t, "roles/x\nroles/y\n")
	// 2 roles × 120 perms = 240 → batches of 100, 100, 40.
	test := holdAllBut(&sizes, "roles/x.perm005", "roles/y.perm119")

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(120), test)()

	require.Equal(t, preflight.StatusFail, result.Status)
	require.Equal(t, []int{100, 100, 40}, sizes)
	require.Contains(t, result.Detail, "missing 2 of 240")
	require.Contains(t, result.Detail, "roles/x.perm005")
	require.Contains(t, result.Detail, "roles/y.perm119")
}

func TestIAMDeduplicatesOverlappingRoles(t *testing.T) {
	var sizes []int
	cfg := rolesFile(t, "roles/x\nroles/x\n")

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(3), holdAllBut(&sizes))()

	require.Equal(t, preflight.StatusPass, result.Status)
	require.Equal(t, "3 permissions held", result.Detail)
}

func TestIAMReportsAPIError(t *testing.T) {
	cfg := rolesFile(t, "roles/x\n")
	failing := func(_ []string) IOE.IOEither[error, []string] {
		return IOE.Left[[]string](errors.New("testIamPermissions on dcr-kube1: 403"))
	}

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(1), failing)()

	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "403")
}

func TestIAMMissingRolesFile(t *testing.T) {
	var sizes []int
	cfg := backend.CredentialsConfig{RolesFilePath: filepath.Join(t.TempDir(), "absent.txt")}

	result := backend.CheckIAMPermissionsWith(cfg, expandTo(1), holdAllBut(&sizes))()

	require.Equal(t, preflight.StatusFail, result.Status)
	require.Empty(t, sizes, "no API call without a roles file")
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: FAIL — `undefined: backend.CredentialsConfig`, `undefined: backend.RoleExpander`.

- [ ] **Step 3: Write the implementation.** Write `internal/preflight/backend/credentials.go`:

```go
package backend

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	Arr "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

const (
	iamPermissionsCheckName = "IAM permissions"
	// testIamPermissions accepts at most 100 permissions per call.
	testIamBatchSize = 100
	fixDeployerRoles = "grant the deployer SA the roles in gcs-files/roles-permissions/deployer-roles.txt"
)

// CredentialsConfig holds parameters for deploy credentials checks.
type CredentialsConfig struct {
	Entry         registry.Entry
	SAKeyPath     string
	RolesFilePath string // gcs-files/roles-permissions/deployer-roles.txt
}

// RoleExpander returns the permissions a role grants.
type RoleExpander func(role string) IOE.IOEither[error, []string]

// PermissionTester returns the subset of a batch the caller holds.
type PermissionTester func(batch []string) IOE.IOEither[error, []string]

// CheckIAMPermissions verifies the deployer SA holds every permission its
// roles grant. testIamPermissions runs AS the deployer key, so grants via
// groups or inherited from the folder/org count; every missing permission
// is listed (the shell version grepped the project policy and stopped at
// the first miss).
func CheckIAMPermissions(cfg CredentialsConfig) IO.IO[preflight.Result] {
	return CheckIAMPermissionsWith(cfg, iamRoleExpander(cfg.SAKeyPath), crmPermissionTester(cfg))
}

// CheckIAMPermissionsWith is CheckIAMPermissions with injectable GCP calls.
func CheckIAMPermissionsWith(
	cfg CredentialsConfig,
	expand RoleExpander,
	test PermissionTester,
) IO.IO[preflight.Result] {
	return F.Pipe4(
		readRolesFile(cfg.RolesFilePath),
		IOE.Chain(IOE.TraverseArray(expand)),
		IOE.Map[error](F.Flow2(Arr.Flatten[string], uniqueSorted)),
		IOE.Chain(missingPermissions(test)),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   iamPermissionsCheckName,
					Detail: err.Error(),
					Fix:    fixDeployerRoles,
					Status: preflight.StatusFail,
				})
			},
			func(r permissionReport) IO.IO[preflight.Result] {
				return IO.Of(r.result())
			},
		),
	)
}

// permissionReport is the outcome of testing every wanted permission.
type permissionReport struct {
	wanted  int
	missing []string
}

func (r permissionReport) result() preflight.Result {
	if len(r.missing) > 0 {
		return preflight.Result{
			Name:   iamPermissionsCheckName,
			Detail: fmt.Sprintf("missing %d of %d: %s", len(r.missing), r.wanted, strings.Join(r.missing, ", ")),
			Fix:    fixDeployerRoles,
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{
		Name:   iamPermissionsCheckName,
		Detail: fmt.Sprintf("%d permissions held", r.wanted),
		Status: preflight.StatusPass,
	}
}

// ParseRoles returns the non-blank, non-comment lines of a roles file.
func ParseRoles(content string) []string {
	return F.Pipe2(
		strings.Split(content, "\n"),
		Arr.Map(strings.TrimSpace),
		Arr.Filter(func(line string) bool { return line != "" && !strings.HasPrefix(line, "#") }),
	)
}

func readRolesFile(path string) IOE.IOEither[error, []string] {
	return F.Pipe1(
		IOE.TryCatchError(func() ([]byte, error) { return os.ReadFile(path) }),
		IOE.Map[error](func(b []byte) []string { return ParseRoles(string(b)) }),
	)
}

func uniqueSorted(perms []string) []string {
	out := slices.Clone(perms)
	slices.Sort(out)
	return slices.Compact(out)
}

// missingPermissions tests perms in batches and reports every one not held.
func missingPermissions(test PermissionTester) func([]string) IOE.IOEither[error, permissionReport] {
	return func(perms []string) IOE.IOEither[error, permissionReport] {
		return F.Pipe2(
			slices.Collect(slices.Chunk(perms, testIamBatchSize)),
			IOE.TraverseArray(test),
			IOE.Map[error](func(granted [][]string) permissionReport {
				held := Arr.Flatten(granted)
				return permissionReport{
					wanted: len(perms),
					missing: F.Pipe1(perms, Arr.Filter(func(p string) bool {
						return !slices.Contains(held, p)
					})),
				}
			}),
		)
	}
}

func iamRoleExpander(saKeyPath string) RoleExpander {
	return func(role string) IOE.IOEither[error, []string] {
		return IOE.TryCatchError(func() ([]string, error) {
			svc, err := iam.NewService(
				context.Background(),
				option.WithAuthCredentialsFile(option.ServiceAccount, saKeyPath),
			)
			if err != nil {
				return nil, fmt.Errorf("IAM client: %w", err)
			}
			resp, err := svc.Roles.Get(role).Do()
			if err != nil {
				return nil, fmt.Errorf("role %s: %w", role, err)
			}
			return resp.IncludedPermissions, nil
		})
	}
}

func crmPermissionTester(cfg CredentialsConfig) PermissionTester {
	return func(batch []string) IOE.IOEither[error, []string] {
		return IOE.TryCatchError(func() ([]string, error) {
			svc, err := cloudresourcemanager.NewService(
				context.Background(),
				option.WithAuthCredentialsFile(option.ServiceAccount, cfg.SAKeyPath),
			)
			if err != nil {
				return nil, fmt.Errorf("resource manager client: %w", err)
			}
			resp, err := svc.Projects.TestIamPermissions(
				cfg.Entry.GCPProject,
				&cloudresourcemanager.TestIamPermissionsRequest{Permissions: batch},
			).Do()
			if err != nil {
				return nil, fmt.Errorf("testIamPermissions on %s: %w", cfg.Entry.GCPProject, err)
			}
			return resp.Permissions, nil
		})
	}
}
```

- [ ] **Step 4: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: `DONE 27 tests` with no failures.

- [ ] **Step 5: Lint.**

Run: `golangci-lint run ./internal/preflight/backend/...`

Expected: `0 issues.`

- [ ] **Step 6: Commit.**

```bash
git add internal/preflight/backend/credentials_test.go internal/preflight/backend/credentials.go
git commit -m "feat(preflight): add IAM permission check for the deployer key"
```


---

## Task 10: Create pulumi-backend checks

**Files:** Create `internal/preflight/backend/pulumi_test.go`, `internal/preflight/backend/pulumi.go`

Ports all five checks of `scripts/pulumi/check-backend.sh`: env, manager key file, state bucket + versioning, gcpkms key reachable, active login. Inputs come from `PulumiBackendConfig` (the CLI reads `PULUMI_*` once), not ambient env. A check whose input is unset reports `INFO skipped` — the env check already fails for it. The login must match exactly (a substring match passed a login to `…-old`). `pulumi whoami --json` is parsed with `encoding/json`.

- [ ] **Step 1: Write the failing test.** Write `internal/preflight/backend/pulumi_test.go`:

```go
package backend_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
)

const testBackendURL = "gs://pulumi-state-dcr-kube1"

func fullPulumiCfg() backend.PulumiBackendConfig {
	return backend.PulumiBackendConfig{ //nolint:gosec // G101: fixture values, not credentials
		CredsPath:       "/creds/pulumi-manager.json",
		SecretsProvider: "gcpkms://projects/dcr-kube1/locations/us-east1/keyRings/r/cryptoKeys/k",
		BackendURL:      testBackendURL,
		Stack:           "dcr-kube1",
	}
}

func loginAs(url string) backend.LoginReader {
	return func() IOE.IOEither[error, string] { return IOE.Of[error](url) }
}

func TestCheckEnvVarsAllSet(t *testing.T) {
	require.Equal(t, preflight.StatusPass, backend.CheckEnvVars(fullPulumiCfg())().Status)
}

func TestCheckEnvVarsNamesEveryUnsetVar(t *testing.T) {
	cfg := fullPulumiCfg()
	cfg.Stack, cfg.BackendURL = "", ""
	result := backend.CheckEnvVars(cfg)()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Equal(t, "unset: PULUMI_BACKEND_URL, PULUMI_STACK", result.Detail)
}

func TestCheckManagerKey(t *testing.T) {
	cfg := fullPulumiCfg()
	cfg.CredsPath = filepath.Join(t.TempDir(), "pulumi-manager.json")
	require.Equal(t, preflight.StatusFail, backend.CheckManagerKey(cfg)().Status)

	require.NoError(t, os.WriteFile(cfg.CredsPath, []byte("{}"), 0o600))
	require.Equal(t, preflight.StatusPass, backend.CheckManagerKey(cfg)().Status)
}

func TestDependentChecksSkipWhenInputUnset(t *testing.T) {
	cfg := backend.PulumiBackendConfig{}
	for _, check := range backend.PulumiChecks(cfg)[1:] {
		require.Equal(t, preflight.StatusInfo, check().Status)
	}
}

func TestSecretsProviderNonKMSIsInfo(t *testing.T) {
	cfg := fullPulumiCfg()
	cfg.SecretsProvider = "passphrase"
	require.Equal(t, preflight.StatusInfo, backend.CheckSecretsProviderKey(cfg)().Status)
}

func TestCheckPulumiLogin(t *testing.T) {
	cfg := fullPulumiCfg()
	require.Equal(t, preflight.StatusPass, backend.CheckPulumiLogin(cfg, loginAs(testBackendURL+"/"))().Status)

	result := backend.CheckPulumiLogin(cfg, loginAs("gs://pulumi-state-dcr-experiments"))()
	require.Equal(t, preflight.StatusFail, result.Status)
	require.Contains(t, result.Detail, "expected "+testBackendURL)

	// A substring match used to pass a login to a different bucket.
	result = backend.CheckPulumiLogin(cfg, loginAs(testBackendURL+"-old"))()
	require.Equal(t, preflight.StatusFail, result.Status)

	failing := func() IOE.IOEither[error, string] { return IOE.Left[string](errors.New("not logged in")) }
	require.Equal(t, preflight.StatusFail, backend.CheckPulumiLogin(cfg, failing)().Status)
}

func TestParseWhoami(t *testing.T) {
	got := backend.ParseWhoami([]byte(`{"user":"me","url":"gs://pulumi-state-dcr-kube1"}`))
	require.Equal(t, E.Right[error]("gs://pulumi-state-dcr-kube1"), got)
	require.True(t, E.IsLeft(backend.ParseWhoami([]byte(`{"user":"me"}`))))
	require.True(t, E.IsLeft(backend.ParseWhoami([]byte(`not json`))))
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: FAIL — `undefined: backend.PulumiBackendConfig`, `undefined: backend.LoginReader`.

- [ ] **Step 3: Write the implementation.** Write `internal/preflight/backend/pulumi.go`:

```go
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"cloud.google.com/go/storage"
	Arr "github.com/IBM/fp-go/v2/array"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	P "github.com/IBM/fp-go/v2/pair"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"google.golang.org/api/option"
)

const (
	pulumiLoginCheckName = "Pulumi login"
	// fixPulumiGCSSetup is the remedy for every state-backend failure.
	fixPulumiGCSSetup = "run 'just gcp-pulumi pulumi-gcs-setup'"
	fixClusterEnv     = "enter the cluster shell with 'just cluster-env'"
)

// PulumiBackendConfig is the cluster shell's Pulumi wiring, read once from
// the PULUMI_* variables by the CLI.
type PulumiBackendConfig struct {
	CredsPath       string // PULUMI_GCP_CREDENTIALS
	SecretsProvider string // PULUMI_SECRET_PROVIDER
	BackendURL      string // PULUMI_BACKEND_URL
	Stack           string // PULUMI_STACK
}

// LoginReader returns the backend URL of the active `pulumi login`.
type LoginReader func() IOE.IOEither[error, string]

// PulumiChecks is the pulumi-backend gate: env, manager key, state bucket,
// secrets-provider key, active login. Checks whose input is unset report
// INFO "skipped" — the env check already fails for it.
func PulumiChecks(cfg PulumiBackendConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		CheckEnvVars(cfg),
		CheckManagerKey(cfg),
		CheckBucketVersioning(cfg),
		CheckSecretsProviderKey(cfg),
		CheckPulumiLogin(cfg, PulumiWhoami),
	}
}

// CheckEnvVars verifies every PULUMI_* value is set.
func CheckEnvVars(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	missing := F.Pipe2(
		[]P.Pair[string, string]{
			P.MakePair("PULUMI_GCP_CREDENTIALS", cfg.CredsPath),
			P.MakePair("PULUMI_SECRET_PROVIDER", cfg.SecretsProvider),
			P.MakePair("PULUMI_BACKEND_URL", cfg.BackendURL),
			P.MakePair("PULUMI_STACK", cfg.Stack),
		},
		Arr.Filter(func(v P.Pair[string, string]) bool { return P.Tail(v) == "" }),
		Arr.Map(P.Head[string, string]),
	)
	if len(missing) > 0 {
		return IO.Of(preflight.Result{
			Name:   "Pulumi env vars",
			Detail: "unset: " + strings.Join(missing, ", "),
			Fix:    fixClusterEnv,
			Status: preflight.StatusFail,
		})
	}
	return IO.Of(preflight.Result{Name: "Pulumi env vars", Detail: "all set", Status: preflight.StatusPass})
}

// CheckManagerKey verifies the PULUMI_GCP_CREDENTIALS key file exists.
func CheckManagerKey(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if cfg.CredsPath == "" {
			return skipped("manager key", "PULUMI_GCP_CREDENTIALS")
		}
		if _, err := os.Stat(cfg.CredsPath); err != nil {
			return preflight.Result{
				Name:   "manager key",
				Detail: "missing at " + cfg.CredsPath,
				Fix:    "run 'just gcp-sa create-sa --sa-name pulumi-manager'",
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: "manager key", Detail: cfg.CredsPath, Status: preflight.StatusPass}
	}
}

// CheckBucketVersioning verifies the state bucket exists with versioning on.
func CheckBucketVersioning(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	bucket := strings.TrimPrefix(cfg.BackendURL, "gs://")
	name := "state bucket gs://" + bucket
	if cfg.BackendURL == "" || cfg.CredsPath == "" {
		return IO.Of(skipped(name, "PULUMI_BACKEND_URL or PULUMI_GCP_CREDENTIALS"))
	}
	return F.Pipe2(
		bucketVersioned(bucket),
		IOE.WithResource[bool](newStorageClient(cfg.CredsPath), closeStorageClient),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: "not found or unreachable: " + err.Error(),
					Fix:    fixPulumiGCSSetup,
					Status: preflight.StatusFail,
				})
			},
			func(versioned bool) IO.IO[preflight.Result] {
				if !versioned {
					return IO.Of(preflight.Result{
						Name: name, Detail: "versioning NOT enabled", Fix: fixPulumiGCSSetup, Status: preflight.StatusFail,
					})
				}
				return IO.Of(preflight.Result{Name: name, Detail: "versioning enabled", Status: preflight.StatusPass})
			},
		),
	)
}

func bucketVersioned(bucket string) func(*storage.Client) IOE.IOEither[error, bool] {
	return func(client *storage.Client) IOE.IOEither[error, bool] {
		return IOE.TryCatchError(func() (bool, error) {
			attrs, err := client.Bucket(bucket).Attrs(context.Background())
			if err != nil {
				return false, err
			}
			return attrs.VersioningEnabled, nil
		})
	}
}

func newStorageClient(credsPath string) IOE.IOEither[error, *storage.Client] {
	return IOE.TryCatchError(func() (*storage.Client, error) {
		return storage.NewClient(
			context.Background(),
			option.WithAuthCredentialsFile(option.ServiceAccount, credsPath),
		)
	})
}

func closeStorageClient(c *storage.Client) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) { return struct{}{}, c.Close() })
}

// CheckSecretsProviderKey verifies the gcpkms secrets-provider key is
// readable with the manager key. Other providers are reported as INFO.
func CheckSecretsProviderKey(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	const name = "secrets provider key"
	switch {
	case cfg.SecretsProvider == "" || cfg.CredsPath == "":
		return IO.Of(skipped(name, "PULUMI_SECRET_PROVIDER or PULUMI_GCP_CREDENTIALS"))
	case !strings.HasPrefix(cfg.SecretsProvider, "gcpkms://"):
		return IO.Of(preflight.Result{Name: name, Detail: "not gcpkms, check skipped", Status: preflight.StatusInfo})
	}
	return F.Pipe2(
		IOE.FromEither(registry.ParseKMSKey(cfg.SecretsProvider)),
		IOE.Chain(func(key registry.KMSKey) IOE.IOEither[error, string] {
			return F.Pipe1(
				describeKey(key),
				IOE.WithResource[string](newKMSClient(cfg.CredsPath), closeKMSClient),
			)
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: err.Error(),
					Fix:    "run 'just gcp-kms create-keyring-and-key'",
					Status: preflight.StatusFail,
				})
			},
			func(keyName string) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{Name: name, Detail: keyName + " reachable", Status: preflight.StatusPass})
			},
		),
	)
}

func describeKey(key registry.KMSKey) func(*kms.KeyManagementClient) IOE.IOEither[error, string] {
	return func(client *kms.KeyManagementClient) IOE.IOEither[error, string] {
		return IOE.TryCatchError(func() (string, error) {
			resp, err := client.GetCryptoKey(context.Background(), &kmspb.GetCryptoKeyRequest{
				Name: fmt.Sprintf(
					"projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
					key.Project, key.Location, key.KeyRing, key.Key,
				),
			})
			if err != nil {
				return "", fmt.Errorf("KMS key %s not reachable: %w", key.Key, err)
			}
			return resp.GetName(), nil
		})
	}
}

// CheckPulumiLogin verifies the active (global) pulumi login is this
// cluster's backend.
func CheckPulumiLogin(cfg PulumiBackendConfig, read LoginReader) IO.IO[preflight.Result] {
	if cfg.BackendURL == "" {
		return IO.Of(skipped(pulumiLoginCheckName, "PULUMI_BACKEND_URL"))
	}
	return F.Pipe1(
		read(),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name: pulumiLoginCheckName, Detail: err.Error(), Fix: fixPulumiGCSSetup, Status: preflight.StatusFail,
				})
			},
			func(active string) IO.IO[preflight.Result] {
				return IO.Of(loginResult(active, cfg.BackendURL))
			},
		),
	)
}

func loginResult(active, expected string) preflight.Result {
	if strings.TrimSuffix(active, "/") != strings.TrimSuffix(expected, "/") {
		return preflight.Result{
			Name:   pulumiLoginCheckName,
			Detail: fmt.Sprintf("active login is '%s', expected %s", active, expected),
			Fix:    "pulumi login is global — " + fixPulumiGCSSetup,
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{Name: pulumiLoginCheckName, Detail: "matches " + expected, Status: preflight.StatusPass}
}

// PulumiWhoami is the production LoginReader.
func PulumiWhoami() IOE.IOEither[error, string] {
	return F.Pipe1(
		IOE.TryCatchError(func() ([]byte, error) {
			out, err := exec.Command("pulumi", "whoami", "--json").Output()
			if err != nil {
				return nil, fmt.Errorf("cannot determine the active Pulumi backend (not logged in?): %w", err)
			}
			return out, nil
		}),
		IOE.ChainEitherK(ParseWhoami),
	)
}

// ParseWhoami extracts the backend URL from `pulumi whoami --json`.
func ParseWhoami(out []byte) E.Either[error, string] {
	var who struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &who); err != nil {
		return E.Left[string](fmt.Errorf("parse pulumi whoami: %w", err))
	}
	if who.URL == "" {
		return E.Left[string](errors.New("pulumi whoami reported no backend url"))
	}
	return E.Right[error](who.URL)
}

func skipped(name, unset string) preflight.Result {
	return preflight.Result{Name: name, Detail: "skipped: " + unset + " unset", Status: preflight.StatusInfo}
}
```

- [ ] **Step 4: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: `DONE 34 tests` with no failures.

- [ ] **Step 5: Lint.**

Run: `golangci-lint run ./internal/preflight/backend/...`

Expected: `0 issues.`

- [ ] **Step 6: Commit.**

```bash
git add internal/preflight/backend/pulumi_test.go internal/preflight/backend/pulumi.go
git commit -m "feat(preflight): add pulumi-backend checks"
```


---

## Task 11: Add the `cluster-ops preflight` command

**Files:** Create `cmd/cluster-ops/preflight_test.go`, `cmd/cluster-ops/preflight.go`, modify `cmd/cluster-ops/main.go`

Four subcommands over the check lists, one shared `reportGate` (prints every result, exit 1 on failure, `Next: <recipe>` hint on success — recipe output is documentation). `--cluster`/`--stack`/`--kubeconfig` default from `CLUSTER_NAME`/`PULUMI_STACK`/`KUBECONFIG`; `--registry-dir` defaults to `config/clusters` (recipes `cd` to the repo root). Input loading threads one `prereqsInput` through an `Either` pipeline.

- [ ] **Step 1: Write the failing test.** Write `cmd/cluster-ops/preflight_test.go`:

```go
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func runPreflight(t *testing.T, args ...string) error {
	t.Helper()
	app := &cli.App{
		Name:           "preflight-test",
		Commands:       []*cli.Command{preflightCommand()},
		ExitErrHandler: func(*cli.Context, error) {}, // keep os.Exit out of tests
	}
	return app.Run(append([]string{"preflight-test", "preflight"}, args...))
}

func TestDeployerAccessNeedsCluster(t *testing.T) {
	t.Setenv("CLUSTER_NAME", "")
	err := runPreflight(t, "deployer-access", "--sa-key", "k.json")
	require.ErrorIs(t, err, errNoCluster)
}

func TestDeployerAccessUnknownCluster(t *testing.T) {
	err := runPreflight(t, "deployer-access", "--cluster", "nope", "--sa-key", "k.json",
		"--registry-dir", t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "registry entry")
}

func TestDeployerAccessClusterFromEnv(t *testing.T) {
	t.Setenv("CLUSTER_NAME", "from-env")
	err := runPreflight(t, "deployer-access", "--sa-key", "k.json", "--registry-dir", t.TempDir())
	require.Error(t, err)
	require.NotErrorIs(t, err, errNoCluster, "CLUSTER_NAME must default --cluster")
}

func TestBackendPrereqsNeedsStack(t *testing.T) {
	t.Setenv("PULUMI_STACK", "")
	err := runPreflight(t, "backend-prereqs", "--folder", "modware-order")
	require.ErrorIs(t, err, errNoStack)
}

func TestBackendPrereqsMissingStackFile(t *testing.T) {
	err := runPreflight(t, "backend-prereqs", "--folder", filepath.Join(t.TempDir(), "modware-order"),
		"--stack", "dcr-kube1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scaffold it first")
}

func TestPulumiBackendUnsetEnvExitsOne(t *testing.T) {
	for _, v := range []string{"PULUMI_GCP_CREDENTIALS", "PULUMI_SECRET_PROVIDER", "PULUMI_BACKEND_URL", "PULUMI_STACK"} {
		t.Setenv(v, "")
	}
	stdout := os.Stdout
	devnull, err := os.Open(os.DevNull)
	require.NoError(t, err)
	os.Stdout = devnull
	t.Cleanup(func() { os.Stdout = stdout; _ = devnull.Close() })

	err = runPreflight(t, "pulumi-backend")
	var exit cli.ExitCoder
	require.True(t, errors.As(err, &exit), "gate failure must be a cli.ExitCoder")
	require.Equal(t, 1, exit.ExitCode())
}
```

- [ ] **Step 2: Run it and confirm it fails.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./cmd/cluster-ops/...`

Expected: FAIL — `undefined: preflightCommand`, `undefined: errNoCluster`.

- [ ] **Step 3: Write the implementation.** Write `cmd/cluster-ops/preflight.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	S "github.com/IBM/fp-go/v2/string"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

const deployerRolesFile = "gcs-files/roles-permissions/deployer-roles.txt"

var (
	errNoCluster = errors.New("no cluster name — pass --cluster or set CLUSTER_NAME (via cluster env)")
	errNoStack   = errors.New("no stack name — pass --stack or set PULUMI_STACK (via cluster env)")
)

func registryDirFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "registry-dir",
		Value: "config/clusters",
		Usage: "Cluster registry directory",
	}
}

func clusterFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "cluster", Aliases: []string{"c"},
			EnvVars: []string{"CLUSTER_NAME"},
			Usage:   "Cluster name (defaults to CLUSTER_NAME)",
		},
		&cli.StringFlag{
			Name: "sa-key", Aliases: []string{"k"},
			Required: true,
			Usage:    "Deployer service account key JSON path",
		},
		registryDirFlag(),
	}
}

func preflightCommand() *cli.Command {
	return &cli.Command{
		Name:  "preflight",
		Usage: "Read-only deploy gates; exit 1 lists every failed check",
		Subcommands: []*cli.Command{
			{
				Name:   "backend-prereqs",
				Usage:  "Namespace, ArangoDB Secret, database via the app's Service, port",
				Action: backendPrereqsAction,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name: "folder", Aliases: []string{"f"},
						Required: true,
						Usage:    "Service project folder (stack config source)",
					},
					&cli.StringFlag{
						Name: "stack", Aliases: []string{"s"},
						EnvVars: []string{"PULUMI_STACK"},
						Usage:   "Pulumi stack name (defaults to PULUMI_STACK)",
					},
					&cli.StringFlag{
						Name: "arango-service", Aliases: []string{"a"},
						Value: "arangodb",
						Usage: "ArangoDB Service name the app connects to",
					},
					&cli.StringFlag{
						Name:    "kubeconfig",
						EnvVars: []string{"KUBECONFIG"},
						Usage:   "Kubeconfig path (defaults to KUBECONFIG)",
					},
					registryDirFlag(),
				},
			},
			{
				Name:   "deployer-access",
				Usage:  "Deployer key project, cluster access via kops state, KMS round-trip",
				Action: deployerAccessAction,
				Flags:  clusterFlags(),
			},
			{
				Name:   "deploy-credentials",
				Usage:  "deployer-access plus every IAM permission of deployer-roles.txt",
				Action: deployCredentialsAction,
				Flags:  clusterFlags(),
			},
			{
				Name:   "pulumi-backend",
				Usage:  "PULUMI_* wiring: env, manager key, state bucket, KMS key, active login",
				Action: pulumiBackendAction,
			},
		},
	}
}

// reportGate prints every result; a failed gate exits 1 after one summary
// line, a green gate prints okLines (the last is the Next: recipe hint).
func reportGate(results []preflight.Result, failMsg string, okLines ...string) error {
	preflight.Print(os.Stdout, results)
	if preflight.Failed(results) {
		return cli.Exit("\nERROR: "+failMsg+" — fix the FAIL lines above, then re-run.", 1)
	}
	for _, line := range okLines {
		fmt.Println(line)
	}
	return nil
}

func runGate(checks []IO.IO[preflight.Result]) []preflight.Result {
	return preflight.Run(checks)
}

// ---------- deployer-access / deploy-credentials ----------

func loadDeployerConfig(cltx *cli.Context) E.Either[error, backend.DeployerConfig] {
	return F.Pipe3(
		cltx.String("cluster"),
		E.FromPredicate(S.IsNonEmpty, func(string) error { return errNoCluster }),
		E.Chain(func(name string) E.Either[error, registry.Entry] {
			return registry.LoadByName(cltx.String("registry-dir"), name)
		}),
		E.Map[error](func(entry registry.Entry) backend.DeployerConfig {
			return backend.DeployerConfig{Entry: entry, SAKeyPath: cltx.String("sa-key")}
		}),
	)
}

func deployerAccessAction(cltx *cli.Context) error {
	return F.Pipe1(
		loadDeployerConfig(cltx),
		E.Fold(F.Identity[error], func(cfg backend.DeployerConfig) error {
			return reportGate(
				runGate(backend.DeployerChecks(cfg)),
				"deployer access verification failed",
				fmt.Sprintf("\nDeployer access verified for %s.", cfg.Entry.Cluster),
				fmt.Sprintf("Next: just ci check-deploy-credentials --cluster %s --sa-key %s", cfg.Entry.Cluster, cfg.SAKeyPath),
			)
		}),
	)
}

func deployCredentialsAction(cltx *cli.Context) error {
	return F.Pipe1(
		loadDeployerConfig(cltx),
		E.Fold(F.Identity[error], func(cfg backend.DeployerConfig) error {
			return reportGate(
				runGate(append(
					backend.DeployerChecks(cfg),
					backend.CheckIAMPermissions(backend.CredentialsConfig{
						Entry:         cfg.Entry,
						SAKeyPath:     cfg.SAKeyPath,
						RolesFilePath: deployerRolesFile,
					}),
				)),
				"deploy credentials verification failed",
				fmt.Sprintf("\nDeploy credentials verified for %s.", cfg.Entry.Cluster),
				fmt.Sprintf(
					"Next: just ci set-deploy-secret --cluster %s --sa-key %s --repos <owner/name>",
					cfg.Entry.Cluster, cfg.SAKeyPath,
				),
			)
		}),
	)
}

// ---------- backend-prereqs ----------

// stackProperties is the slice of <project>:properties the gate needs.
type stackProperties struct {
	AppName        string `yaml:"appName"`
	Port           int    `yaml:"port"`
	ArangodbSecret struct {
		Name    string `yaml:"name"`
		Userkey string `yaml:"userkey"`
		Passkey string `yaml:"passkey"`
	} `yaml:"arangodbSecret"`
}

// prereqsInput threads the resolved inputs through the loading pipeline.
type prereqsInput struct {
	folder, stack, project string
	props                  stackProperties
	entry                  registry.Entry
	clients                kube.Clients
}

func readStackProperties(in prereqsInput) E.Either[error, prereqsInput] {
	path := filepath.Join(in.folder, "Pulumi."+in.stack+".yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return E.Left[prereqsInput](fmt.Errorf("MISSING: %s — scaffold it first (scaffold-backend-stack)", path))
	}
	var doc struct {
		Config map[string]stackProperties `yaml:"config"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return E.Left[prereqsInput](fmt.Errorf("parse %s: %w", path, err))
	}
	props, ok := doc.Config[in.project+":properties"]
	if !ok {
		return E.Left[prereqsInput](fmt.Errorf("%s has no %s:properties block", path, in.project))
	}
	in.props = props
	return E.Right[error](in)
}

func loadPrereqsInput(cltx *cli.Context) E.Either[error, prereqsInput] {
	folder := filepath.Clean(cltx.String("folder"))
	return F.Pipe4(
		prereqsInput{folder: folder, stack: cltx.String("stack"), project: filepath.Base(folder)},
		E.FromPredicate(func(in prereqsInput) bool { return in.stack != "" }, func(prereqsInput) error { return errNoStack }),
		E.Chain(readStackProperties),
		E.Chain(func(in prereqsInput) E.Either[error, prereqsInput] {
			return F.Pipe1(
				registry.LoadByStack(cltx.String("registry-dir"), in.stack),
				E.Map[error](func(entry registry.Entry) prereqsInput { in.entry = entry; return in }),
			)
		}),
		E.Chain(func(in prereqsInput) E.Either[error, prereqsInput] {
			return F.Pipe1(
				kube.NewClients(cltx.String("kubeconfig"), ""),
				E.Map[error](func(c kube.Clients) prereqsInput { in.clients = c; return in }),
			)
		}),
	)
}

func backendPrereqsAction(cltx *cli.Context) error {
	return F.Pipe1(
		loadPrereqsInput(cltx),
		E.Fold(F.Identity[error], func(in prereqsInput) error {
			return reportGate(
				runGate(backend.PrereqsChecks(backend.PrereqsConfig{
					Clients:       in.clients,
					Namespace:     in.entry.Namespace,
					SecretName:    in.props.ArangodbSecret.Name,
					UserKey:       in.props.ArangodbSecret.Userkey,
					PassKey:       in.props.ArangodbSecret.Passkey,
					ArangoService: cltx.String("arango-service"),
					AppName:       in.props.AppName,
					Port:          in.props.Port,
				})),
				"prerequisite gate failed",
				fmt.Sprintf("\nAll prerequisites green for %s/%s.", in.project, in.stack),
				fmt.Sprintf(
					"Next: just gcp-pulumi bootstrap-service --stack %s --folder %s --image-tag <published-tag>",
					in.stack, in.folder,
				),
			)
		}),
	)
}

// ---------- pulumi-backend ----------

func pulumiBackendAction(_ *cli.Context) error {
	return reportGate(
		runGate(backend.PulumiChecks(backend.PulumiBackendConfig{
			CredsPath:       os.Getenv("PULUMI_GCP_CREDENTIALS"),
			SecretsProvider: os.Getenv("PULUMI_SECRET_PROVIDER"),
			BackendURL:      os.Getenv("PULUMI_BACKEND_URL"),
			Stack:           os.Getenv("PULUMI_STACK"),
		})),
		"Pulumi backend check failed (docs/reference/pulumi/backend-bootstrap.md)",
		"\nBackend wiring looks correct.",
	)
}
```

- [ ] **Step 4: Register the command in `cmd/cluster-ops/main.go`.**

In the `Commands` list of `main()`, add `preflightCommand(),` after `validateCommand(),`:

```go
		Commands: []*cli.Command{
			kopsCommand(),
			validateCommand(),
			preflightCommand(),
```

Run: `go run ./cmd/cluster-ops preflight --help`

Expected: lists `backend-prereqs`, `deployer-access`, `deploy-credentials`, `pulumi-backend`.

- [ ] **Step 5: Run the tests and confirm they pass.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./cmd/cluster-ops/...`

Expected: `DONE 6 tests` with no failures.

- [ ] **Step 6: Lint.**

Run: `golangci-lint run ./cmd/cluster-ops/...`

Expected: `0 issues.`

- [ ] **Step 7: Commit.**

```bash
git add cmd/cluster-ops/preflight_test.go cmd/cluster-ops/preflight.go cmd/cluster-ops/main.go
git commit -m "feat(cli): add cluster-ops preflight command"
```

---

## Task 12: Rewrite check-backend-prereqs Recipe and Contract Test

**Files:** Modify `just_modules/pulumi.justfile:641`, `scripts/test-backend-prereqs.sh`

- [ ] Read current recipe body at `just_modules/pulumi.justfile:641-759`
- [ ] Drop the `arango_port` parameter **and its attribute** — the tunnel picks a free local port, so the flag is dead. In `just_modules/pulumi.justfile`, delete line 638:

  ```justfile
  [arg("arango_port", long="arango-port", short="p", help="Local port for the port-forward probe (default 18529)")]
  ```

  and change the usage comment at line 634 to:

  ```justfile
  # Usage: just gcp-pulumi check-backend-prereqs --folder <dir> [--stack <name>] [--arango-service <name>]
  ```

  `just` refuses to parse a justfile whose `[arg(...)]` names an undefined parameter (`error: argument attribute for undefined argument`), which would break every recipe. No caller passes `--arango-port` (`grep -rn "arango-port" just_modules Justfile scripts` → only these lines).

- [ ] Replace the recipe signature and body (keep the remaining `[arg]` attributes, group, no-cd):

```justfile
check-backend-prereqs folder stack="" arango_service="arangodb":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    [ -z "${stack_name}" ] && stack_name="${PULUMI_STACK:-}"
    folder="{{ folder }}"

    go run ./cmd/cluster-ops preflight backend-prereqs \
        --folder {{ quote(folder) }} \
        --stack "${stack_name}" \
        --arango-service {{ quote(arango_service) }}
```

- [ ] Read current contract test `scripts/test-backend-prereqs.sh`
- [ ] Rewrite contract test (mock `go` on PATH, preserve assertions):

```bash
#!/usr/bin/env bash
# Contract tests for backend prerequisite checks:
#   - `just gcp-pulumi check-backend-prereqs` (composite gate)
#   - `just gcp-cluster verify-deployer-access` (deployer identity probe)
#
# Mocks `go run` on PATH (kubectl/kops/gcloud run from Go code, not shell).
# Both recipes must make zero mutations: every recorded mock call is checked.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

tmp="$(mktemp -d)"
mock_bin="${tmp}/bin"
mkdir -p "${mock_bin}"
calls="${tmp}/calls.log"
: > "${calls}"
cleanup() { rm -rf "${tmp}"; }
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

# --- mock go run ---
cat > "${mock_bin}/go" <<'EOF'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
case "${MOCK_GO_MODE:-ok}" in
    "fail-prereqs") exit 1 ;;
    "fail-deployer") [[ "$*" == *"deployer-access"* ]] && exit 1 ;;
esac
exit 0
EOF
chmod +x "${mock_bin}/go"
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

# --- fixtures ---
folder="${tmp}/modware-order"
mkdir -p "${folder}"
cfg="${folder}/Pulumi.dcr-kube1.yaml"
cat > "${cfg}" <<'YAML'
config:
  modware-order:properties:
    appName: order
    arangodbSecret:
      name: order
      passkey: password
      userkey: user
    port: 9250
YAML

sa_key="${tmp}/deployer.json"
cat > "${sa_key}" <<'JSON'
{"project_id": "dcr-kube1", "type": "service_account"}
JSON

echo "=== 1. gate green: go run invoked with correct flags ==="
export MOCK_GO_MODE=ok
export PULUMI_STACK=dcr-kube1
: > "${calls}"
out=$(just gcp-pulumi check-backend-prereqs --folder "${folder}") || fail "green gate failed: ${out}"
grep -q "go run ./cmd/cluster-ops preflight backend-prereqs --folder ${folder} --stack dcr-kube1 --arango-service arangodb" "${calls}" \
    || fail "go run not called with expected flags:\n$(cat "${calls}")"
echo "  green: PASS"

echo "=== 2. gate failure propagates exit code ==="
export MOCK_GO_MODE=fail-prereqs
if out=$(just gcp-pulumi check-backend-prereqs --folder "${folder}" 2>&1); then
    fail "fail-prereqs mode did not propagate exit 1"
fi
echo "  exit propagation: PASS"

echo "=== 3. verify-deployer-access calls deployer-access subcommand ==="
export MOCK_GO_MODE=ok
: > "${calls}"
out=$(just gcp-cluster verify-deployer-access --cluster dcr-kube1 --sa-key "${sa_key}") || fail "deployer happy path failed"
grep -q "go run ./cmd/cluster-ops preflight deployer-access --cluster dcr-kube1 --sa-key ${sa_key}" "${calls}" \
    || fail "deployer-access not invoked:\n$(cat "${calls}")"
echo "  deployer-access: PASS"

echo "backend prerequisite contract tests PASSED"
```

- [ ] Run rewritten test: `bash scripts/test-backend-prereqs.sh`
- [ ] Expected: PASSED
- [ ] Run `just lint-recipes`
- [ ] Expected: zero lint errors
- [ ] Commit:
  ```bash
  git add just_modules/pulumi.justfile scripts/test-backend-prereqs.sh
  git commit -m "refactor(recipes): port check-backend-prereqs to Go (fixes 4 bugs)"
  ```

**Expected:** Recipe calls Go CLI, contract test passes.

---

## Task 13: Rewrite verify-deployer-access Recipe

**Files:** Modify `just_modules/cluster.justfile:2076` (contract coverage: `scripts/test-backend-prereqs.sh` section 3; `scripts/test-backend-bootstrap.sh` runs unchanged)

- [ ] Read current recipe body at `just_modules/cluster.justfile:2076-2151`
- [ ] Replace recipe body (keep [arg] attributes):

```justfile
verify-deployer-access cluster="" sa_key:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    [ -z "${name}" ] && name="${CLUSTER_NAME:-}"

    go run ./cmd/cluster-ops preflight deployer-access \
        --cluster "${name}" \
        --sa-key {{ quote(sa_key) }}
```

- [ ] Do **not** modify `scripts/test-backend-bootstrap.sh`. `bootstrap-service` reaches the gate through a nested `just gcp-pulumi check-backend-prereqs` call, which that test already intercepts with its `just` mock (`MOCK_PREREQ_MODE`), and `bootstrap-service` itself is unchanged in phase 1. The existing test keeps its stronger assertions — zero deploy-path calls after a gate failure, and the full gate → ensure → tag → preview → up → rollout → image-check order. The `verify-deployer-access` wrapper is covered by section 3 of the Task 12 rewrite of `scripts/test-backend-prereqs.sh`.
- [ ] Run both: `just test-backend-bootstrap && just test-backend-prereqs`
- [ ] Expected: last lines `backend bootstrap-service contract tests PASSED` and `backend prerequisite contract tests PASSED`
- [ ] Run lint: `just lint-recipes`
- [ ] Expected: zero errors
- [ ] Commit:
  ```bash
  git add just_modules/cluster.justfile
  git commit -m "refactor(recipes): port verify-deployer-access to Go"
  ```

**Expected:** Recipe rewired; both contract tests pass unchanged.

---

## Task 14: Rewrite check-deploy-credentials Recipe and Update CI Test

**Files:** Modify `just_modules/ci.justfile:16`, `scripts/test-ci-recipes.sh`

- [ ] Read current recipe body at `just_modules/ci.justfile:16-73`
- [ ] Replace recipe body (keep [arg] attributes):

```justfile
check-deploy-credentials cluster="" sa_key:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    [ -z "${name}" ] && name="${CLUSTER_NAME:-}"

    go run ./cmd/cluster-ops preflight deploy-credentials \
        --cluster "${name}" \
        --sa-key {{ quote(sa_key) }}
```

- [ ] Read `scripts/test-ci-recipes.sh`. It covers THREE recipes: sections 1–2 test `check-deploy-credentials`, sections 3–6 test `set-deploy-secret` and `sync-deploy-vars`. **Only sections 1–2 and the mocks they need change; sections 3–6 stay byte-for-byte** (they use only `REAL_JUST` and the `gh` mock). Do not replace the whole file.
- [ ] Replace lines 1–122 (from the shebang through the end of section 2, `echo "  happy path: PASS …"`) with the block below. Keep line 123 onward (blank line, then `echo "=== 3. set-deploy-secret: upsert carries selected visibility ==="` … end of file) unchanged.

```bash
#!/usr/bin/env bash
# Contract tests for the `just ci` recipes: check-deploy-credentials,
# set-deploy-secret, sync-deploy-vars.
#
# check-deploy-credentials is a thin wrapper over
# `go run ./cmd/cluster-ops preflight deploy-credentials`; go is mocked on
# PATH (MOCK_GO_EXIT selects the gate result) and the probe behaviour —
# wrong-project key, IAM permissions batched via testIamPermissions with
# every missing permission listed — is covered by the Go unit tests in
# internal/preflight/backend. gh is mocked on PATH for the other recipes.
# Asserts: flag pass-through and exit propagation for the gate; var names
# derive from the registry ci_env (never hardcoded), a missing registry entry
# fails, repo names validate, the secret upsert carries --visibility
# selected, and a gh failure propagates with the repo named.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

tmp="$(mktemp -d)"
mock_bin="${tmp}/bin"
mkdir -p "${mock_bin}"
calls="${tmp}/calls.log"
: > "${calls}"
cleanup() { rm -rf "${tmp}"; }
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

cat > "${mock_bin}/gh" <<'EOF'
#!/usr/bin/env bash
echo "gh $*" >> "${MOCK_CALLS_LOG}"
case "${MOCK_GH_MODE:-ok}:$1 $2" in
    "noauth:org list") exit 1 ;;
esac
case "$1 $2" in
    "org list") echo "dictyBase" ;;
    "repo view") echo '{"id": "R_1"}' ;;
    "variable list") echo "PULUMI_STATE_STORAGE	gs://org-bucket" ;;
    "secret set") exit 0 ;;
    "variable set") exit 0 ;;
    "api") echo '{"repositories": [{"name": "modware-order"}]}' ;;
esac
exit 0
EOF
cat > "${mock_bin}/go" <<'EOF'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
exit "${MOCK_GO_EXIT:-0}"
EOF
# Nested just calls: record and succeed.
cat > "${mock_bin}/just" <<'EOF'
#!/usr/bin/env bash
echo "just $*" >> "${MOCK_CALLS_LOG}"
exit 0
EOF
chmod +x "${mock_bin}/gh" "${mock_bin}/go" "${mock_bin}/just"
export MOCK_CALLS_LOG="${calls}"
REAL_JUST="$(command -v just)"
export PATH="${mock_bin}:${PATH}"

sa_key="${tmp}/deployer.json"
cat > "${sa_key}" <<'JSON'
{"project_id": "dcr-kube1", "client_email": "deployer@dcr-kube1.iam.gserviceaccount.com", "type": "service_account"}
JSON

line_of() { grep -n -e "$1" "${calls}" | head -n1 | cut -d: -f1; }
last_of() { grep -n -e "$1" "${calls}" | tail -n1 | cut -d: -f1; }

echo "=== 1. check-deploy-credentials: failing gate propagates ==="
: > "${calls}"
if out=$(MOCK_GO_EXIT=1 "${REAL_JUST}" ci check-deploy-credentials --cluster dcr-kube1 --sa-key "${sa_key}" 2>&1); then
    fail "failing preflight exited 0: ${out}"
fi
echo "  exit propagated: PASS"

echo "=== 2. check-deploy-credentials: flags reach the Go preflight ==="
: > "${calls}"
out=$("${REAL_JUST}" ci check-deploy-credentials --cluster dcr-kube1 --sa-key "${sa_key}") \
    || fail "happy path failed: ${out}"
grep -q -- "go run ./cmd/cluster-ops preflight deploy-credentials --cluster dcr-kube1 --sa-key ${sa_key}" "${calls}" \
    || fail "deploy-credentials not invoked with the flags: $(cat "${calls}")"
: > "${calls}"
CLUSTER_NAME=dcr-kube1 "${REAL_JUST}" ci check-deploy-credentials --sa-key "${sa_key}" >/dev/null \
    || fail "CLUSTER_NAME default failed"
grep -q -- "--cluster dcr-kube1" "${calls}" || fail "CLUSTER_NAME not used as the --cluster default: $(cat "${calls}")"
echo "  happy path: PASS (flags + CLUSTER_NAME default)"
```

  The gcloud/kubectl/kops mocks and the `deployer-roles.txt` fixture are removed: after this task nothing in the script reaches those tools (sections 3–6 never did), and the role/permission logic lives in Go.

- [ ] Run updated test: `chmod +x scripts/test-ci-recipes.sh && just test-ci-recipes`
- [ ] Expected: sections 1–6 each print `PASS`; the script's existing final line prints unchanged
- [ ] Run lint: `just lint-recipes`
- [ ] Expected: zero errors
- [ ] Commit:
  ```bash
  git add just_modules/ci.justfile scripts/test-ci-recipes.sh
  git commit -m "refactor(recipes): port check-deploy-credentials to Go (fixes IAM batch bug)"
  ```

**Expected:** Recipe and test updated.

---

## Task 15: Rewire check-backend Recipe (Pulumi Backend Check)

**Files:** Modify `just_modules/pulumi.justfile:281-289` (existing `check-backend` recipe — do NOT add a second one; just rejects duplicate recipe names), delete `scripts/pulumi/check-backend.sh`, create `scripts/test-pulumi-backend.sh`, modify `Justfile:187-190,264`

- [ ] Replace the existing `check-backend` recipe (`just_modules/pulumi.justfile:281-289`, which runs `scripts/pulumi/check-backend.sh`) with:

```justfile
# Verify Pulumi backend wiring (env vars, state bucket, KMS key, active login).
# Read-only. Reads PULUMI_* env vars from the cluster shell; fails naming the
# missing layer. Zero mutations.
# Usage: just gcp-pulumi check-backend
[group('pulumi-management')]
[no-cd]
check-backend:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    go run ./cmd/cluster-ops preflight pulumi-backend
```

- [ ] Create contract test `scripts/test-pulumi-backend.sh`:

```bash
#!/usr/bin/env bash
# Contract test for check-backend: invokes Go CLI, reads PULUMI_* env vars.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

tmp="$(mktemp -d)"
mock_bin="${tmp}/bin"
mkdir -p "${mock_bin}"
calls="${tmp}/calls.log"
: > "${calls}"
cleanup() { rm -rf "${tmp}"; }
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

cat > "${mock_bin}/go" <<'EOF'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
exit 0
EOF
chmod +x "${mock_bin}/go"
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

# Set minimal Pulumi env
export PULUMI_GCP_CREDENTIALS=/tmp/fake.json
export PULUMI_SECRET_PROVIDER=gcpkms://fake
export PULUMI_BACKEND_URL=gs://fake-bucket
export PULUMI_STACK=prod

echo "=== 1. check-backend invokes Go CLI ==="
: > "${calls}"
just gcp-pulumi check-backend >/dev/null || fail "check-backend failed"
grep -q "go run ./cmd/cluster-ops preflight pulumi-backend" "${calls}" \
    || fail "pulumi-backend not invoked: $(cat "${calls}")"
echo "  Go CLI invoked: PASS"

echo "=== 2. check-backend propagates a failing gate ==="
: > "${calls}"
if MOCK_GO_EXIT=1 just gcp-pulumi check-backend >/dev/null 2>&1; then
    fail "check-backend exited 0 although the preflight failed"
fi
echo "  exit propagated: PASS"

echo "=== 3. old script is gone ==="
[ ! -e scripts/pulumi/check-backend.sh ] || fail "scripts/pulumi/check-backend.sh still exists"
echo "  script removed: PASS"

echo "check-backend contract test PASSED"
```

  The mock `go` above must honour the failure mode — use this body for it:

```bash
cat > "${mock_bin}/go" <<'EOF'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
exit "${MOCK_GO_EXIT:-0}"
EOF
```

- [ ] Delete the replaced script (keep `scripts/lib/check-helpers.sh` — eight other recipes still source it):

```bash
git rm scripts/pulumi/check-backend.sh
```

- [ ] Wire the test into `Justfile`. Add after `test-ci-recipes` (`Justfile:187-190`):

```justfile
test-pulumi-backend:
    #!/usr/bin/env bash
    set -euo pipefail
    "{{ justfile_directory() }}/scripts/test-pulumi-backend.sh"
```

  and append `test-pulumi-backend` to the `check:` dependency list at `Justfile:264`, before `build`.

- [ ] Run test: `chmod +x scripts/test-pulumi-backend.sh && just test-pulumi-backend`
- [ ] Expected: last line `check-backend contract test PASSED`
- [ ] Run lint: `just lint-recipes`
- [ ] Expected: zero errors
- [ ] Commit:
  ```bash
  git add just_modules/pulumi.justfile scripts/test-pulumi-backend.sh Justfile
  git commit -m "refactor(pulumi): run check-backend through the Go preflight"
  ```

**Expected:** Existing `check-backend` recipe now calls the Go preflight; shell script removed; contract test runs in `just check`.

---

## Task 16: Add the k3d integration test

**Files:** Create `internal/preflight/backend/integration_test.go`

Covers what fakes cannot: the real API server (namespace and Secret checks) and terratest's port-forward. `TestIntegrationTunnelMissingServiceIsAnError` drives terratest through its internal `t.Fatal` path on a real cluster — the adapter must return an error, not panic or hand back a nil tunnel. Uses `kube-system/kube-dns` (k3s CoreDNS) so it needs no workload deployed.

- [ ] **Step 1: Write the test.** Write `internal/preflight/backend/integration_test.go`:

```go
//go:build integration

package backend_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Run with: just test-preflight-integration (k3d). Exercises the parts the
// fakes cannot: the real API server and terratest's port-forward.

func integrationClients(t *testing.T) kube.Clients {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set — run via just test-preflight-integration")
	}
	return F.Pipe1(
		kube.NewClients(kubeconfig, ""),
		E.Fold(
			func(err error) kube.Clients { t.Fatalf("kube clients: %v", err); return kube.Clients{} },
			F.Identity[kube.Clients],
		),
	)
}

func TestIntegrationNamespaceAndSecret(t *testing.T) {
	clients := integrationClients(t)
	ctx := context.Background()
	ns := fmt.Sprintf("preflight-it-%d", time.Now().UnixNano())
	_, err := clients.Kube.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, clients.Kube.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}))
	})

	cfg := backend.PrereqsConfig{
		Clients: clients, Namespace: ns, SecretName: testSecretName, UserKey: testUserKey, PassKey: testPassKey,
	}
	require.Equal(t, preflight.StatusPass, backend.CheckNamespace(cfg)().Status)

	missing := backend.CheckSecret(cfg)()
	require.Equal(t, preflight.StatusFail, missing.Status)
	require.Equal(t, "not found", missing.Detail)

	_, err = clients.Kube.CoreV1().Secrets(ns).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecretName},
		Data:       map[string][]byte{testUserKey: []byte("root"), testPassKey: []byte("pw")},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Equal(t, preflight.StatusPass, backend.CheckSecret(cfg)().Status)

	absent := cfg
	absent.Namespace = ns + "-absent"
	require.Equal(t, "not found", backend.CheckNamespace(absent)().Detail)
}

// The tunnel resolves a real Service to a ready pod on a free local port.
// k3s ships CoreDNS behind kube-system/kube-dns (TCP 53).
func TestIntegrationTunnelToService(t *testing.T) {
	clients := integrationClients(t)
	tunnel := F.Pipe1(
		kube.OpenServiceTunnel(clients, "kube-system", "kube-dns", 53)(),
		E.Fold(
			func(err error) *kube.Tunnel { t.Fatalf("open tunnel: %v", err); return nil },
			F.Identity[*kube.Tunnel],
		),
	)
	t.Cleanup(func() { _ = tunnel.Close()() })

	require.NotEqual(t, 18529, tunnel.LocalPort)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", tunnel.LocalPort), 5*time.Second)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

// A missing Service drives terratest through its internal t.Fatal path; the
// adapter must turn that into an error instead of crashing.
func TestIntegrationTunnelMissingServiceIsAnError(t *testing.T) {
	clients := integrationClients(t)
	got := kube.OpenServiceTunnel(clients, "kube-system", "no-such-service", 8529)()
	require.True(t, E.IsLeft(got), "missing Service must be an error, not a panic or nil tunnel")
}
```

- [ ] **Step 2: Confirm it compiles and lints under the tag.**

Run: `go vet -tags=integration ./internal/preflight/backend/ && golangci-lint run --build-tags=integration ./internal/preflight/backend/...`

Expected: no vet output; `0 issues.`

- [ ] **Step 3: Confirm the default build skips it.**

Run: `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./internal/preflight/backend/...`

Expected: `DONE 34 tests` (unchanged — the file only builds with `-tags=integration`).

- [ ] **Step 4: Commit.**

```bash
git add internal/preflight/backend/integration_test.go
git commit -m "test(preflight): add k3d integration tests for checks and tunnel"
```

---

## Task 17: Add test-preflight-integration Recipe

**Files:** Modify `Justfile` (new recipe after `test-ci-recipes`, `Justfile:187-190`). Do **not** add it to `check:` at `Justfile:264` — `just check` must pass on machines without a running k3d cluster.

- [ ] Add new recipe to `Justfile`:

```justfile
# Run the preflight integration tests against the local k3d cluster.
# Needs the cluster running (just k3d start-cluster). Not part of `just check`.
# Usage: just test-preflight-integration [--name <k3d-cluster>]
[arg("name", long="name", short="n", help="k3d cluster name")]
[group('testing')]
[no-cd]
test-preflight-integration name="k3d-dev-cluster":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    if ! k3d cluster get {{ quote(name) }} >/dev/null 2>&1; then
        echo "ERROR: k3d cluster {{ name }} not found — start it with 'just k3d start-cluster'." >&2
        exit 1
    fi

    # `k3d kubeconfig get` prints YAML to stdout, not a path: write it to a
    # temp file and point KUBECONFIG at the file. Never switches global context.
    kubeconfig_file=$(mktemp /tmp/k3d-kubeconfig-XXXXXX)
    trap 'rm -f "${kubeconfig_file}"' EXIT
    k3d kubeconfig get {{ quote(name) }} > "${kubeconfig_file}"

    KUBECONFIG="${kubeconfig_file}" gotestsum --format-hide-empty-pkg --format dots -- \
        -tags=integration -count=1 ./internal/preflight/backend/...
```

- [ ] Run `just lint-recipes`
- [ ] Expected: zero errors (quote() on every interpolation, `XXXXXX` mktemp suffix)
- [ ] Run `just test-preflight-integration` (k3d running)
- [ ] Expected: `DONE 3 tests` with 0 failures. With k3d stopped: exits 1 with `ERROR: k3d cluster k3d-dev-cluster not found`.
- [ ] Commit:
  ```bash
  git add Justfile
  git commit -m "test(preflight): add k3d integration test recipe"
  ```

**Expected:** Opt-in integration recipe added; `just check` unchanged by this task.

---

## Task 18: Update `docs/reference/backend/prerequisites.md`

**Files:** Modify `docs/reference/backend/prerequisites.md`

- [ ] **Step 1:** In `## Behavior` (lines 12–41), replace items 2–4 of the `check-backend-prereqs` list with:

```markdown
2. **Namespace** — exists. A failure names its cause: `not found`,
   `access denied` (RBAC) or `API server unreachable` — the shell reported all
   three as missing.
3. **ArangoDB credentials Secret** — the Secret named by the stack config's
   `arangodbSecret` exists in the namespace and carries non-empty
   `userkey`/`passkey` keys. Key names only — never Secret values.
4. **Application database** — the service's database exists in ArangoDB and
   the credentials work, **through the same Service the app connects to** (the
   `arangodb` Service behind the `ARANGODB_SERVICE_HOST`/`ARANGODB_SERVICE_PORT`
   env vars modware services read). The probe opens a tunnel to the Service on
   a free local port and calls the ArangoDB REST API with the Secret
   credentials in the request header: 200 = green, 401 = credentials,
   404 = database missing (apply the `create-arangodb-databases` stack,
   [ArangoDB deploy](../../arangodb-deploy.md)).
```

- [ ] **Step 2:** In the same section, replace the `verify-deployer-access` list (items 1–3 under "checks the deployer identity the CI pipeline will use") with:

```markdown
1. **Deployer key** — the key file is a GCP SA key whose `project_id` equals
   the registry `gcp_project`.
2. **Cluster access** — exports a kubeconfig from the registry's `kops_state`
   bucket with the deployer key (the export path `composite-deploy` uses), then
   asks the API server whether that identity may `update deployments` in the
   namespace. The export uses `--admin`, so this proves the key can read the
   kops state; the admin identity itself is always allowed.
3. **KMS** — encrypt/decrypt round-trip on the registry's secrets key; the
   location comes from the `kms_secrets_provider` URI.

Every check runs even when another fails; the output lists each `FAIL`.
```

- [ ] **Step 3:** In `## Flags` → `### check-backend-prereqs` (lines 54–62), delete the `--arango-port` row — the tunnel picks a free local port.
- [ ] **Step 4:** Run `just docs docs-lint`. Expected: no findings.
- [ ] **Step 5: Commit.**

```bash
git add docs/reference/backend/prerequisites.md
git commit -m "docs(backend): describe the Go prerequisite and deployer gates"
```

---

## Task 19: Update `docs/reference/backend/ci-credentials.md`

**Files:** Modify `docs/reference/backend/ci-credentials.md`

The doc has no `## Behavior` heading — the check lives under `## check-deploy-credentials`. The `## Flags` table also covers `set-deploy-secret`; **leave it unchanged** (no flag changed).

- [ ] **Step 1:** Replace the numbered list under `## check-deploy-credentials` (lines 42–49) with:

```markdown
1. Key file parses as GCP SA JSON; its `project_id` equals the registry's
   `gcp_project` — a dev key against a prod registry fails here.
2. The SA holds every permission of every role in `deployer-roles.txt`:
   roles expand to permissions, which are tested **as the deployer key** with
   `testIamPermissions` (100 per call). Grants through groups or inherited from
   the folder/org count; every missing permission is listed.
3. Kubeconfig export from the registry's `kops_state` bucket works — the same
   export path `composite-deploy` uses at deploy time.
4. KMS encrypt/decrypt round-trip on the registry's `kms_secrets_provider`
   key (stack operations fail cryptically without the KMS role).
5. Never prints key material or secret values.
```

- [ ] **Step 2:** Run `just docs docs-lint`. Expected: no findings.
- [ ] **Step 3: Commit.**

```bash
git add docs/reference/backend/ci-credentials.md
git commit -m "docs(backend): describe the permission-level deploy credential check"
```

---

## Task 20: Update `docs/reference/pulumi/check-backend.md`

**Files:** Modify `docs/reference/pulumi/check-backend.md`

`check-backend` already has its own reference doc (linked from `backend-bootstrap.md#stages`); update it rather than adding a second copy to `backend-bootstrap.md`.

- [ ] **Step 1:** Replace `## Behavior` items 1–5 and the line after them (lines 13–19) with:

```markdown
1. Requires all four `PULUMI_*` variables — anything empty means the shell is not the cluster shell.
2. Checks the manager key file exists at `$PULUMI_GCP_CREDENTIALS`; the remaining checks authenticate with that key explicitly.
3. Checks the state bucket `$PULUMI_BACKEND_URL` exists and has object versioning enabled.
4. Parses the `gcpkms://` URI in `$PULUMI_SECRET_PROVIDER` into project/location/keyring/key and reads the crypto key. Other providers report `INFO`.
5. Compares the active `pulumi whoami --json` URL with `$PULUMI_BACKEND_URL` exactly — `pulumi login` is machine-global, so a stale login silently points at another backend.

Prints one `PASS`/`FAIL`/`INFO` line per check — a check whose input variable is unset reports `INFO skipped` — then a summary; exits non-zero when any check failed.
```

- [ ] **Step 2:** Replace the `## Failures and Fixes` table rows (lines 34–41) with:

```markdown
| `Pulumi env vars unset: …` | Run outside the `cluster-env` sub-shell | `just cluster-env --env <env> --cluster <cluster-name>` |
| `manager key missing at …` | Key file deleted or not yet created | `just gcp-sa create-sa --sa-name pulumi-manager` |
| `state bucket … not found or unreachable` | Bucket not created, or no read access | Re-run `just gcp-pulumi bootstrap-backend` |
| `versioning NOT enabled` | Pre-existing bucket without versioning | `just gcp-pulumi pulumi-gcs-setup` re-asserts it |
| `KMS key … not reachable` | Keyring/key not created, or no read access | `just gcp-kms create-keyring-and-key` |
| `malformed KMS URI` | Hand-edited env file | Regenerate: `just create-cluster-env --force yes` |
| `cannot determine the active Pulumi backend (not logged in?)` | No `pulumi login` on this machine | Re-run `just gcp-pulumi pulumi-gcs-setup` |
| `active login is 'X', expected Y` | Machine-global login left over from another cluster | Re-run `just gcp-pulumi pulumi-gcs-setup` |
```

- [ ] **Step 3:** Run `just docs docs-lint`. Expected: no findings.
- [ ] **Step 4: Commit.**

```bash
git add docs/reference/pulumi/check-backend.md
git commit -m "docs(pulumi): describe the Go check-backend gate"
```

---

## Task 21: Run `just check` (Gate)

**Files:** None (verification task)

- [ ] Prerequisite: `scripts/test-register-cluster.sh` section 3 needs the gitignored env file `.env.prod.dcr-kube1` in the checkout you run from (it already fails on a fresh worktree with `ERROR: env file not found: .env.prod.dcr-kube1`, independent of this plan). Run `just check` from the main checkout, or copy that env file into the worktree first.
- [ ] Run `just check; echo "exit=$?"`
- [ ] Expected: each contract script prints its own `… contract tests PASSED` line (e.g. `backend prerequisite contract tests PASSED`, `check-backend contract test PASSED`), `just docs docs-lint` runs last with no errors, and the final line is `exit=0`. `just check` stops at the first failing dependency, so a non-zero exit names the failing recipe in the line above it.
- [ ] If lint-recipes fails: read error, fix recipe (likely quote() or XXXXXX pattern), re-run
- [ ] If contract test fails: read error, fix test mock or recipe invocation, re-run
- [ ] If go build fails: read error, fix import/syntax, re-run
- [ ] Separately, with k3d running: `just test-preflight-integration` — `DONE 3 tests`, 0 failures (not part of `just check`)
- [ ] `just check` exits 0
- [ ] Commit any fixes from this step (if needed)

**Expected:** `just check` exits 0, zero failures.

---

## Task 22: Close git-bug Issue

**Files:** None (git-bug only)

- [ ] Retrieve issue ID from Task 1 output
- [ ] Run `/git-bug close <id>` (or `git-bug close <id>` if not in Claude session)
- [ ] Run `/git-bug sync` to push bug data to remote
- [ ] Expected: issue closed, synced

**Expected:** git-bug issue closed and synced.

---

## Known limitations

- **The deployments-permission probe cannot fail for a valid key.** `KopsExport` uses `kops export kubeconfig --admin`, exactly like the shell recipe it replaces, so the access review runs as the cluster admin and is always allowed. The check proves the deployer key can read the kops state bucket and reach the API server. Proving the deployer's own RBAC needs a non-admin kubeconfig (or an access review with `User`/`Groups` set) — a design decision deferred to phase 2.
- **`testIamPermissions` runs at project level.** A permission that only applies to a narrower resource is reported missing by the API; if a role in `deployer-roles.txt` carries such permissions, the check needs a per-resource call. Verify once against `dcr-kube1` after Task 14.

## Self-Review

| Requirement | Task(s) |
|-------------|---------|
| Shared foundation: registry, results/printer, kube clients/classification, tunnel | 3, 4, 5, 6 |
| Port `check-backend-prereqs` | 7, 11, 12, 18 |
| Port `verify-deployer-access` | 8, 11, 13, 18 |
| Port `check-deploy-credentials` | 8, 9, 11, 14, 19 |
| Port `check-backend` | 10, 11, 15, 20 |
| Fixed local port + `nc -z` false pass | 6 (free port), 7 (decoy regression test) |
| Password in `curl -u` argv | 7 (`ProbeDatabase` header auth, asserted in test) |
| Not-found / forbidden / unreachable conflation | 5 (`Classify`), 7 (`lookupFailure`, tested per cause) |
| KMS location hardcoded to `us-central1` | 3 (`ParseKMSKey`), 8, 10 |
| IAM role check: group/inherited grants missed, stops at first miss | 9 (batched `testIamPermissions`, all missing listed) |
| `jsonpath={.data.${k}}` breaks on dotted keys | 7 (`Secret.Data` lookup) |
| Key-project guard kept | 8 (`CheckKeyProject`) |
| All five `check-backend.sh` checks kept | 10 |
| terratest `t.Fatal` path returns an error | 6 (unit), 16 (real cluster) |
| Contract tests: flag pass-through, exit propagation, gate-before-mutation order | 12, 14, 15 (bootstrap/services tests unchanged and passing) |
| Integration test (opt-in, not in `just check`) | 16, 17 |
| `just check` green | 21 |
| git-bug lifecycle | 1, 22 |
