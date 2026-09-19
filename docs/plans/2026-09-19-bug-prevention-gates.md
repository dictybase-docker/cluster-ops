# Bug-Prevention Mechanical Gates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mechanically prevent the eight recurring bug classes found in the 32 `fix:` commits between 2026-09-02 and 2026-09-19 (ambient env leakage, identifier mismatches, name drift, non-idempotent reruns, platform assumptions, non-fail-closed destructive ops, secret misclassification/injection, long-workflow lifecycle).

**Architecture:** A Go linter (`cmd/lint-recipes` + `internal/recipeslint`) parses `just --dump --dump-format json` — never scraping raw justfiles — and applies precision-first rules to every recipe body (root + all modules), with a unit-tested pure core. A composite `just check` gate aggregates every daemon-free check, CI runs it on every PR, contract-test extensions assert destructive-recipe ordering, and a canonical checklist lands in `AGENTS.md`.

**Tech Stack:** Go 1.26 (stdlib + `github.com/IBM/fp-go/v2` + `urfave/cli/v2`), `just` ≥1.46 (`--dump --dump-format json` verified working), GitHub Actions, bash contract-test scripts using `just --dry-run`.

**Spec:** This plan is self-contained; the "Background" section below is the spec (the bug-class synthesis it implements).

## Global Constraints

- `just check` must run with zero cloud credentials, zero Docker daemon, zero live cluster access (dry-run and static checks only). Dagger-dependent `just test` stays out.
- Lint rules are precision-first: a rule that false-positives twice gets an inline `# lint-recipes:allow-<rule-id>` suppression on the offending line, never a central allowlist file. Rules are removed, not loosened, if they stay noisy.
- ALL new Go code follows the repo's fp-go house style: Option/Either/IO/IOEither allowlist only, raw calls inside `IOE.TryCatchError`, errors wrapped with `%w` via `IOE.MapLeft`, pipeline state carried on a state struct, composition via `F.PipeN` — mirroring `internal/gcp/kms.go`. No nested `f(g(x))` calls and no `combinator(args)(value)` direct applies outside a Pipe.
- The linter runs as `go run ./cmd/lint-recipes [target-dir]` — no new binary artifacts, no install step.
- All new shell scripts are bash, `set -euo pipefail` (or `set -u` for linters that count failures), and follow `scripts/docs-lint.sh` structure.
- Docs edits follow `docs/STYLE.md`. The new `just check` recipe gets one section in `docs/reference/` (see Task 5); guides are NOT touched (STYLE rule 2 — no new guide section for a dev-tools recipe).
- Every task ends with the repo's standard gates passing: `just check` (after Task 2), `golangci-lint fmt && golangci-lint run ./...`, `go test ./...`, and `gopls check` on every touched `.go` file.

## Background (the spec this plan implements)

Eight bug classes distilled from 32 fix commits:

