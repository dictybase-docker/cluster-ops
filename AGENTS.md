# Agent Instructions for cluster-ops

## Documentation

Before creating or editing any file under `docs/`, read
[`docs/STYLE.md`](docs/STYLE.md) and follow it. Core rules:

- Guides stay lean: summary + `→ [details]` link + one command per section.
  Explanation lives in `docs/reference/<area>/`.
- Never duplicate content between docs — link instead.
- Commands show minimal invocation; defaults come from `just cluster-env`.
- Keep TOC, Quick Reference, and cross-doc anchors in sync on every edit.
- **Always use `just` recipes, never raw `kubectl` or manual YAML assembly.**
  If a project lacks a `scaffold-*-stack` or `verify` recipe, build the recipe
  and template first, then document the recipe.
- **Write for human operators in ASD-STE100 style.** Never dump Go internal
  function names (`conf.TryObject`, `openTarget`, call chains, file/line
  citations) into `docs/`.
- **Surface optional modes (`authEnabled`, `ingress.enabled`) as top-level
  numbered sections** in the guide TOC and Quick Reference, stating the default
  (`false`) in the first line.
- **Never `set-secret` on Kubernetes `secretKeyRef` pointers.** Secret names
  (`minio-root`, `backend`) and key names (`rootUser`, `rootPassword`) are
  pointers resolved at pod runtime — keep them plain text in
  `Pulumi.<stack>.yaml`.

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
