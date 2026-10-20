# Deploy Workflow Render Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

Reference for the three CI recipes that turn a tag push into a production
deploy: `render-composite-deploy` (reusable workflow in `dictyBase/workflows`),
`render-tag-deploy` (per-service `tag-build.yaml`), and `pr-deploy-workflows`
(opens the PRs for both).

## Table of Contents

- [What It Does](#what-it-does)
- [Behavior](#behavior)
- [One-Time Workflow Changes for Automated Tag Deploys](#one-time-workflow-changes-for-automated-tag-deploys)
- [Template Shape](#template-shape)
- [Contract Checks](#contract-checks)
- [Flags](#flags)
  - [`render-composite-deploy` Flags](#render-composite-deploy-flags)
  - [`render-tag-deploy` Flags](#render-tag-deploy-flags)
  - [`pr-deploy-workflows` Flags](#pr-deploy-workflows-flags)
- [Warnings](#warnings)

## What It Does

`just ci render-composite-deploy` publishes the org variable
`<CI_ENV>_PULUMI_STATE_STORAGE` from the cluster registry and renders
`dictyBase/workflows/.github/workflows/composite-deploy.yaml` to
`bin/ci-render/composite-deploy.yaml` so `inputs.environment == 'production'`
selects `secrets.<CI_ENV>_DEPLOY_SA_KEY` and `vars.<CI_ENV>_PULUMI_STATE_STORAGE`.

`just ci render-tag-deploy` renders the service repo's `tag-build.yaml` from
`config/templates/tag-build-deploy.yaml.tmpl` — a single job that calls the
composite deploy, no test or lint job (see [template shape](#template-shape)).

`just ci pr-deploy-workflows` clones `dictyBase/workflows` and each target
service repo into a temporary directory, applies the rendered workflow files
(removing obsolete `staging-build.yaml`), pushes the branches over HTTPS, and
opens pull requests ready for review and merge.

## Behavior

`render-composite-deploy`:

1. Resolves the cluster through the [registry](cluster-registry.md) from
   `--cluster` (or `$CLUSTER_NAME`).
2. Sets org variable `<CI_ENV>_PULUMI_STATE_STORAGE` (for example
   `PROD_PULUMI_STATE_STORAGE=gs://pulumi-state-dcr-kube1`) on org `dictyBase`.
3. Fetches `dictyBase/workflows/.github/workflows/composite-deploy.yaml` at
   `--workflow-ref` and rewrites `credentials_json` and `--pulumi-state` to
   select `<CI_ENV>_DEPLOY_SA_KEY` and `<CI_ENV>_PULUMI_STATE_STORAGE` when
   `inputs.environment == 'production'`.
4. Writes to `--out` (default `bin/ci-render/composite-deploy.yaml`) and tells
   the operator whether upstream already matches or needs a one-time PR to
   `dictyBase/workflows`.

`render-tag-deploy`:

1. Resolves the cluster through the [registry](cluster-registry.md) from
   `--stack` → `<CI_ENV>_*` variable names.
2. Renders from `config/templates/tag-build-deploy.yaml.tmpl`: trigger is tags
   only; `concurrency` group serializes runs per (repo, tag); static `with:`
   values come from the registry (cluster, state storage, versions,
   `environment: production` which selects `PROD_DEPLOY_SA_KEY` inside
   `composite-deploy`); `docker_image` defaults to `--project` with `_`
   replaced by `-`, so `--project graphql_server` renders
   `docker_image: graphql-server`.
3. The same PR deletes `staging-build.yaml` — no real environment behind it.
4. Writes to `--out`; never edits the service repo in place.
5. Accepts `--workflow-ref <ref>` so the output is pin-ready.

Rollback after go-live: `gh run rerun <run-id>` on the previous tag's run —
same ref, same image, same stack, no stack file edit.

## One-Time Workflow Changes for Automated Tag Deploys

Automated production deploys on tag push require two one-time workflow updates
(rendered by `render-composite-deploy` and `render-tag-deploy`, and opened as
PRs by `pr-deploy-workflows`):

1. **`dictyBase/workflows` (`.github/workflows/composite-deploy.yaml`):**
   - `credentials_json` selects `secrets.<CI_ENV>_DEPLOY_SA_KEY` when
     `inputs.environment == 'production'` (falling back to `secrets.DEPLOY_SA_KEY`
     for `develop` push deploys).
   - `--pulumi-state` selects `vars.<CI_ENV>_PULUMI_STATE_STORAGE` when
     `inputs.environment == 'production'` (falling back to
     `vars.PULUMI_STATE_STORAGE` for `develop` push deploys).
2. **Each service repo (`.github/workflows/tag-build.yaml`):**
   - Replaces the old image-only `build-publish-image.yaml` workflow with a
     single `composite-deploy.yaml` call passing `environment: production`,
     `stack: <stack>`, and `<CI_ENV>_*` repo variables (`PROD_CLUSTER`,
     `PROD_KOPS_STATE_STORAGE`, `PROD_KOPS_VERSION`, `PROD_KUBECTL_VERSION`,
     `PROD_PULUMI_VERSION`).
   - Deletes obsolete `.github/workflows/staging-build.yaml`.

Merge the `dictyBase/workflows` PR first, then merge each service repo's PR.

## Template Shape

The rendered file is deploy-only: one job calling
`dictyBase/workflows/composite-deploy.yaml`, no `test` or `lint` job. The tag
is cut from `master`, a fast-forward of `develop` that already passed lint,
test and deploy in `ci.yml`, so re-running those gates would re-verify the same
tree. The old `build-publish-image` call is gone — the composite builds and
publishes the image itself; keeping it would build every tag twice.

## Contract Checks

`scripts/test-render-tag-deploy.sh` asserts: rendered YAML parses; trigger is
tags only; no `test` or `lint` job; static values match the registry entry;
`docker_image` is the full image name, including the `graphql_server` →
`graphql-server` case rendered with `--stack` defaulted from `$PULUMI_STACK`;
every `with:` key exists in `composite-deploy.yaml`'s `workflow_call.inputs`
(input-drift guard — a caller/reusable-workflow mismatch fails at render time,
not in a prod deploy run); zero occurrences of `staging`.

## Flags

### `render-composite-deploy` Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--cluster` / `-c` | No | `$CLUSTER_NAME` | Resolved as `config/clusters/<cluster>.yaml` directly — not by `stack:` lookup. Fails with `no cluster name` when neither flag nor env var is set, and `no registry entry for cluster` when the file is absent |
| `--out` / `-o` | No | `bin/ci-render/composite-deploy.yaml` | Rendered copy of upstream `composite-deploy.yaml`; always written, even when it matches upstream |
| `--workflow-ref` / `-w` | No | `develop` | Ref of `dictyBase/workflows` to fetch and diff against |

### `render-tag-deploy` Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Must match a registry entry's `stack:` key; drives all cluster values. Fails with `no stack name` when neither flag nor env var is set |
| `--app` / `-a` | Yes | — | Application name passed to the composite (`order`, `graphql-server`) |
| `--project` / `-p` | Yes | — | cluster-ops Pulumi project folder (`modware-order`, `graphql_server`) |
| `--docker-image` / `-d` | No | `--project` with `_` → `-` | Docker Hub image name. Only needed when it differs from the dashed project folder |
| `--out` / `-o` | Yes | — | Output file path (rendered `tag-build.yaml`) |
| `--workflow-ref` / `-w` | No | `develop` | Pin the reusable workflow ref in the output; also the ref the input-drift guard fetches |

### `pr-deploy-workflows` Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Registry lookup by `stack:` key, same as `render-tag-deploy`; the entry's `cluster:` is what gets passed to `render-composite-deploy` |
| `--services` | No | all of `config/services.yaml` | Comma-separated app subset (`order,stock`) |
| `--render-dir` | No | `bin/ci-render` | Where rendered files are read from. A missing `composite-deploy.yaml` or `<folder>-tag-build.yaml` is rendered on demand with `--workflow-ref`/`--base` set to `--base` |
| `--base` / `-b` | No | `develop` | Branch cloned, PR'd against, **and** used as the pinned workflow ref for any on-demand render |

## Warnings

- The rendered file is inert until merged into the service repo — render,
  PR, merge, then tags start deploying.
- The `deploy` job selects `PROD_DEPLOY_SA_KEY` only after the
  `dictyBase/workflows` key-selection change is merged; without it a tag
  deploy authenticates with the dev key and fails.
- Keep `--workflow-ref` at `develop` until the workflows repo cuts a release;
  pinning early fails the render against the un-tagged ref.