1. Ambient env/credential leakage (`GOOGLE_CLOUD_PROJECT` fallback → wrong-project 403: ea7c1bb, fd42938).
2. Exact-identifier mismatches (`mktemp` without X's: d4d5d99; `--stack prod`: d0e75cc; gcloud format paths: 07f54d9).
3. Name drift between two sources of truth (PVC vs claimName: d1b7f82; autonamed Helm release: bd84577).
4. Non-idempotent reruns (`gcloud create || echo "already exists"`: 0dc61ab).
5. K8s/GCE platform assumptions (requests > allocatable: fab3b44; taints: d1ce7ec).
6. Destructive ops not fail-closed (postgres logical restore: 9 commits 4b33549→73659d5).
7. Secret misclassification + shell injection (Pulumi secret heuristic: 2e17b37, 236d76b; justfile `quote()`: 0509191).
8. Long-workflow lifecycle (dropped port-forward: 4ae1c91).

Known-live findings the new linter will catch immediately (verified 2026-09-19): `just_modules/arangodb.justfile` lines 183, 257, 258, 301, 302, 348, 349, 454 use `mktemp -t <name>` with no `X` suffix — same macOS-portability bug class as d4d5d99, still unfixed. Task 1 includes fixing them.

**Deferred by design** (recorded, not implemented): PR template (panel: click-through friction for solo repo), nightly `pulumi preview` (credential cost/noise; revisit on first drift incident), PVC-vs-claimName scanner, StackReference ID freeze file, pi `cluster-ops` skill (AGENTS.md is canonical per panel consensus). The `github.com/dictyBase/fp-go-loom` `pipelinecheck` gate is optional follow-up (Task 1 Step 8).

## Review Focus

- Interpolation flattening: a recipe body line from `just --dump` is a list mixing literal strings and interpolation nodes; if flattening is wrong, the unquoted-interpolation rule flags nothing (silent no-op). Pinned by `TestFlatten` + `TestWalkFixture` (Task 1).
- Comment lines: rules must not fire on `#` comments (arangodb/redis mention `GOOGLE_CLOUD_PROJECT` only in comments today — `ambient-gcp-project` must not fire there). Pinned by `TestCodePart` + `TestCheckRecipeRules` (Task 1).
- `mktemp` without template (`mktemp`, `mktemp -d`, `mktemp -u`) is safe and must not be flagged; only explicit templates lacking `X{4}`. Pinned by `TestCheckMktemp`.
- Suppression escape hatch: `# lint-recipes:allow-<rule>` on the exact line must silence exactly that rule, not neighbors. Pinned by `TestSuppressions` (Task 1).
- Deterministic output: `just --dump` emits maps; `Walk` must sort findings or output order flakes. Pinned by `TestWalkFixture` (sorted comparison).
- `just check` in CI must not need asdf/gcloud/Docker; only go, just, bash.

---

### Task 1: `internal/recipeslint` package + `cmd/lint-recipes` + fixture tests

**Files:**
- Create: `internal/recipeslint/lint.go` — pure core: types, rules, walker
- Create: `internal/recipeslint/pipeline.go` — effects: dump acquisition, JSON parse, report, `Run`
- Create: `internal/recipeslint/lint_test.go` — unit tests for every rule + suppression
- Create: `internal/recipeslint/pipeline_test.go` — fixture integration via `just --dump`, repo self-check
- Create: `internal/recipeslint/testdata/Justfile` — fixture with one recipe per rule
- Create: `cmd/lint-recipes/main.go` — thin CLI
- Modify: `just_modules/arangodb.justfile` — fix eight live `mktemp -t` violations

**Interfaces:**
- Consumes: `just --dump --dump-format json` (verified working at repo root and per-directory).
- Produces: `recipeslint.Run(Config{Root string}) error` — prints `FAIL <namepath>:<line>  <rule>: <detail>` / `WARN <namepath>  <rule>: <detail>`, returns non-nil error on any fail-rule finding. Rule IDs: `mktemp-no-x`, `create-pipe-swallow`, `ambient-gcp-project`, `hardcoded-stack`, `unquoted-interp` (fail), `port-forward-no-trap` (warn). Task 2 wraps this in `just lint-recipes`.

- [ ] **Step 1: Write the fixture Justfile with known violations**

`internal/recipeslint/testdata/Justfile`:

```just
# Fixture for internal/recipeslint tests — never run these recipes.
# Each recipe name encodes the rule it exercises.

# FINE: bare mktemp without template is safe
mktemp-bare:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(mktemp)
    echo "$out"

# BAD mktemp-no-x: -t template without X suffix
mktemp-template-no-x:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(mktemp -t kubeconfig)
    echo "$out"

# GOOD: template with X suffix passes
mktemp-template-x:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(mktemp -t kubeconfig-XXXXXX)
    echo "$out"

# BAD create-pipe-swallow: gcloud create || echo
create-pipe-swallow:
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud iam service-accounts create sa-name --project="$PROJECT" || echo "already exists"

# GOOD: describe-then-create probe passes
create-probe:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! gcloud iam service-accounts describe sa-name --project="$PROJECT" >/dev/null 2>&1; then
        gcloud iam service-accounts create sa-name --project="$PROJECT"
    fi

# BAD ambient-gcp-project: bare $GOOGLE_CLOUD_PROJECT in code
ambient-gcp-project:
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud storage buckets list --project="$GOOGLE_CLOUD_PROJECT"

# GOOD: comment mentioning the variable is not code
ambient-gcp-comment:
    #!/usr/bin/env bash
    set -euo pipefail
    # pulumi-gcp falls back to $GOOGLE_CLOUD_PROJECT when gcp:project is unset
    echo ok

# BAD hardcoded-stack: literal --stack prod
hardcoded-stack:
    #!/usr/bin/env bash
    set -euo pipefail
    pulumi stack output bucketName --stack prod

# GOOD: $PULUMI_STACK passes
stack-from-env:
    #!/usr/bin/env bash
    set -euo pipefail
    pulumi stack output bucketName --stack "$PULUMI_STACK"

# BAD unquoted-interp: interpolation outside double quotes
unquoted-interp 'name':
    #!/usr/bin/env bash
    set -euo pipefail
    kubectl get cm {{name}}

# GOOD: quoted interpolation passes
quoted-interp 'name':
    #!/usr/bin/env bash
    set -euo pipefail
    kubectl get cm "{{ name }}"

# GOOD: single-rule suppression silences only its own line
suppressed-mktemp:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(mktemp -t kubeconfig)  # lint-recipes:allow-mktemp-no-x
    out2=$(mktemp -t gcs-creds)
    echo "$out $out2"
```

Expected findings (body line numbers count every body line including the shebang — line 3 is the violating line in single-command bodies, line 4 in the two-mktemp body). These live in the test, not a data file.

- [ ] **Step 2: Write the pure core**

`internal/recipeslint/lint.go`:

```go
// Package recipeslint implements mechanical checks for just recipes,
// mirroring scripts/docs-lint.sh. It parses `just --dump --dump-format json`
// (never scrapes raw justfiles) and checks every recipe body in the root
// justfile and all modules.
package recipeslint

import (
	"regexp"
	"sort"
	"strings"
)

// Rule identifies one lint rule. Fail rules block the run; the warn rule
// only reports.
type Rule string

const (
	RuleMktempNoX         Rule = "mktemp-no-x"
	RuleCreatePipeSwallow Rule = "create-pipe-swallow"
	RuleAmbientGCPProject Rule = "ambient-gcp-project"
	RuleHardcodedStack    Rule = "hardcoded-stack"
	RuleUnquotedInterp    Rule = "unquoted-interp"
	RulePortForwardNoTrap Rule = "port-forward-no-trap" // warn-only
)

// Finding is one rule violation: the recipe namepath, the 1-based body line,
// the rule, and the offending text.
type Finding struct {
	Recipe string
	Line   int
	Rule   Rule
	Detail string
}

// warn reports whether the finding is advisory only.
func (f Finding) warn() bool { return f.Rule == RulePortForwardNoTrap }

// dump mirrors the subset of `just --dump --dump-format json` the linter reads.
type dump struct {
	Recipes map[string]recipe `json:"recipes"`
	Modules map[string]dump   `json:"modules"`
}

// recipe mirrors one recipe entry from the dump.
type recipe struct {
	// Body holds each line as a list of literal strings and interpolation
	// nodes (nested arrays); see flatten.
	Body    [][]any `json:"body"`
	Shebang bool    `json:"shebang"`
}

// interpMarker replaces every {{...}} interpolation node when flattening a
// body line; it cannot appear in shell source, so matching is unambiguous.
const interpMarker = "\x00"

// flatten renders a dump-json body line, collapsing interpolation nodes to
// the marker.
func flatten(parts []any) string {
	var b strings.Builder
	for _, p := range parts {
		if s, ok := p.(string); ok {
			b.WriteString(s)
			continue
		}
		b.WriteString(interpMarker)
	}
	return b.String()
}

// codePart strips a trailing shell comment: a '#' outside both single and
// double quotes starts a comment that runs to end of line.
func codePart(line string) string {
	inDQ, inSQ := false, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			if !inSQ {
				inDQ = !inDQ
			}
		case '\'':
			if !inDQ {
				inSQ = !inSQ
			}
		case '#':
			if !inDQ && !inSQ && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				return line[:i]
			}
		}
	}
	return line
}

// interpOutsideQuotes reports whether an interpolation marker occurs while
// double-quote depth is zero. Interpolation inside single quotes still
// evaluates, so it is flagged too.
func interpOutsideQuotes(line string) bool {
	depth := 0
	for i := 0; i < len(line); i++ {
		if line[i] == interpMarker[0] {
			if depth == 0 {
				return true
			}
			continue
		}
		if line[i] == '"' && (i == 0 || line[i-1] != '\\') {
			depth = 1 - depth
		}
	}
	return false
}

var allowRe = regexp.MustCompile(`lint-recipes:allow-([a-z-]+)`)

// allows extracts inline suppression rule ids from a body line.
func allows(line string) map[Rule]bool {
	out := map[Rule]bool{}
	for _, m := range allowRe.FindAllStringSubmatch(line, -1) {
		out[Rule(m[1])] = true
	}
	return out
}

var (
	mktempRe     = regexp.MustCompile(`\bmktemp\s+(.*)`)
	tokenRe      = regexp.MustCompile(`"[^"]*"|\S+`)
	xSuffixRe    = regexp.MustCompile(`X{4}`)
	createPipeRe = regexp.MustCompile(`\bgcloud\b.*\bcreate\b.*\|\|\s*(echo|true)\b`)
	gcpProjectRe = regexp.MustCompile(`\$\{?GOOGLE_CLOUD_PROJECT\}?\b`)
	stackRe      = regexp.MustCompile(`--stack\s+['"]?(prod|dev|staging)\b`)
	trapRe       = regexp.MustCompile(`\btrap\b`)
)

