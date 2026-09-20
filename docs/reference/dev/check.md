# check

Back to: [README](../../../README.md)

## What it runs

`just check` is the aggregate mechanical gate — recipe lint, docs lint,
contract tests, and the Go build. One command for humans and agents; CI runs
the same command on every PR. Needs no cloud credentials, Docker daemon, or
asdf.

```bash
just check
```

| Recipe | Script | Catches |
|--------|--------|---------|
| `lint-recipes` | `go run ./cmd/lint-recipes` | `mktemp` without `XXXXXX`, `create \|\| echo` rerun breakage, ambient `$GOOGLE_CLOUD_PROJECT`, hardcoded `--stack` literals, unquoted interpolations |
| `docs docs-lint` | `scripts/docs-lint.sh` | STYLE.md mechanical rules (TOC, anchors, links, section shape) |
| `test-postgres-logical` | `scripts/test-postgres-logical-recipes.sh` | logical restore contract incl. destructive ordering + idempotency |
| `test-logto` | `scripts/test-logto-recipes.sh` | Logto recipe contract |
| `test-bootstrap` | `scripts/test-bootstrap-bundle.sh` | kops bundle contract (mock kops + kubectl) |
| `test-tool-versions` | `scripts/test-tool-versions.sh` | per-cluster asdf manifest lifecycle |
| `build` | `go build ./cmd/cluster-ops` | compile gate |

Each recipe also runs standalone; `just --list` shows them under
`dev-tools`.

## lint-recipes rules

`internal/recipeslint` parses `just --dump --dump-format json` — never raw
justfiles — and checks every recipe body in the root and all modules.

Fail rules:

| Rule | Reason |
|------|--------|
| `mktemp-no-x` | BSD/macOS `mktemp` fails on templates without an `X{4}` suffix |
| `create-pipe-swallow` | `gcloud ... create \|\| echo "already exists"` swallows the exit code, not stderr; use describe-then-create |
| `ambient-gcp-project` | `$GOOGLE_CLOUD_PROJECT` in recipe code carries a stale cross-cluster value; bind the project explicitly |
| `hardcoded-stack` | `--stack` literals predate per-cluster stack names; use `$PULUMI_STACK` |
| `unquoted-interp` | `{{...}}` outside double quotes; `quote()`-wrapped calls are safe bare |

Warn-only rules (reported, never fail):

| Rule | Reason |
|------|--------|
| `port-forward-no-trap` | `kubectl port-forward` in a recipe body with no `trap` cleanup |

## Suppression

A recipe line can silence one rule for that line only with a trailing
comment: `# lint-recipes:allow-<rule-id>`. Never add a central allowlist;
rules are removed, not loosened, if noisy.
