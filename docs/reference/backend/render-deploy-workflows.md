# Deploy Workflow Render Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

`just ci render-tag-deploy` renders the complete `tag-build.yaml` for a
service repo — test, lint, then the composite deploy — from the repo template.
The file lands on disk; the PR, review, and merge in the service repo stay
manual cross-repo steps.

## Behavior

1. Resolves the cluster through the [registry](cluster-registry.md) from
   `--stack` → `<CI_ENV>_*` variable names.
2. Renders from `config/templates/tag-build-deploy.yaml.tmpl`: trigger is tags
   only; `concurrency` group serializes runs per (repo, tag); deploy job
   `needs: [test, lint]` — a tag passes the same gate as develop; static
   `with:` values come from the registry (cluster, state storage, versions,
   `environment: production` which selects `PROD_DEPLOY_SA_KEY` inside
   `composite-deploy`).
3. The same PR deletes `staging-build.yaml` — no real environment behind it.
4. Writes to `--out`; never edits the service repo in place.
5. Accepts `--workflow-ref <ref>` so the output is pin-ready.

Rollback after go-live: `gh run rerun <run-id>` on the previous tag's run —
same ref, same image, same stack, no stack file edit.

## Template Shape

The rendered file mirrors `ci.yml`: the existing test job, a lint job, then
one deploy job calling `dictyBase/workflows/composite-deploy.yaml`. The old
`build-publish-image` call is gone — the composite builds and publishes the
image itself; keeping it would build every tag twice.

## Contract Checks

The render contract test asserts: rendered YAML parses; trigger is tags only;
`needs: [test, lint]`; static values match the registry entry; every `with:`
key exists in `composite-deploy.yaml`'s `workflow_call.inputs` (input-drift
guard — a caller/reusable-workflow mismatch fails at render time, not in a
prod deploy run); zero occurrences of `staging`.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | Yes* | `$PULUMI_STACK` | Registry stack; drives all cluster values |
| `--app` / `-a` | Yes | — | Application name passed to the composite |
| `--project` / `-p` | Yes | — | cluster-ops Pulumi project folder (= service repo name) |
| `--out` / `-o` | Yes | — | Output file path (rendered `tag-build.yaml`) |
| `--workflow-ref` | No | `develop` | Pin the reusable workflow ref in the output |

*Or `$PULUMI_STACK` set inside `cluster-env`.

## Warnings

- The rendered file is inert until merged into the service repo — render,
  PR, merge, then tags start deploying.
- The `deploy` job selects `PROD_DEPLOY_SA_KEY` only after the
  `dictyBase/workflows` key-selection change is merged; without it a tag
  deploy authenticates with the dev key and fails.
- Keep `--workflow-ref` at `develop` until the workflows repo cuts a release;
  pinning early fails the render against the un-tagged ref.