// checkMktemp returns the offending template when a mktemp invocation uses an
// explicit template lacking an X{4} suffix (macOS/BSD mktemp fails on it).
// Bare mktemp / mktemp -d is safe.
func checkMktemp(code string) (string, bool) {
	m := mktempRe.FindStringSubmatch(code)
	if m == nil {
		return "", false
	}
	var toks []string
	for _, t := range tokenRe.FindAllString(m[1], -1) {
		if !strings.HasPrefix(t, "-") {
			toks = append(toks, t)
		}
	}
	if len(toks) == 0 {
		return "", false
	}
	last := toks[len(toks)-1]
	if xSuffixRe.MatchString(last) {
		return "", false
	}
	return last, true
}

// checkRecipe runs every rule over one recipe body. Line numbers are
// 1-based over the body (shebang included).
func checkRecipe(namepath string, r recipe) []Finding {
	var findings []Finding
	hasPF, hasTrap := false, false
	for i, parts := range r.Body {
		raw := flatten(parts)
		code := codePart(raw)
		if strings.Contains(code, "port-forward") {
			hasPF = true
		}
		if trapRe.MatchString(code) {
			hasTrap = true
		}
		alw := allows(raw)
		if !alw[RuleMktempNoX] {
			if tmpl, hit := checkMktemp(code); hit {
				findings = append(findings, Finding{
					Recipe: namepath, Line: i + 1, Rule: RuleMktempNoX, Detail: tmpl,
				})
			}
		}
		if !alw[RuleCreatePipeSwallow] &&
			createPipeRe.MatchString(code) && !strings.Contains(code, "describe") {
			findings = append(findings, Finding{
				Recipe: namepath, Line: i + 1, Rule: RuleCreatePipeSwallow, Detail: raw,
			})
		}
		if !alw[RuleAmbientGCPProject] && gcpProjectRe.MatchString(code) {
			findings = append(findings, Finding{
				Recipe: namepath, Line: i + 1, Rule: RuleAmbientGCPProject, Detail: raw,
			})
		}
		if !alw[RuleHardcodedStack] && stackRe.MatchString(code) {
			findings = append(findings, Finding{
				Recipe: namepath, Line: i + 1, Rule: RuleHardcodedStack, Detail: raw,
			})
		}
		if !alw[RuleUnquotedInterp] && r.Shebang && interpOutsideQuotes(code) {
			findings = append(findings, Finding{
				Recipe: namepath, Line: i + 1, Rule: RuleUnquotedInterp, Detail: raw,
			})
		}
	}
	if hasPF && !hasTrap {
		findings = append(findings, Finding{
			Recipe: namepath, Line: 1, Rule: RulePortForwardNoTrap,
			Detail: "recipe uses kubectl port-forward but has no trap/cleanup",
		})
	}
	return findings
}

