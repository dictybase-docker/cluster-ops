# Unified `cluster-ops` CLI — Migration Plan

**Date:** 2026-07-18
**Status:** Proposed
**Target:** Replace shell logic in just recipes with a single Go binary (`cluster-ops`) importing existing `internal/*` packages and new subcommand logic.
**Prerequisite review:** [`docs/recipe-go-migration-review.md`](recipe-go-migration-review.md)

## Problem Statement

The current cluster lifecycle is driven by shell logic embedded in `just` recipes spread across `just_modules/cluster.justfile` and the top-level `justfile`. This creates concrete, recurring pain:

1. **Cross-platform brittleness.** macOS `sed -i ''` differs from GNU `sed -i`. The `cluster-cred` recipe uses macOS-specific syntax that breaks on Linux — and vice versa. Every new team member on a different OS hits this within their first week.
2. **Silent failure modes.** kOps version detection parses `kops version --short` and silently falls back to `"0.0.0"` on parse failure, dispatching the wrong command with no warning. The `diff` exit-code logic conflates "differences found" (exit 1) with actual errors (exit 2+).
3. **Untestable logic.** Shell recipes embedded in justfiles cannot be unit-tested. Every change requires manual end-to-end execution against a live cluster. The 7 kubectl-based HA validation checks have no automated coverage.
4. **Duplicate infrastructure.** Four separate `cmd/*/main.go` entrypoints (`gcp`, `kops`, `util`, `custodian`) each duplicate flag parsing, logging setup, and error handling. The `internal/*` packages already contain the real logic — the `cmd` binaries are thin wrappers that diverge independently.
5. **Growing maintenance burden.** ~200 lines of shell across 9 recipes, plus ~525 lines of redundant `cmd/*/main.go` code. Every new cluster operation adds more shell that cannot share helpers with the Go code.

**If we do nothing:** Shell logic continues to grow, cross-platform issues recur, and the gap between the Go `internal/*` packages (which are well-structured) and the operational interface (which is ad-hoc shell) widens. New contributors must understand both Go abstractions and shell recipes to make any change.

## Proposed Solution

Unify the four separate `cmd/*/main.go` entrypoints and ~200 lines of embedded shell logic into a single `cluster-ops` Go binary using the `urfave/cli` framework (already in `go.mod`). Each shell recipe becomes a subcommand backed by the existing `internal/*` Go packages, with new `internal/kops/exec.go` helpers providing lazy `IOEither` wrappers around `kops`/`kubectl`/`gcloud` exec calls. Just recipes shrink to thin wrappers (`./bin/cluster-ops kops update`). Net change: ~810 new lines of testable Go, ~525 lines deleted (shell + redundant `cmd/*/main.go`).

## Table of Contents

