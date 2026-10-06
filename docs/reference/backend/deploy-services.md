# Backend Services Aggregate Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

`just ci deploy-backend-services` runs the full backend service chain for
every entry in `config/services.yaml` against one cluster, in one run. The
individual recipes (scaffold, prereq gate, bootstrap, variables, render) stay
usable solo; the aggregate composes them. Idempotent: a re-run converges —
it skips prior progress instead of failing on it.

## Behavior

Once per run:

1. Resolves the cluster through the [registry](cluster-registry.md) from
   `--stack`.
2. Requires the deployer key at `credentials/<cluster>/deployer.json`
   ([create-deploy-key](ci-credentials.md)); fails with the create command
   when absent.
3. `set-deploy-secret` — one org-secret upsert covering every target repo
   (`--visibility selected`).

Per service (order fixed by the contract test):

1. **Scaffold** — skipped when `Pulumi.<stack>.yaml` already exists
   ("stack config present, skipping scaffold"). Ports come from the manifest,
   never from flags.
2. **Prerequisite gate** — `check-backend-prereqs` (read-only).
3. **Bootstrap** — `bootstrap-service`; when the deployment already runs the
   wanted tag it verifies rollout only (no `pulumi up`). Without
   `--image-tag`, the tag resolves per service from the repo's highest semver
   tag (`just ci latest-tag`), verbatim (`v`-prefix or bare).
4. **Variables** — `sync-deploy-vars` upsert.
5. **Render** — `tag-build.yaml` regenerated to
   `bin/ci-render/<folder>-tag-build.yaml` (deterministic output; the PR into
   the service repo stays manual).

## Service Manifest

`config/services.yaml` — the four modware services and their ports:

| app | folder | port |
|-----|--------|------|
| order | modware-order | 9250 |
| annotation | modware-annotation | 9250 |
| stock | modware-stock | 9345 |
| content | modware-content | 9250 |

Ports trace to the frozen lab stacks (`modware-*/Pulumi.experiments.yaml`);
change a port here in the same change as the service's stack files.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | Yes* | `$PULUMI_STACK` | Registry stack name |
| `--image-tag` / `-t` | No | per-service `latest-tag` | Pin one tag across all services |
| `--services` | No | all | Comma-separated subset of apps |
| `--arango-service` / `-a` | No | `arangodb` | Passed to the prereq gate |

## Warnings

- The rendered files are artifacts for the service repos, not for this repo —
  PR them in; never commit `bin/ci-render/`.
- A failing service aborts the loop; finished services keep their state.
  Re-run after the fix — prior progress is skipped, the failed service
  converges.