// Walk collects findings across the root recipes and every module, sorted by
// recipe namepath, line, and rule for deterministic output (dump maps are
// unordered).
func Walk(d dump) []Finding {
	var findings []Finding
	var walk func(prefix string, d dump)
	walk = func(prefix string, d dump) {
		for name, r := range d.Recipes {
			findings = append(findings, checkRecipe(prefix+name, r)...)
		}
		for modName, mod := range d.Modules {
			walk(prefix+modName+":", mod)
		}
	}
	walk("", d)
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Recipe != b.Recipe {
			return a.Recipe < b.Recipe
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Rule < b.Rule
	})
	return findings
}
```

- [ ] **Step 3: Write the effects + entrypoint pipeline**

`internal/recipeslint/pipeline.go`:

```go
package recipeslint

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
)

// Config is the boundary input: the directory holding the justfile to lint.
type Config struct {
	Root string
}

// work carries the pipeline intermediates on a state struct.
type work struct {
	Cfg Config
}

// readDump runs `just --dump --dump-format json` against the target justfile.
// Only the raw call lives in TryCatchError; the failure is wrapped with the
// target path via MapLeft.
func readDump(w work) IOE.IOEither[error, []byte] {
	justfile := filepath.Join(w.Cfg.Root, "justfile")
	return F.Pipe1(
		IOE.TryCatchError(func() ([]byte, error) {
			return exec.Command(
				"just", "--justfile", justfile, "--dump", "--dump-format", "json",
			).Output()
		}),
		IOE.MapLeft[error](func(err error) error {
			return fmt.Errorf("just --dump failed for %s: %w", justfile, err)
		}),
	)
}

// parseAndCheck unmarshals the dump and walks every recipe body.
func parseAndCheck(data []byte) IOE.IOEither[error, []Finding] {
	return F.Pipe1(
		IOE.TryCatchError(func() (dump, error) {
			var d dump
			if err := json.Unmarshal(data, &d); err != nil {
				return dump{}, fmt.Errorf("parsing just dump: %w", err)
			}
			return d, nil
		}),
		IOE.Map(func(d dump) []Finding { return Walk(d) }),
	)
}

// report prints findings and fails the pipeline when fail-rule findings
// exist. Warn findings never flip the outcome.
func report(findings []Finding) E.Either[error, struct{}] {
	var fails, warns []Finding
	for _, f := range findings {
		if f.warn() {
			warns = append(warns, f)
			continue
		}
		fails = append(fails, f)
	}
	for _, f := range fails {
		detail := f.Detail
		if len(detail) > 120 {
			detail = detail[:120]
		}
		fmt.Printf("FAIL %s:%d  %s: %s\n", f.Recipe, f.Line, f.Rule, detail)
	}
	for _, f := range warns {
		fmt.Printf("WARN %s  %s: %s\n", f.Recipe, f.Rule, f.Detail)
	}
	if len(fails) > 0 {
		return E.Left[struct{}](fmt.Errorf("lint-recipes: %d finding(s)", len(fails)))
	}
	if len(warns) == 0 {
		fmt.Println("lint-recipes: no recipe findings")
	}
	return E.Right[error](struct{}{})
}

// Run lints the justfile in cfg.Root, prints findings, and returns an error
// when any fail-rule finding exists. The full pipeline stays inline here —
// no one-step handoff to a named continuation.
func Run(cfg Config) error {
	outcome := F.Pipe3(
		work{Cfg: cfg},
		readDump,
		IOE.Chain(parseAndCheck),
		IOE.ChainEitherK(report),
	)
	return E.ToError(outcome())
}
```

- [ ] **Step 4: Write the thin CLI**

`cmd/lint-recipes/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/recipeslint"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:      "lint-recipes",
		Usage:     "Mechanical checks for just recipes (bug-prevention gates)",
		ArgsUsage: "[target-dir]",
		Action: func(cltx *cli.Context) error {
			root := cltx.Args().Get(0)
			if root == "" {
				root = "."
			}
			return recipeslint.Run(recipeslint.Config{Root: root})
		},
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Write the unit tests**

`internal/recipeslint/lint_test.go`:

```go
package recipeslint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlatten(t *testing.T) {
	got := flatten([]any{"kubectl get cm ", []any{"interp"}})
	require.Equal(t, "kubectl get cm \x00", got)
}

func TestFlattenAllStrings(t *testing.T) {
	require.Equal(t, "echo hello", flatten([]any{"echo hello"}))
}

func TestCodePart(t *testing.T) {
	tests := []struct{ in, want string }{
		{"echo ok", "echo ok"},
		{"echo ok # trailing", "echo ok "},
		{"echo 'a # b'", "echo 'a # b'"},
		{"echo \"a # b\" # trailing", "echo \"a # b\" "},
		{"# pulumi-gcp falls back to $GOOGLE_CLOUD_PROJECT", ""},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, codePart(tc.in), "codePart(%q)", tc.in)
	}
}

func TestInterpOutsideQuotes(t *testing.T) {
	tests := map[string]bool{
		"kubectl get cm {{name}}":       true,  // bare: flagged
		"kubectl get cm \"{{ name }}\"": false, // quoted: fine
		"echo '{{name}}'":               true,  // single quotes evaluate: flagged
		"echo \"{{ a }} {{ b }}\"":      false,
	}
	for line, want := range tests {
		require.Equal(t, want, interpOutsideQuotes(line), "interpOutsideQuotes(%q)", line)
	}
}

func TestCheckMktemp(t *testing.T) {
	for _, safe := range []string{"out=$(mktemp)", "out=$(mktemp -d)", "out=$(mktemp -t kubeconfig-XXXXXX)"} {
		_, hit := checkMktemp(safe)
		require.False(t, hit, "must not be flagged: %s", safe)
	}
	tmpl, hit := checkMktemp("out=$(mktemp -t kubeconfig)")
	require.True(t, hit)
	require.Equal(t, "kubeconfig", tmpl)
}


func mkRecipe(body ...string) recipe {
	r := recipe{Shebang: true}
	for _, line := range body {
		r.Body = append(r.Body, []any{line})
	}
	return r
}

func TestCheckRecipeRules(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash",                                             // 1
		"set -euo pipefail",                                               // 2
		"gcloud storage buckets list --project=\"$GOOGLE_CLOUD_PROJECT\"", // 3
	)
	findings := checkRecipe("t", r)
	require.Len(t, findings, 1)
	require.Equal(t, RuleAmbientGCPProject, findings[0].Rule)
	require.Equal(t, 3, findings[0].Line)
}

func TestCheckRecipeCommentNotCode(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"# pulumi-gcp falls back to $GOOGLE_CLOUD_PROJECT when gcp:project unset",
		"echo ok",
	)
	require.Empty(t, checkRecipe("t", r), "comment must not be flagged")
}

func TestSuppressions(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash",                                           // 1
		"set -euo pipefail",                                             // 2
		"out=$(mktemp -t kubeconfig)  # lint-recipes:allow-mktemp-no-x", // 3
		"out2=$(mktemp -t gcs-creds)",                                   // 4
	)
	findings := checkRecipe("t", r)
	require.Len(t, findings, 1, "suppressed line 3 must not be flagged, neighbor line 4 must")
	require.Equal(t, 4, findings[0].Line)
	require.Equal(t, RuleMktempNoX, findings[0].Rule)
}

func TestPortForwardWarn(t *testing.T) {
	r := mkRecipe(
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"kubectl port-forward -n ns svc/svc 5432:5432 &",
		"echo ok",
	)
	findings := checkRecipe("t", r)
	require.Len(t, findings, 1)
	require.True(t, findings[0].warn(), "port-forward finding must be warn-only")
	// with a trap, nothing is reported
	rTrap := mkRecipe(
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"trap cleanup EXIT",
		"kubectl port-forward -n ns svc/svc 5432:5432 &",
	)
	require.Empty(t, checkRecipe("t", rTrap), "trap must silence port-forward rule")
}

func TestWalkSorted(t *testing.T) {
	d := dump{Recipes: map[string]recipe{
		"zeta":  mkRecipe("#!/usr/bin/env bash", "set -euo pipefail", "mktemp -t a"),
		"alpha": mkRecipe("#!/usr/bin/env bash", "set -euo pipefail", "mktemp -t b"),
	}}
	findings := Walk(d)
	require.Len(t, findings, 2)
	require.Equal(t, "alpha", findings[0].Recipe, "walk output must be sorted by recipe")
}
```

- [ ] **Step 6: Write the fixture integration tests**

`internal/recipeslint/pipeline_test.go`:

```go
package recipeslint

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureFindings lints the testdata fixture via the real `just --dump`.
func fixtureFindings(t *testing.T) []Finding {
	t.Helper()
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not installed")
	}
	out, err := exec.Command(
		"just", "--justfile", filepath.Join("testdata", "Justfile"),
		"--dump", "--dump-format", "json",
	).Output()
	require.NoError(t, err, "just --dump")
	var d dump
	require.NoError(t, json.Unmarshal(out, &d))
	return Walk(d)
}

func TestWalkFixture(t *testing.T) {
	want := []Finding{
		{Recipe: "ambient-gcp-project", Line: 3, Rule: RuleAmbientGCPProject},
		{Recipe: "create-pipe-swallow", Line: 3, Rule: RuleCreatePipeSwallow},
		{Recipe: "hardcoded-stack", Line: 3, Rule: RuleHardcodedStack},
		{Recipe: "mktemp-template-no-x", Line: 3, Rule: RuleMktempNoX},
		{Recipe: "suppressed-mktemp", Line: 4, Rule: RuleMktempNoX},
		{Recipe: "unquoted-interp", Line: 3, Rule: RuleUnquotedInterp},
	}
	got := fixtureFindings(t)
	require.Equal(t, want, got, "fixture findings mismatch")
}

func TestRunFailsOnFixture(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not installed")
	}
	require.Error(t, Run(Config{Root: "testdata"}), "Run must fail on the dirty fixture")
}

func TestRepoClean(t *testing.T) {
	err := Run(Config{Root: filepath.Join("..", "..")})
	require.NoError(t, err, "repo justfiles have findings — fix them before committing")
}
```


`TestRunFailsOnFixture` and `TestRepoClean` print to stdout; `go test` captures it — acceptable.
- [ ] **Step 7: Fix the eight live arangodb `mktemp` violations**

In `just_modules/arangodb.justfile`, replace each `mktemp -t <name>` (lines 183, 257, 258, 301, 302, 348, 349, 454) with `mktemp -t <name>-XXXXXX`. Verify with:
Run: `rg -n 'mktemp -t' just_modules/`
Expected: every hit ends in at least `XXXXXX`. `TestRepoClean` guards this permanently.

- [ ] **Step 8: Run all gates**

Run:
```bash
go test ./internal/recipeslint/ -v
golangci-lint fmt
golangci-lint run ./...
go test ./...
gopls check -severity=hint internal/recipeslint/*.go cmd/lint-recipes/main.go
```
Expected: all pass. Fix any diagnostic — nested-call gate: no `f(g(x))` and no `comb(args)(v)` outside Pipes in the new files.

- [ ] **Step 9: Commit**