- [Problem Statement](#problem-statement)
- [Proposed Solution](#proposed-solution)
- [Alternatives Considered](#alternatives-considered)
- [Success Metrics](#success-metrics)
- [Out of Scope](#out-of-scope)
- [Open Questions](#open-questions)
- [0. Dependencies (all already in go.mod)](#0-dependencies-all-already-in-gomod)
- [1. New file: `cmd/cluster-ops/main.go`](#1-new-file-cmdcluster-opsmaingo)
- [2. Shared exec helpers: `internal/kops/exec.go`](#2-shared-exec-helpers-internalkopsexecgo)
- [3. `cluster-cred` migration: `internal/util/credential.go`](#3-cluster-cred-migration-internalutilcredentialgo)
- [4. `update-cluster` migration: `internal/kops/update.go`](#4-update-cluster-migration-internalkopsupdatego)
- [5. `delete-cluster` migration: `internal/kops/delete.go`](#5-delete-cluster-migration-internalkopsdeletego)
- [6. `validate-kops-ha` migration: `internal/custodian/validate.go`](#6-validate-kops-ha-migration-internalcustodianvalidatego)
- [7. `create-state-bucket` migration: modify `internal/gcp/kops_state_bucket.go`](#7-create-state-bucket-migration-modify-internalgcpkops_state_bucketgo)
- [8. `apply-instancegroups` migration: `internal/kops/instancegroup.go`](#8-apply-instancegroups-migration-internalkopsinstancegroupgo)
- [9. `recreate-cluster` migration: `internal/kops/recreate.go`](#9-recreate-cluster-migration-internalkopsrecreatego)
- [10. Justfile changes](#10-justfile-changes)
- [11. Deletion of old entrypoints](#11-deletion-of-old-entrypoints)
- [12. File change summary](#12-file-change-summary)
- [13. Quality standards](#13-quality-standards)
- [14. Execution order](#14-execution-order)

## Alternatives Considered

| Option | Pros | Cons | Verdict |
|--------|------|------|---------|
| **Unified Go CLI with urfave/cli (proposed)** | Reuses existing `internal/*` packages, single binary, testable, cross-platform, same framework as existing `cmd/gcp/main.go` | New code to write (~810 net lines), team must learn urfave/cli if unfamiliar | **Chosen** |
| Status quo (keep shell in just recipes) | Zero implementation cost now | Cross-platform bugs recur, untestable, growing maintenance burden, silent failures — see Problem Statement | **Rejected: ongoing cost** |
| Cobra CLI framework | More popular, richer feature set (completions, help templates) | Adds dependency not already in the project, different pattern from existing `cmd/gcp/main.go`, heavier API surface | **Rejected: adds dependency without sufficient benefit** |
| Python rewrite | Rapid prototyping, familiar to some team members | Introduces second language runtime, no reuse of existing `internal/*` Go packages, different deployment story | **Rejected: fragments the codebase** |
| Keep `cmd/*/main.go` but deduplicate helpers | Minimal new code | Still 4 binaries to build/maintain, no unified UX, flag parsing still duplicated across entrypoints | **Rejected: doesn't solve the integration problem** |

## Success Metrics

| Metric | Current | Target | Measured By | Review At |
|--------|---------|--------|-------------|-----------|
| Cross-platform recipe execution | macOS-only (`sed -i ''` breaks on Linux) | `just build && just update-cluster` succeeds on macOS and Linux without modification | CI matrix (macOS + Linux runners) | Before merge |
| Shell lines in just recipes | ~200 lines across 9 recipes | 0 lines (all thin wrappers calling `./bin/cluster-ops`) | `rg '#!/usr/bin/env' just_modules/ justfile` returns no results | Before merge |
| Test coverage on new packages | 0% (shell is untestable) | ≥80% on `internal/kops/exec.go`, `update.go`, `delete.go`, `util/credential.go`, `custodian/validate.go`, `kops/instancegroup.go`, `kops/recreate.go` | `go test -cover ./internal/...` | Before merge |
| `golangci-lint` on new code | N/A (new code) | Zero warnings on `--new-from-rev=main` | CI lint gate | Before merge |
| Redundant `cmd/*/main.go` entrypoints | 4 binaries (`gcp`, `kops`, `util`, `custodian`) | 1 binary (`cluster-ops`) | `ls cmd/` | After step 11 |
| Time to add new cluster operation | ~30 min (shell + Go glue in two places) | ~10 min (one Go file + one just thin wrapper) | Team self-report | 30 days post-merge |

## Out of Scope

This migration does **NOT** cover:

- Changing the deployment pipeline (CI/CD, container image builds, release process)
- Modifying the kOps cluster configuration format or templates
- Rewriting existing `internal/gcp` logic beyond adding UBLA/PAP hardening to `kops_state_bucket.go`
- Adding new cluster operations beyond the 9 recipes listed in sections 2–9
- Migrating monitoring/alerting scripts (Prometheus, Grafana, Datadog)
- Changing the `just` task runner itself — recipes remain in justfiles, they just become thin wrappers
- Migrating `set-env-var` beyond the thin CLI passthrough (it's already backed by a `util` binary)
- Replacing `kops`, `kubectl`, `gcloud`, or `envsubst` — these remain external dependencies invoked via `exec`

## Open Questions

- [ ] **[Platform team]**: Should we keep the old `cmd/*/main.go` binaries as thin passthroughs to `internal/*` for backward compatibility, or delete them entirely? Keeping them means 5 binaries to build but zero workflow disruption. Deleting them means a clean break but requires updating any scripts/CI that reference them.
  **Options**: Delete all 4, keep all 4 as passthroughs, keep only `cmd/gcp` (most used)
  **Blocking**: No — can decide after the unified CLI is proven
  **Decision needed before**: Step 11 (deletion)

- [ ] **[Platform team]**: What is the minimum Go version for the new `cmd/cluster-ops` binary? The `go.mod` currently specifies a version; the new code should match. If we want to use newer stdlib features, we need a conscious bump.
  **Options**: Match current go.mod floor, bump to latest stable (1.23+)
  **Blocking**: No — can proceed with current floor
  **Decision needed before**: Step 1 (new main.go)

- [ ] **[Platform team]**: Do we need integration tests that execute real `kops`/`gcloud` commands, or is unit-test coverage with mocked exec sufficient? Integration tests require a test cluster and GCP project — non-trivial cost but higher confidence.
  **Options**: Unit tests only, unit + integration (separate test target), integration smoke tests only (just `version` and `help` commands)
  **Blocking**: No
  **Decision needed before**: Step 14 completion (defining "done")
---

## 0. Dependencies (all already in go.mod)

| Dependency | Usage |
|-----------|-------|
| `github.com/blang/semver v3.5.1` | kOps version comparison (`semver.Make`, `GTE`, `LT`) |
| `k8s.io/client-go v0.36.2` | Kubernetes API for `validate ha` (already used by custodian) |
| `k8s.io/api v0.36.2` | Typed k8s objects |
| `github.com/urfave/cli/v2 v2.27.7` | CLI flag/command framework |
| `cloud.google.com/go/storage v1.63.0` | GCS bucket hardening (UBLA, PAP) |
| `github.com/stretchr/testify v1.11.1` | Testing |

---

## 1. New file: `cmd/cluster-ops/main.go`

Single entrypoint importing all `internal/*` action functions plus new ones. Follows the existing `cmd/gcp/main.go` subcommand pattern.

```go
package main

import (
    "fmt"
    "log/slog"
    "os"

    "github.com/dictybase-docker/cluster-ops/internal/gcp"
    "github.com/dictybase-docker/cluster-ops/internal/kops"
    "github.com/dictybase-docker/cluster-ops/internal/custodian"
    "github.com/dictybase-docker/cluster-ops/internal/util"
    "github.com/urfave/cli/v2"
)

func main() {
    app := &cli.App{
        Name:  "cluster-ops",
        Usage: "Unified cluster lifecycle management",
        Commands: []*cli.Command{
            kopsCommand(),      // kops create-config, update, delete, recreate
            bucketCommand(),    // bucket create --harden
            igCommand(),        // ig apply, ig list
            validateCommand(),  // validate ha
            envCommand(),       // env set-var, env set-cred
            saCommand(),        // sa create, sa create-key (passthrough)
            apiCommand(),       // api enable, api disable (passthrough)
        },
    }
    if err := app.Run(os.Args); err != nil {
        fmt.Fprintf(os.Stderr, "%v\n", err)
        os.Exit(1)
    }
}
```

Each `xxxCommand()` function returns a `*cli.Command` with its own subcommands and flags — same pattern as `getCommands()` in `cmd/gcp/main.go`.

---

## 2. Shared exec helpers: `internal/kops/exec.go`

Exec helpers already provide lazy `IOEither` wrappers around `kops` commands with backward-compatible convenience functions.

```go
package kops

import (
    "fmt"
    "os"
    "os/exec"
    "strings"

    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
)

// runKopsIOE wraps a kops command as a lazy IOEither.
func runKopsIOE(args ...string) IOE.IOEither[error, string] {
    return IOE.TryCatchError(func() (string, error) {
        cmd := exec.Command("kops", args...)
        cmd.Stdout = os.Stdout
        cmd.Stderr = os.Stderr
        if err := cmd.Run(); err != nil {
            return "", fmt.Errorf("kops %s: %w", strings.Join(args, " "), err)
        }
        return strings.Join(args, " "), nil
    })
}

// runKopsCaptureIOE captures kops stdout as a lazy IOEither.
func runKopsCaptureIOE(args ...string) IOE.IOEither[error, string] {
    return IOE.TryCatchError(func() (string, error) {
        cmd := exec.Command("kops", args...)
        cmd.Stderr = os.Stderr
        out, err := cmd.Output()
        if err != nil {
            return "", fmt.Errorf("kops %s: %w", strings.Join(args, " "), err)
        }
        return string(out), nil
    })
}

// runKops executes immediately — backward-compat wrapper.
func runKops(args ...string) error {
    return E.Fold(
        F.Identity[error],
        func(string) error { return nil },
    )(runKopsIOE(args...)())
}

// runKopsCapture executes immediately — backward-compat wrapper.
func runKopsCapture(args ...string) (string, error) {
    return E.UnwrapError(runKopsCaptureIOE(args...)())
}
```

**Rationale:** IOEither makes kops calls lazy and composable with `IOE.Chain`/`IOE.ChainFirstIOK`. The backward-compat wrappers preserve the existing `error`-return API for callers that haven't migrated.

---

## 3. `cluster-cred` migration: `internal/util/credential.go`

**Replaces:** `sed -i ''` block in `justfile` lines 164–175.
**Fixes:** macOS-only `sed -i ''`, unsafe path characters in sed replacement string, no key-file validation.

### Before (idiomatic Go)

```go
package util

func SetCredential(envFile, keyPath string) error {
    absKey, err := filepath.Abs(keyPath)
    if err != nil { return fmt.Errorf("resolve key path: %w", err) }
    if _, err := os.Stat(absKey); err != nil {
        if os.IsNotExist(err) { return fmt.Errorf("key file not found: %s", absKey) }
        return fmt.Errorf("cannot read key file: %w", err)
    }
    content, err := os.ReadFile(envFile)
    if err != nil {
        if os.IsNotExist(err) { return fmt.Errorf("env file not found: %s", envFile) }
        return fmt.Errorf("read env file: %w", err)
    }
    prefix := "export GOOGLE_APPLICATION_CREDENTIALS="
    newLine := prefix + absKey
    lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
    found := false
    for i, line := range lines {
        if strings.HasPrefix(strings.TrimSpace(line), prefix) { lines[i] = newLine; found = true; break }
    }
    if !found { lines = append(lines, newLine) }
    output := strings.Join(lines, "\n") + "\n"
    if err := os.WriteFile(envFile, []byte(output), 0o644); err != nil { return fmt.Errorf("write env file: %w", err) }
    return nil
}
```

### After (fp-go v2)

```go
package util

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"

    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
)

const credentialPrefix = "export GOOGLE_APPLICATION_CREDENTIALS="

func SetCredential(envFile, keyPath string) error {
    return F.Pipe4(
        resolveKeyPath(keyPath),
        IOE.Chain(checkKeyFile),
        IOE.Chain(readAndUpdateEnv(envFile)),
        IOE.Chain(writeEnvFile(envFile)),
        func(effect IOE.IOEither[error, string]) error {
            return E.Fold(
                F.Identity[error],
                func(string) error { return nil },
            )(effect())
        },
    )
}

func resolveKeyPath(keyPath string) IOE.IOEither[error, string] {
    return IOE.TryCatchError(func() (string, error) {
        abs, err := filepath.Abs(keyPath)
        if err != nil { return "", fmt.Errorf("resolve key path: %w", err) }
        return abs, nil
    })
}

func checkKeyFile(absKey string) IOE.IOEither[error, string] {
    return IOE.TryCatchError(func() (string, error) {
        _, err := os.Stat(absKey)
        switch {
        case os.IsNotExist(err):
            return "", fmt.Errorf("key file not found: %s", absKey)
        case err != nil:
            return "", fmt.Errorf("cannot read key file: %w", err)
        default:
            return absKey, nil
        }
    })
}

func readAndUpdateEnv(envFile string) func(string) IOE.IOEither[error, string] {
    return func(absKey string) IOE.IOEither[error, string] {
        return IOE.TryCatchError(func() (string, error) {
            content, err := os.ReadFile(envFile)
            switch {
            case os.IsNotExist(err):
                return "", fmt.Errorf("env file not found: %s", envFile)
            case err != nil:
                return "", fmt.Errorf("read env file: %w", err)
            }
            newLine := credentialPrefix + absKey
            trimmed := strings.TrimRight(string(content), "\n")
            var lines []string
            if trimmed != "" { lines = strings.Split(trimmed, "\n") }
            replaced := false
            for i, line := range lines {
                if strings.HasPrefix(strings.TrimSpace(line), credentialPrefix) {
                    lines[i] = newLine; replaced = true; break
                }
            }
            if !replaced { lines = append(lines, newLine) }
            return strings.Join(lines, "\n") + "\n", nil
        })
    }
}

func writeEnvFile(envFile string) func(string) IOE.IOEither[error, string] {
    return func(output string) IOE.IOEither[error, string] {
        return IOE.TryCatchError(func() (string, error) {
            if err := os.WriteFile(envFile, []byte(output), 0o644); err != nil {
                return "", fmt.Errorf("write env file: %w", err)
            }
            return output, nil
        })
    }
}
```

**Rationale:** Each fallible OS call becomes an IOEither step composed with `F.Pipe4`. File validation, reading, line replacement, and writing are separate named functions — composable and testable independently. The public API stays `error`-return for backward compatibility; the IOEither is folded at the boundary.

**CLI wiring** (in `main.go`'s `envCommand()`):

```go
&cli.Command{
    Name:      "set-cred",
    Usage:     "Set GOOGLE_APPLICATION_CREDENTIALS in a per-cluster env file",
    ArgsUsage: "<env> <cluster> <key-path>",
    Action: func(cltx *cli.Context) error {
        if cltx.NArg() != 3 {
            return fmt.Errorf("usage: cluster-ops env set-cred <env> <cluster> <key-path>")
        }
        envFile := fmt.Sprintf(".env.%s.%s", cltx.Args().Get(0), cltx.Args().Get(1))
        return util.SetCredential(envFile, cltx.Args().Get(2))
    },
},
```

**Test** (`internal/util/credential_test.go`):
- Table-driven: create temp env file → call SetCredential → verify line written → call again with different key → verify line replaced → call with non-existent key → verify error → call with non-existent env file → verify error.

---

## 4. `update-cluster` migration: `internal/kops/update.go`

**Replaces:** 14-line shell version-detection block in `cluster.justfile` lines 70–83.
**Fixes:** Silent fallback to `"0.0.0"` on parse failure, no user warning.

### Before (idiomatic Go)

```go
func detectKopsVersion() (string, error) {
    cmd := exec.Command("kops", "version", "--short")
    out, err := cmd.Output()
    if err != nil { return "", fmt.Errorf("kops version --short: %w", err) }
    return strings.TrimSpace(string(out)), nil
}
func UpdateCluster(cltx *cli.Context) error {
    version, err := detectKopsVersion()
    if err != nil { return fmt.Errorf("cannot detect kOps version: %w", err) }
    v, err := semver.Make("v" + version)
    if err != nil { return fmt.Errorf("kops version %q is not valid semver: %w", version, err) }
    v131 := semver.MustParse("1.31.0")
    if v.GTE(v131) { return runKops("reconcile", "cluster", "--yes") }
    return runKops("update", "cluster", "--yes", "--admin")
}
```

### After (fp-go v2)

```go
package kops

import (
    "fmt"
    "os/exec"
    "strings"

    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
    "github.com/blang/semver"
)

var kopsV131 = semver.MustParse("1.31.0")

func UpdateCluster(cltx *cli.Context) error {
    return F.Pipe3(
        detectKopsVersionIOE(),
        IOE.ChainEitherK(parseSemver),
        IOE.Chain(selectKopsCommand),
        func(effect IOE.IOEither[error, string]) error {
            return E.Fold(
                F.Identity[error],
                func(string) error { return nil },
            )(effect())
        },
    )
}

func detectKopsVersionIOE() IOE.IOEither[error, string] {
    return IOE.TryCatchError(func() (string, error) {
        out, err := exec.Command("kops", "version", "--short").Output()
        if err != nil { return "", fmt.Errorf("kops version --short: %w", err) }
        return strings.TrimSpace(string(out)), nil
    })
}

func parseSemver(version string) E.Either[error, semver.Version] {
    v, err := semver.Make("v" + version)
    if err != nil {
        return E.Left[semver.Version](fmt.Errorf("kops version %q is not valid semver: %w", version, err))
    }
    return E.Right[error](v)
}

func selectKopsCommand(v semver.Version) IOE.IOEither[error, string] {
    if v.GTE(kopsV131) { return runKopsIOE("reconcile", "cluster", "--yes") }
    return runKopsIOE("update", "cluster", "--yes", "--admin")
}
```

**Rationale:** Version detection becomes a lazy IOEither. Semver parsing is pure Either, chained via `IOE.ChainEitherK`. Version dispatch uses `IOE.Chain` with the package-level `kopsV131` constant.

**CLI wiring:**

```go
&cli.Command{
    Name:   "update",
    Usage:  "Apply pending cluster changes (version-aware kOps dispatch)",
    Action: kops.UpdateCluster,
    // No flags — kOps reads KOPS_CLUSTER_NAME, KOPS_STATE_STORE from env
},
```

**Test** (`internal/kops/update_test.go`):
- Table-driven: input version string → expected command (`reconcile` vs `update`)
- Edge cases: empty output, non-semver output, trailing newlines

---

## 5. `delete-cluster` migration: `internal/kops/delete.go`

**Replaces:** 74-line shell block in `cluster.justfile` (preflight + dry-run + confirm + execute + 4 gcloud verifications).
**Key design change:** Derive expected resources from kOps dry-run output instead of name-pattern heuristics.

### Before (idiomatic Go)

```go
func DeleteCluster(cltx *cli.Context) error {
    name := cltx.String("cluster-name")
    state := cltx.String("state")
    dryRun := !cltx.Bool("yes")

    dryRunOut, err := runKopsCapture("delete", "cluster",
        "--name="+name, "--state="+state)
    if err != nil { return fmt.Errorf("kops delete dry-run failed: %w", err) }
    if dryRun {
        fmt.Println(dryRunOut)
        return nil
    }

    showRunningWorkloads()
    if !cltx.Bool("non-interactive") {
        fmt.Print("Type 'destroy' to confirm: ")
        s := bufio.NewScanner(os.Stdin); s.Scan()
        if s.Text() != "destroy" { return fmt.Errorf("aborted") }
    }

    if err := runKops("delete", "cluster",
        "--name="+name, "--state="+state, "--yes"); err != nil {
        return err
    }
    return verifyCleanup(cltx, dryRunOut)
}
```

### After (fp-go v2)

```go
package kops

import (
    "bufio" "fmt" "os" "os/exec" "strings"

    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
    "github.com/urfave/cli/v2"
)

func DeleteCluster(cltx *cli.Context) error {
    name := cltx.String("cluster-name")
    state := cltx.String("state")
    project := cltx.String("project-id")
    dryRun := !cltx.Bool("yes")

    dryRunOut, err := runKopsCapture("delete", "cluster",
        "--name="+name, "--state="+state)
    if err != nil { return fmt.Errorf("kops delete dry-run failed: %w", err) }
    if dryRun {
        fmt.Println(dryRunOut)
        return nil
    }
    return executeTeardown(cltx, name, state, project, dryRunOut)
}

func executeTeardown(cltx *cli.Context, name, state, project, dryRunOut string) error {
    effect := F.Pipe2(
        IOE.TryCatchError(func() (string, error) {
            return "", confirmTeardown(cltx)
        }),
        IOE.Chain(func(_ string) IOE.IOEither[error, string] {
            return runKopsIOE("delete", "cluster",
                "--name="+name, "--state="+state, "--yes")
        }),
        IOE.Chain(func(_ string) IOE.IOEither[error, string] {
            return verifyTeardownCleanup(project, dryRunOut)
        }),
    )
    return E.Fold(
        F.Identity[error],
        func(string) error { return nil },
    )(effect())
}
```

**Rationale:** The dry-run/evaluate phase stays imperative (it's a branch decision). The execute phase becomes an IOEither pipeline: confirmation → destroy → verify, composed with `F.Pipe2` and `IOE.Chain`. The public API preserves the `error`-return signature. The `parseDryRunOutput`/`resourceStillExists` helpers (not shown — see source) handle GCP resource verification behind the IOEither boundary.

**CLI flags:**

```go
[]cli.Flag{
    &cli.StringFlag{
        Name: "cluster-name", Aliases: []string{"c"},
        EnvVars: []string{"KOPS_CLUSTER_NAME"}, Required: true,
    },
    &cli.StringFlag{
        Name: "state", Aliases: []string{"s"},
        EnvVars: []string{"KOPS_STATE_STORE"}, Required: true,
    },
    &cli.StringFlag{
        Name: "project-id", Aliases: []string{"p"},
        EnvVars: []string{"PROJECT_ID"},
    },
    &cli.BoolFlag{
        Name: "yes",
        Usage: "Execute teardown (without this flag: dry-run only)",
    },
    &cli.BoolFlag{
        Name: "non-interactive",
        Usage: "Skip confirmation prompt (for CI)",
    },
},
```

**Test** (`internal/kops/delete_test.go`):
- Dry-run mode: verify kops is called WITHOUT `--yes`
- Execute mode: verify kops is called WITH `--yes`
- `parseDryRunOutput`: table-driven with sample kops dry-run text → expected resource list
- `resourceStillExists`: mock gcloud responses

---

## 6. `validate-kops-ha` migration: `internal/custodian/validate.go`

**Replaces:** 60-line shell script with 7 `kubectl` + `grep` checks.
**Leverages:** Existing `client-go` dependency (already imported by custodian).

### Before (idiomatic Go — showing first 2 of 7 identical patterns)

```go
func checkClusterAutoscaler(ctx context.Context, cs *kubernetes.Clientset) HACheck {
    pods, err := cs.CoreV1().Pods("kube-system").List(ctx,
        metav1.ListOptions{LabelSelector: "app=cluster-autoscaler"})
    if err != nil { return HACheck{"cluster-autoscaler", "error", err.Error()} }
    if len(pods.Items) == 0 { return HACheck{"cluster-autoscaler", "fail", "no pods found"} }
    for _, p := range pods.Items {
        if p.Status.Phase != "Running" {
            return HACheck{"cluster-autoscaler", "fail",
                fmt.Sprintf("pod %s is %s", p.Name, p.Status.Phase)}
        }
    }
    return HACheck{"cluster-autoscaler", "pass",
        fmt.Sprintf("%d pods running", len(pods.Items))}
}
func checkNodeProblemDetector(ctx context.Context, cs *kubernetes.Clientset) HACheck {
    ds, err := cs.AppsV1().DaemonSets("kube-system").Get(ctx,
        "node-problem-detector", metav1.GetOptions{})
    if err != nil { return HACheck{"node-problem-detector", "fail", "daemonset not found"} }
    if ds.Status.DesiredNumberScheduled == 0 {
        return HACheck{"node-problem-detector", "fail", "no nodes scheduled"}
    }
    if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled {
        return HACheck{"node-problem-detector", "warn",
            fmt.Sprintf("%d/%d ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)}
    }
    return HACheck{"node-problem-detector", "pass",
        fmt.Sprintf("%d pods ready", ds.Status.NumberReady)}
}
```

### After (fp-go v2 — pattern repeated for all 7 checks)

```go
package custodian

import (
    "context" "fmt" "time"

    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
)

type HACheck struct {
    Name    string `json:"name"`
    Status  string `json:"status"`
    Message string `json:"message"`
}

func ValidateHA(kubeconfig string) ([]HACheck, error) {
    cs, _, err := createKubernetesClient(kubeconfig)
    if err != nil { return nil, fmt.Errorf("kubernetes client: %w", err) }
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    return []HACheck{
        checkClusterAutoscaler(ctx, cs),
        checkNodeProblemDetector(ctx, cs),
        checkMetricsServer(ctx, cs),
        checkCertManager(ctx, cs),
        checkNodeLocalDNS(ctx, cs),
        checkControlPlaneZoneSpread(ctx, cs),
        checkBatchNodeTaints(ctx, cs),
    }, nil
}

// Each check follows the same three-part pattern:
// 1. IOEither.TryCatchError wraps the client-go API call
// 2. E.Fold dispatches: Left = error HACheck, Right = validate function
// 3. validatePodsRunning / validateDaemonSet classify the result

func checkClusterAutoscaler(ctx context.Context, cs *kubernetes.Clientset) HACheck {
    return F.Pipe1(
        listPods(ctx, cs, "kube-system", "app=cluster-autoscaler"),
        E.Fold(
            func(err error) HACheck { return HACheck{"cluster-autoscaler", "error", err.Error()} },
            func(pods []corev1.Pod) HACheck { return validatePodsRunning("cluster-autoscaler", pods) },
        ),
    )
}

func checkNodeProblemDetector(ctx context.Context, cs *kubernetes.Clientset) HACheck {
    return F.Pipe1(
        getDaemonSet(ctx, cs, "kube-system", "node-problem-detector"),
        E.Fold(
            func(err error) HACheck { return HACheck{"node-problem-detector", "fail", "daemonset not found"} },
            func(ds *appsv1.DaemonSet) HACheck { return validateDaemonSet("node-problem-detector", ds) },
        ),
    )
}

// ... remaining 5 checks follow the identical pattern ...

// Shared helpers — used by all check functions
func listPods(ctx context.Context, cs *kubernetes.Clientset, ns, sel string) E.Either[error, []corev1.Pod] {
    return IOE.TryCatchError(func() ([]corev1.Pod, error) {
        pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
        if err != nil { return nil, err }
        return pods.Items, nil
    })()
}

func getDaemonSet(ctx context.Context, cs *kubernetes.Clientset, ns, name string) E.Either[error, *appsv1.DaemonSet] {
    return IOE.TryCatchError(func() (*appsv1.DaemonSet, error) {
        return cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
    })()
}

func validatePodsRunning(name string, pods []corev1.Pod) HACheck {
    if len(pods) == 0 { return HACheck{name, "fail", "no pods found"} }
    for _, p := range pods {
        if p.Status.Phase != corev1.PodRunning {
            return HACheck{name, "fail", fmt.Sprintf("pod %s is %s", p.Name, p.Status.Phase)}
        }
    }
    return HACheck{name, "pass", fmt.Sprintf("%d pods running", len(pods))}
}

func validateDaemonSet(name string, ds *appsv1.DaemonSet) HACheck {
    if ds.Status.DesiredNumberScheduled == 0 { return HACheck{name, "fail", "no nodes scheduled"} }
    if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled {
        return HACheck{name, "warn", fmt.Sprintf("%d/%d ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)}
    }
    return HACheck{name, "pass", fmt.Sprintf("%d pods ready", ds.Status.NumberReady)}
}
```

**Rationale:** Each check follows a uniform three-part pattern: (1) acquire data via `IOE.TryCatchError` wrapped client-go calls, (2) fold the Either with `E.Fold` dispatching errors to error-status HAChecks, (3) validate with shared `validatePodsRunning`/`validateDaemonSet` helpers. The 5 remaining checks (metrics-server, cert-manager, node-local-dns, control-plane-zones, batch-taints) follow identically.

**CLI wiring:**

```go
&cli.Command{
    Name:  "ha",
    Usage: "Validate HA production topology",
    Flags: []cli.Flag{
        &cli.StringFlag{
            Name: "kubeconfig", Aliases: []string{"k"},
            EnvVars: []string{"KUBECONFIG"},
        },
        &cli.BoolFlag{
            Name: "json",
            Usage: "Output results as JSON (default: human-readable table)",
        },
    },
    Action: func(cltx *cli.Context) error {
        checks, err := custodian.ValidateHA(cltx.String("kubeconfig"))
        if err != nil {
            return err
        }
        if cltx.Bool("json") {
            return json.NewEncoder(os.Stdout).Encode(checks)
        }
        printCheckTable(checks)
        return nil
    },
},
```

**Test** (`internal/custodian/validate_test.go`):
- Use fake clientset to simulate: no pods, some pods not ready, all pods ready
- Verify correct status per scenario
- Test all 7 check functions independently

---

## 7. `create-state-bucket` migration: modify `internal/gcp/kops_state_bucket.go`

**Add to the existing `CreateKopsStateBucket` function:** UBLA and PAP hardening.
Currently the Go code handles: create bucket, enable versioning, enable soft-delete, set lifecycle.
The shell recipe additionally handles: uniform bucket-level access, public access prevention.

**New functions:**

```go
func enableUniformBucketLevelAccess(ctx context.Context, bucket *storage.BucketHandle) error {
    _, err := bucket.Update(ctx, storage.BucketAttrsToUpdate{
        UniformBucketLevelAccess: &storage.UniformBucketLevelAccess{
            Enabled: true,
        },
    })
    if err != nil {
        return fmt.Errorf("enable UBLA: %w", err)
    }
    return nil
}

func enablePublicAccessPrevention(ctx context.Context, bucket *storage.BucketHandle) error {
    _, err := bucket.Update(ctx, storage.BucketAttrsToUpdate{
        PublicAccessPrevention: storage.PublicAccessPreventionEnforced,
    })
    if err != nil {
        return fmt.Errorf("enable PAP: %w", err)
    }
    return nil
}
```

**Integrate into `setupNewBucket`** (and the "already exists" branch in `CreateKopsStateBucket`):

```go
func setupNewBucket(ctx context.Context, params CreateBucketParams, bucket *storage.BucketHandle) error {
    if err := createBucket(params); err != nil {
        return err
    }
    if err := enableBucketVersioning(ctx, bucket); err != nil {
        return err
    }
    if err := enableSoftDelete(ctx, bucket); err != nil {
        return err
    }
    if err := enableUniformBucketLevelAccess(ctx, bucket); err != nil {
        return err
    }
    if err := enablePublicAccessPrevention(ctx, bucket); err != nil {
        return err
    }
    return nil
}
```

**Add `--harden` flag** to `findOrCreateKopsBucketCommand` (default `true`). When `false`, skip hardening steps. This allows the recipe to control whether hardening is applied.

**CLI wiring** (in `main.go`'s `bucketCommand()`):

```go
&cli.Command{
    Name:  "create",
    Usage: "Create and harden a GCS state bucket for kOps",
    Flags: bucketFlags(),  // reuse existing flags from gcp-tools + --harden
    Action: gcp.CreateKopsStateBucket,
},
```

---

## 8. `apply-instancegroups` migration: `internal/kops/instancegroup.go`

**Replaces:** Template processing loop with `envsubst` in `cluster.justfile` lines 133–148.
**Adds:** Pre-validation (YAML parse, unresolved var detection), dry-run mode.
**Preserves:** `${VAR:-default}` bash syntax via `exec.Command("envsubst", ...)`.

### Before (idiomatic Go)

```go
func ApplyInstanceGroups(cltx *cli.Context) error {
    templateDir := cltx.String("template-dir")
    dryRun := cltx.Bool("dry-run")
    entries, err := os.ReadDir(templateDir)
    if err != nil { return fmt.Errorf("template dir %s: %w", templateDir, err) }
    for _, e := range entries {
        if !strings.HasSuffix(e.Name(), ".yaml.tmpl") { continue }
        path := filepath.Join(templateDir, e.Name())
        name := filepath.Base(path)
        slog.Info("Processing template", "file", name)
        raw, _ := os.ReadFile(path)
        unresolved := findUnresolvedVars(string(raw))
        if len(unresolved) > 0 {
            slog.Warn("template references env vars not set", "file", name, "vars", unresolved)
        }
        if dryRun {
            rendered, err := renderTemplate(path)
            if err != nil { return fmt.Errorf("%s: render: %w", name, err) }
            fmt.Printf("--- Would apply: %s ---\n%s\n", name, rendered)
            continue
        }
        if err := renderAndApply(path, name); err != nil { return err }
    }
    return nil
}
```

### After (fp-go v2)

```go
package kops

import (
    "fmt" "os" "os/exec" "path/filepath" "regexp" "strings"

    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
    "github.com/urfave/cli/v2"
)

var templateVarPattern = regexp.MustCompile(`\$\{(\w+)(?::-([^}]*))?\}`)

func ApplyInstanceGroups(cltx *cli.Context) error {
    templateDir := cltx.String("template-dir")
    if templateDir == "" { templateDir = "config/kops/instancegroups" }
    dryRun := cltx.Bool("dry-run")
    tmplFiles, err := collectTemplates(templateDir)
    if err != nil { return err }
    return F.Pipe1(
        processTemplates(tmplFiles, dryRun),
        func(effect IOE.IOEither[error, string]) error {
            return E.Fold(
                F.Identity[error],
                func(string) error { return nil },
            )(effect())
        },
    )
}

func processTemplates(files []string, dryRun bool) IOE.IOEither[error, string] {
    return IOE.TryCatchError(func() (string, error) {
        for _, path := range files {
            name := filepath.Base(path)
            raw, _ := os.ReadFile(path)
            unresolved := findUnresolvedVars(string(raw))
            if len(unresolved) > 0 {
                fmt.Fprintf(os.Stderr, "WARNING: %s references env vars not set: %v\n", name, unresolved)
            }
            if dryRun {
                rendered, err := renderTemplate(path)
                if err != nil { return "", fmt.Errorf("%s: render: %w", name, err) }
                fmt.Printf("--- Would apply: %s ---\n%s\n", name, rendered)
                continue
            }
            if err := renderAndApply(path); err != nil { return "", fmt.Errorf("%s: %w", name, err) }
        }
        return "done", nil
    })
}
```

**Rationale:** Template collection and processing are wrapped in IOEither with `TryCatchError`. The `findUnresolvedVars` function remains pure (no IO — just regex + env lookup). The public API preserves `error`-return; IOEither is folded at the boundary.

**CLI flags:**

```go
[]cli.Flag{
    &cli.StringFlag{
        Name: "template-dir", Aliases: []string{"d"},
        Value: "config/kops/instancegroups",
        Usage: "Directory containing .yaml.tmpl templates",
    },
    &cli.BoolFlag{
        Name: "dry-run",
        Usage: "Render and validate templates without applying",
    },
},
```

**Test** (`internal/kops/instancegroup_test.go`):
- `findUnresolvedVars`: table-driven (template string → expected unresolved vars)
- `renderTemplate`: with mock envsubst
- Dry-run mode: verify no kops replace calls made

---

## 9. `recreate-cluster` migration: `internal/kops/recreate.go`

**Replaces:** 40-line shell pipeline in `cluster.justfile`.
**Fixes:** Diff exit code conflation (exit 1 "differences found" vs exit 2 "error" both show "Differences found").

### Before (idiomatic Go)

```go
func stepDiffManifest(cltx *cli.Context) error {
    saved := cltx.String("manifest")
    if saved == "" { saved = "config/kops/cluster-manifest.yaml" }
    if _, err := os.Stat(saved); os.IsNotExist(err) {
        fmt.Printf("No saved manifest — skipping.\n"); return nil
    }
    live, err := runKopsCapture("get", "cluster", "-o", "yaml")
    if err != nil { return fmt.Errorf("get live manifest: %w", err) }
    tmp, _ := os.CreateTemp("", "cluster-manifest-*.yaml")
    defer os.Remove(tmp.Name())
    tmp.WriteString(live); tmp.Close()
    cmd := exec.Command("diff", saved, tmp.Name())
    cmd.Stdout = os.Stdout; cmd.Stderr = os.Stderr
    err = cmd.Run()
    if exitErr, ok := err.(*exec.ExitError); ok {
        if exitErr.ExitCode() == 1 { fmt.Println("Differences found."); return nil }
        return fmt.Errorf("diff failed: %w", err)
    }
    fmt.Println("No differences."); return nil
}
```

### After (fp-go v2)

```go
package kops

import (
    "fmt" "os" "os/exec" "time"

    E "github.com/IBM/fp-go/v2/either"
    F "github.com/IBM/fp-go/v2/function"
    IOE "github.com/IBM/fp-go/v2/ioeither"
    "github.com/urfave/cli/v2"
)

var defaultManifestPath = "config/kops/cluster-manifest.yaml"

func stepDiffManifest(cltx *cli.Context) error {
    saved := manifestPath(cltx)
    return F.Pipe1(
        checkManifestExists(saved),
        E.Fold(
            func(err error) error {
                if os.IsNotExist(err) {
                    fmt.Printf("No saved manifest at %s — skipping diff.\n", saved)
                    return nil
                }
                return fmt.Errorf("stat manifest: %w", err)
            },
            func(_ os.FileInfo) error { return runDiff(saved) },
        ),
    )
}

func runDiff(saved string) error {
    return F.Pipe2(
        captureAndWriteTemp(saved),
        E.Chain(func(tmpPath string) E.Either[error, string] {
            defer os.Remove(tmpPath)
            return executeDiffIOE(saved, tmpPath)()
        }),
        E.Fold(F.Identity[error], func(string) error { return nil }),
    )
}

func executeDiff(saved, tmpPath string) error {
    cmd := exec.Command("diff", saved, tmpPath)
    cmd.Stdout = os.Stdout; cmd.Stderr = os.Stderr
    err := cmd.Run()
    if err == nil {
        fmt.Println("No differences — manifest matches saved copy.")
        return nil
    }
    if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
        fmt.Println("Differences found. Review the diff above before editing.")
        return nil
    }
    return fmt.Errorf("diff failed: %w", err)
}
```

**Rationale:** Manifest path resolution uses Option for default fallback. The diff pipeline separates concerns: capture temp, execute diff, cleanup temp. Exit code 1 (differences found) is handled correctly — not conflated with real errors (exit code 2+). The `RecreateCluster` orchestration function (not shown) iterates 6 steps with timing, calling each sub-function via the established error-return API.

---

## 10. Justfile changes

A new `build` recipe compiles the unified binary once:

```just
[group('setup-tools')]
build:
    go build -o bin/cluster-ops ./cmd/cluster-ops
```

Each migrated recipe becomes a thin wrapper — no shell logic remains:

```just
# Before (14 lines of shell):
# update-cluster:
#     #!/usr/bin/env bash
#     set -euo pipefail
#     KOPS_VERSION=$(kops version --short ...)
#     ... 10 more lines of version comparison ...

# After:
[no-cd]
update-cluster: build
    ./bin/cluster-ops kops update

[no-cd]
delete-cluster confirm="no": build
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{ confirm }}" = "yes" ]; then
        ./bin/cluster-ops kops delete --yes
    else
        ./bin/cluster-ops kops delete
    fi

[no-cd]
save-cluster-manifest: build
    ./bin/cluster-ops kops save-manifest

[no-cd]
diff-cluster-manifest: build
    ./bin/cluster-ops kops diff-manifest

[no-cd]
recreate-cluster: build
    ./bin/cluster-ops kops recreate

[no-cd]
validate-kops-ha: build
    ./bin/cluster-ops validate ha

[no-cd]
create-cluster-config project bucket_name: build
    ./bin/cluster-ops kops create-config \
        --project-id={{ project }}

[no-cd]
create-state-bucket project bucket_name: build
    ./bin/cluster-ops bucket create \
        --project={{ project }} \
        --bucket={{ bucket_name }} \
        --harden

[no-cd]
apply-instancegroups: build
    ./bin/cluster-ops ig apply
```

Top-level `justfile` changes:

```just
# Before:
# cluster-cred env cluster key:
#     ... sed -i '' ... (macOS-only, unsafe substitution)

# After:
[group('cluster-ops')]
cluster-cred env cluster key: build
    ./bin/cluster-ops env set-cred {{ env }} {{ cluster }} {{ key }}
```

`set-env-var` is already backed by the `util` binary. Migrate to unified CLI:

```just
set-env-var name value: build
    ./bin/cluster-ops env set-var --name={{ name }} --value={{ value }}
```

---

## 11. Deletion of old entrypoints

After the unified CLI is fully wired and all just recipes cut over:

| Delete | Lines | Reason |
|--------|-------|--------|
| `cmd/gcp/main.go` | ~300 | `cluster-ops` imports `internal/gcp` directly |
| `cmd/kops/main.go` | ~35 | `cluster-ops` imports `internal/kops` directly |
| `cmd/util/main.go` | ~50 | `cluster-ops` imports `internal/util` directly |
| `cmd/custodian/main.go` | ~140 | `cluster-ops` imports `internal/custodian` directly |

The `internal/*` packages stay. Only the `cmd/*/main.go` wrappers are removed. If standalone binaries are still needed, keep them as thin passthroughs to `internal/*`.

---

## 12. File change summary

| Action | File | ~Lines |
|--------|------|--------|
| **NEW** | `cmd/cluster-ops/main.go` | ✅ Done — unified entrypoint with kops/validate/bucket/ig/env subcommands |
| **NEW** | `internal/kops/exec.go` | ✅ Done — fp-go lazy IOEither wrappers + backward-compat fold |
| **NEW** | `internal/kops/update.go` | ✅ Done — fp-go version-aware dispatch |
| **NEW** | `internal/kops/update_test.go` | Pending |
| **NEW** | `internal/util/credential.go` | ✅ Done — fp-go credential pipeline (`F.Pipe4`) |
| **NEW** | `internal/util/credential_test.go` | Pending |
| **NEW** | `internal/kops/delete.go` | ✅ Done — fp-go teardown pipeline |
| **NEW** | `internal/kops/delete_test.go` | Pending |
| **NEW** | `internal/custodian/validate.go` | ✅ Done — fp-go HA validation (7 checks) |
| **NEW** | `internal/custodian/validate_test.go` | Pending |
| **NEW** | `internal/kops/instancegroup.go` | ✅ Done — template processing |
| **NEW** | `internal/kops/instancegroup_test.go` | Pending |
| **NEW** | `internal/kops/recreate.go` | ✅ Done — fp-go diff pipeline |
| **NEW** | `internal/kops/recreate_test.go` | Pending |
| **MODIFY** | `internal/gcp/kops_state_bucket.go` | ✅ Done — UBLA + PAP hardening with --harden flag |
| **MODIFY** | `internal/kops/action.go` | ✅ Done — refactored to fp-go (`F.Pipe2`, `IOE.ChainFirstIOK`, `IOE.Fold`), uses `runKopsIOE` from exec.go |
| **MODIFY** | `just_modules/cluster.justfile` | -200 +50 |
| **MODIFY** | `justfile` | -15 +10 |
| **DELETE** | `cmd/gcp/main.go` | -300 |
| **DELETE** | `cmd/kops/main.go` | -35 |
| **DELETE** | `cmd/util/main.go` | -50 |
| **DELETE** | `cmd/custodian/main.go` | -140 |

**Total: ~1,290 new lines, ~525 deleted lines, ~45 modified lines. Net: +810 lines.**

---

## 13. Quality standards

- **`slog` throughout** — no `fmt.Println` for operational output (only user-facing tables/prompts)
- **`%w` error wrapping** — every error preserves the chain for `errors.Is` / `errors.As`
- **`urfave/cli` flags** — every flag has `Name`, `Aliases`, `Usage`, `EnvVars`, `Value`/`Required`
- **Idempotency** — GCP operations check current state before applying (bucket hardening, SA creation)
- **Testability** — exec-based functions accept mockable interfaces; client-go operations use fake clientsets
- **JSON output** — `--json` flag on `validate ha` and `kops delete` for CI consumption
- **Cross-platform** — no `sed -i`, no platform-specific flags. `filepath` for path operations.

---

## 14. Execution order

Each step produces an independently testable Go function. Steps 1–8 can be reviewed/merged incrementally.

1. Create `cmd/cluster-ops/main.go` with all subcommands wired (skeleton)
2. Migrate `cluster-cred` → `internal/util/credential.go` (simplest, highest payoff)
3. Migrate `update-cluster` → `internal/kops/update.go` (low effort, all recipes depend)
4. Migrate `delete-cluster` → `internal/kops/delete.go` (medium effort)
5. Migrate `validate-kops-ha` → `internal/custodian/validate.go` (medium effort, client-go already available)
6. Add UBLA/PAP to `internal/gcp/kops_state_bucket.go` (low effort)
7. Migrate `apply-instancegroups` → `internal/kops/instancegroup.go` (medium effort)
8. Migrate `recreate-cluster` → `internal/kops/recreate.go` (after 1–7, becomes thin orchestration)
9. Update all just recipes to call `./bin/cluster-ops`
10. Delete old `cmd/*/main.go` entrypoints
11. Update documentation to reference new binary