```bash
git add internal/recipeslint cmd/lint-recipes just_modules/arangodb.justfile
git commit -m "feat(lint): add Go lint-recipes mechanical gate for just recipes

internal/recipeslint parses just --dump --dump-format json and checks
every recipe body: mktemp-no-x, create-pipe-swallow, ambient-gcp-project,
hardcoded-stack, unquoted-interp (fail), port-forward-no-trap (warn).
Inline lint-recipes:allow-<rule> suppressions. Fixes the eight remaining
mktemp -t templates without X suffixes in arangodb recipes."
```

---

### Task 2: `lint-recipes` and `check` recipes in root justfile

**Files:**
- Modify: `justfile` (dev-tools section, after `test-bootstrap`)

**Interfaces:**
- Consumes: `cmd/lint-recipes` (Task 1), `scripts/docs-lint.sh` (existing), the four contract scripts (existing), `just build` (existing).
- Produces: `just lint-recipes`, `just test-postgres-logical`, `just test-logto`, `just test-tool-versions`, `just check` — `check` is the single gate CI runs (Task 3) and agents/humans run before completing work.

- [ ] **Step 1: Add the recipes**

Insert into `justfile` after the `test-bootstrap` recipe:

```just
# Run lint-recipes mechanical checks on all just recipes
[group('dev-tools')]
[no-cd]
lint-recipes:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    go run ./cmd/lint-recipes

# Run postgres logical recipe contract tests
[group('dev-tools')]
[no-cd]
test-postgres-logical:
    #!/usr/bin/env bash
    set -euo pipefail
    "{{ justfile_directory() }}/scripts/test-postgres-logical-recipes.sh"

# Run logto recipe contract tests
[group('dev-tools')]
[no-cd]
test-logto:
    #!/usr/bin/env bash
    set -euo pipefail
    "{{ justfile_directory() }}/scripts/test-logto-recipes.sh"

# Run per-cluster tool-versions contract tests
[group('dev-tools')]
[no-cd]
test-tool-versions:
    #!/usr/bin/env bash
    set -euo pipefail
    "{{ justfile_directory() }}/scripts/test-tool-versions.sh"

# Run every mechanical gate: recipe lint, docs lint, contract tests, go build.
# One gate for humans and agents; CI runs the same command.
# No cloud credentials, Docker daemon, or asdf needed.
# Usage: just check
[group('dev-tools')]
[no-cd]
check: lint-recipes docs-lint test-bootstrap test-postgres-logical test-logto test-tool-versions build
```

- [ ] **Step 2: Verify each piece runs standalone**

Run:
```bash
just lint-recipes && just docs-lint && just test-bootstrap \
  && just test-postgres-logical && just test-logto && just test-tool-versions \
  && just build
```
Expected: all exit 0. If any contract script needs Docker or credentials, STOP and record the finding in the plan file — that script must move out of `check` into a separate `just check-live` recipe.

- [ ] **Step 3: Verify `just check` runs the full chain**

Run: `just check`
Expected: exit 0, all checks PASS lines visible.

- [ ] **Step 4: Verify check fails when a check fails**

Run: `git stash && sed -i '' 's/mktemp -t kubeconfig-XXXXXX/mktemp -t kubeconfig/' just_modules/arangodb.justfile && just check; rc=$?; git checkout just_modules/arangodb.justfile; [ $rc -ne 0 ] && echo "check-fails-correctly"`
Expected: `just check` exits non-zero with a `mktemp-no-x` FAIL; working tree restored.

- [ ] **Step 5: Commit**

```bash
git add justfile
git commit -m "feat(just): add check composite gate and per-script contract recipes"
```

---

### Task 3: CI wiring

**Files:**
- Modify: `.github/workflows/lint.yml`

**Interfaces:**
- Consumes: `just check` (Task 2), `extractions/setup-just@v4` (pattern already used in build-and-publish.yml with just-version '1.58.0').
- Produces: every PR runs `just check`; golangci-lint job unchanged.

- [ ] **Step 1: Extend lint.yml**

Replace `.github/workflows/lint.yml` contents with:

```yaml
name: Lint Golang code
on:
  pull_request:
    branches-ignore:
      - master
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - name: check out code
        uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version: '1.26'
          cache: false
      - name: run linter
        uses: golangci/golangci-lint-action@v7
        with:
          version: v2.12.2
  check:
    runs-on: ubuntu-latest
    steps:
      - name: check out code
        uses: actions/checkout@v7
      - name: setup just
        uses: extractions/setup-just@v4
        with:
          just-version: '1.58.0'
      - name: setup go
        uses: actions/setup-go@v7
        with:
          go-version: '1.26'
      - name: run mechanical gates (lint-recipes, docs-lint, contract tests, build)
        run: just check
```

- [ ] **Step 2: Verify locally what CI will run**

Run: `just check`
Expected: exit 0 (same command CI runs — parity is the point).

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/lint.yml
git commit -m "ci: run just check (recipe lint, docs lint, contract tests) on PRs"
```

---

### Task 4: Contract-test extension — destructive ordering + idempotency

**Files:**
- Modify: `scripts/test-postgres-logical-recipes.sh`

**Interfaces:**
- Consumes: `just --dry-run postgres restore-logical` output (existing render in the script).
- Produces: `assert_before EARLY LATE` helper asserting EARLY's line number < LATE's; two new test blocks: preflight-before-mutation ordering, byte-identical second dry-run.

Verified marker strings in current dry-run output (2026-09-19): `Error: archive checksum mismatch` (preflight), `majors are incompatible` (preflight), `pg_restore -h` (mutation), `ANALYZE VERBOSE` (post-mutation), `port-forward` (x2 — open, reopen-after-restore).

- [ ] **Step 1: Add the ordering helper and tests**

Append after the existing `render_restore` block in `scripts/test-postgres-logical-recipes.sh`:

```bash
assert_before() {
    local render="$1" early="$2" late="$3" label="$4"
    local early_line late_line
    early_line=$(echo "$render" | grep -nF "$early" | head -1 | cut -d: -f1)
    late_line=$(echo "$render" | grep -nF "$late" | head -1 | cut -d: -f1)
    if [ -z "$early_line" ] || [ -z "$late_line" ]; then
        echo "FAIL: $label: marker missing (early='$early' late='$late')" >&2
        exit 1
    fi
    if [ "$early_line" -ge "$late_line" ]; then
        echo "FAIL: $label: '$early' (line $early_line) must precede '$late' (line $late_line)" >&2
        exit 1
    fi
}

# Destructive ordering: every preflight check must appear before the first
# mutating command. Guards the fail-closed contract from commits 4634d6d,
# a5b7e71, d078722, 73659d5 — a regression that reorders restore before
# validation fails here.
assert_before "$render_restore" 'Error: archive checksum mismatch' 'pg_restore -h' 'checksum before restore'
assert_before "$render_restore" 'majors are incompatible' 'pg_restore -h' 'major compat before restore'
assert_before "$render_restore" 'Error: pg_restore cannot read archive' 'pg_restore -h' 'archive readable before restore'

# Post-restore hygiene: reopen the port-forward before ANALYZE (commit 4ae1c91).
# Two port-forward invocations expected (open before restore, reopen after);
# the reopen must precede ANALYZE.
pf_first=$(echo "$render_restore" | grep -n 'port-forward' | head -1 | cut -d: -f1)
pf_reopen=$(echo "$render_restore" | grep -n 'port-forward' | tail -1 | cut -d: -f1)
analyze_line=$(echo "$render_restore" | grep -n 'ANALYZE VERBOSE' | cut -d: -f1)
if [ "$pf_first" = "$pf_reopen" ] || [ -z "$pf_first" ]; then
    echo "FAIL: expected two port-forward invocations (open + reopen), found one" >&2
    exit 1
fi
if [ "$pf_reopen" -ge "$analyze_line" ]; then
    echo "FAIL: reopen port-forward (line $pf_reopen) must precede ANALYZE (line $analyze_line)" >&2
    exit 1
fi

# Idempotency: a second identical dry-run must render byte-identical output.
render_restore_2=$(just --dry-run postgres restore-logical \
    --archive scratch/postgres/test.dump \
    --app-password test-password 2>&1)
if [ "$render_restore" != "$render_restore_2" ]; then
    echo "FAIL: restore-logical dry-run not deterministic across two runs" >&2
    diff <(echo "$render_restore") <(echo "$render_restore_2") | head -20 >&2
    exit 1
fi
echo "PASS: destructive ordering + idempotency checks"
```

- [ ] **Step 2: Run the test, verify it passes**

Run: `./scripts/test-postgres-logical-recipes.sh`
Expected: all existing checks plus the new PASS line. If `pf_reopen -ge analyze_line` fails, the 4ae1c91 fix regressed — fix the recipe, not the test.

- [ ] **Step 3: Commit**

```bash
git add scripts/test-postgres-logical-recipes.sh
git commit -m "test(postgres): assert destructive ordering and dry-run idempotency in logical contract tests"
```

---

### Task 5: Docs reference page + AGENTS.md infrastructure-change checklist

**Files:**
- Create: `docs/reference/dev/check.md`
- Modify: `docs/README.md` TOC (per STYLE.md TOC-sync rule)
- Modify: `AGENTS.md` (append a section; existing Documentation section untouched)

**Interfaces:**
- Consumes: `just check` (Task 2).
- Produces: canonical invariants every agent (and human) applies to justfile/Pulumi/recipe changes; a reference page so `check` has a link target (docs-lint rule 7).

- [ ] **Step 1: Write the reference page**

`docs/reference/dev/check.md` (lean reference shape per STYLE.md):

```markdown
# check

Aggregate mechanical gate: recipe lint, docs lint, contract tests, and the
Go build. One command for humans and agents; CI runs the same command on
every PR. Needs no cloud credentials, Docker daemon, or asdf.

    just check

## What it runs

| Recipe | Script | Catches |
|---|---|---|
| `lint-recipes` | `go run ./cmd/lint-recipes` | mktemp without `XXXXXX`, `create \|\| echo` rerun breakage, ambient `$GOOGLE_CLOUD_PROJECT`, hardcoded `--stack prod`, unquoted interpolations |
| `docs-lint` | `scripts/docs-lint.sh` | STYLE.md mechanical rules (TOC, anchors, links, section shape) |
| `test-bootstrap` | `scripts/test-bootstrap-bundle.sh` | kops bundle contract |
| `test-postgres-logical` | `scripts/test-postgres-logical-recipes.sh` | logical restore contract incl. destructive ordering + idempotency |
| `test-logto` | `scripts/test-logto-recipes.sh` | logto recipe contract |
| `test-tool-versions` | `scripts/test-tool-versions.sh` | per-cluster asdf manifest lifecycle |
| `build` | `go build ./cmd/cluster-ops` | compile gate |

## Suppression

A recipe line can silence one rule for that line only with a trailing
comment: `# lint-recipes:allow-<rule-id>`. Rules are removed, not loosened,
if noisy — never add a central allowlist.

## Related

→ [docs/STYLE.md](../STYLE.md)
```

(Adjust the relative link after checking the final location satisfies docs-lint's link/anchor checks; run `just docs-lint` to verify.)

- [ ] **Step 2: Sync the docs TOC**

Add a row for `docs/reference/dev/check.md` in the `docs/README.md` reference table, then run `just docs-lint` and fix any anchor/TOC findings.

- [ ] **Step 3: Append the AGENTS.md checklist**

Add to the end of `AGENTS.md`:

```markdown
## Infrastructure change checklist

Every change to `just_modules/*.justfile`, Pulumi programs (`*/main.go`), or
recipe-invoking scripts must satisfy all of these. `just check` enforces the
mechanical subset; the rest are review rules.

- Bind identity, project, stack, and namespace explicitly (from the
  `cluster-env` file or stack exports). Never read ambient
  `$GOOGLE_CLOUD_PROJECT`, `$PULUMI_STACK`, or default gcloud config.
- One source of truth per resource name — derive producer and consumer from
  a single helper/variable; never hardcode a name that Pulumi autonames or a
  chart generates.
- Create cloud objects with describe-then-create probes, never
  `create || echo "already exists"`. Retry only classified transient errors
  (IAM propagation) with backoff, never bare `sleep`.
- Destructive recipes (restore, reset, delete, cluster create): validate
  everything (archive, checksums, versions, target state) BEFORE the first
  mutating command; fail closed. Preflight lines must precede mutation lines
  — contract tests assert the order.
- Quote every user-supplied interpolation landing in a shell command with
  `quote()`; keep `mktemp` templates with a `XXXXXX` suffix.
- Wrap `kubectl port-forward` in a `trap ... EXIT` cleanup; re-probe long
  forwards before reusing them.
- Values containing password/token/secret/credential/keys are secrets unless
  proven otherwise; pass `--plaintext` to `set-config` only for non-secret
  pointers the Pulumi heuristic misclassifies.
- Run `just check` before reporting completion — zero failures.
```

- [ ] **Step 4: Verify gates**

Run: `just check && just docs-lint`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add docs/reference/dev/check.md docs/README.md AGENTS.md
git commit -m "docs: add check reference page and infrastructure-change checklist to AGENTS.md"
```

---

### Task 6 (optional, only if user wants PR flow): PR template

**Files:**
- Create: `.github/PULL_REQUEST_TEMPLATE.md`

Panel split 2-1 against for a solo repo — implement ONLY if the user says yes; skip otherwise.

- [ ] **Step 1: Write template**

```markdown
## Failure-mode checklist (from the 32-fix bug analysis)

- [ ] No ambient env reads (`$GOOGLE_CLOUD_PROJECT`, default gcloud config) — identity bound explicitly
- [ ] Resource names derived from one source; no hardcoded Pulumi-autonamed/chart-generated names
- [ ] Re-run safe: probe-then-create, no `create || echo`; transient errors retried with backoff
- [ ] Destructive operations validate everything before first mutation
- [ ] User-supplied interpolations `quote()`-wrapped; mktemp templates have `XXXXXX`
- [ ] `just check` passes locally
```

- [ ] **Step 2: Commit**

```bash
git add .github/PULL_REQUEST_TEMPLATE.md
git commit -m "chore(ci): add failure-mode PR checklist"
```

---

### Task 7 (optional follow-up): pipelinecheck for the lint-recipes entrypoint

Only if the user opts in — adds `github.com/dictyBase/fp-go-loom` dependency and drops a thin `cmd/lint-recipes/pipeline_test.go` calling `pipelinecheck.Require(t, pipelinecheck.Config{RequirePointFreeSeed: true})`, asserting `Run` keeps its full inline `F.Pipe2` (no `F.Pipe1(seed, namedFn)` handoff, no applied seed). Skip until the repo adopts fp-go-loom elsewhere.

---

## Self-Review

**Spec coverage:** classes 1,2,4,7 covered by Go lint rules + AGENTS.md; class 6 by contract ordering tests (Task 4); class 8 by port-forward warn rule + 4ae1c91 regression assert; class 3 (name drift) and 5 (platform assumptions) intentionally deferred to review rules + AGENTS.md — regex cannot check them with acceptable precision (panel consensus). Nightly preview and pi skill cut.

**Placeholder scan:** No TBD/TODO/appropriate-error-handling patterns; every code step carries its complete code. Fixture expectations in `TestWalkFixture` were derived from the fixture; if an executor's run diverges, the fixture or `just --dump` shape is wrong — investigate, do not loosen the test.

**Type consistency:** `Run(Config) error`, `Finding{Recipe,Line,Rule,Detail}`, rule ID strings, and the `# lint-recipes:allow-<rule-id>` marker are consistent across lint.go, pipeline.go, both test files, the fixture, the Task 2 recipe, and the Task 5 docs table. All tests use `github.com/stretchr/testify/require` (already in go.mod v1.12.1; the repo's stack-program tests already import it). fp-go API signatures (`IOE.TryCatchError(func() (A, error))`, `IOE.MapLeft[error](f)`, `IOE.Chain`, `IOE.ChainEitherK`, `E.Left[struct{}]`/`E.Right[error]`, `E.ToError`) verified against the vendored fp-go v2 docs.

**Review Focus pinned:** interpolation flattening → `TestFlatten` + `TestWalkFixture`; comment non-fire → `TestCheckRecipeRules` (comment case) + `TestCodePart`; mktemp-no-template pass → `TestCheckMktemp` bare/-d cases; suppression precision → `TestSuppressions` (neighbor still flagged); deterministic output → `TestWalkSorted`; CI daemon-freedom → Task 2 Step 2 STOP clause; repo self-clean enforced forever by `TestRepoClean`.
