# Plan: Upstream Deploy Refactor (dagger-of-dcr and workflows)

**Status**: Implemented. P0.1–P0.3 landed in dagger-of-dcr and workflows (named-args recipes, version/ref inputs, cleanup). P0.4 landed in cluster-ops: the registry (`config/clusters/<cluster>.yaml`) owns the tool versions, `just ci sync-deploy-vars` publishes them, and the rendered tag workflow passes them. Kept as the record of the merge order and the compatibility reasoning.

## Table of Contents
- [Goal](#goal)
- [Non-goals](#non-goals)
- [Current state](#current-state)
- [The fact that makes the plan safe](#the-fact-that-makes-the-plan-safe)
- [Work items](#work-items)
  - [P0.1 dagger-of-dcr: named arguments and tool versions](#p01-dagger-of-dcr-named-arguments-and-tool-versions)
  - [P0.2 workflows: named callers and version inputs](#p02-workflows-named-callers-and-version-inputs)
  - [P0.3 Cleanup: remove the compatibility layer](#p03-cleanup-remove-the-compatibility-layer)
  - [P0.4 cluster-ops bridge: registry owns the tool versions](#p04-cluster-ops-bridge--registry-owns-the-tool-versions)
- [Order of execution](#order-of-execution)
- [Acceptance](#acceptance)
- [Risks](#risks)
- [Effect on the modware service plan](#effect-on-the-modware-service-plan)

## Goal

Make the deploy tooling accept a **different tool version for each cluster**.

Today one set of fixed versions serves every cluster. The experiments cluster
(Kubernetes 1.28.8) works with these versions. The dcr-kube1 cluster
(Kubernetes 1.35.8) does not. kOps must stay within one minor release of the
cluster. kOps 1.29.2 cannot manage a 1.35.8 cluster.

Three changes follow from this goal:

1. The `dagger-of-dcr` recipes take **named arguments**. Tool versions and the
   cluster-ops ref become arguments with defaults.
2. The `dictyBase/workflows` callers pass these arguments. New inputs carry the
   versions.
3. The cluster-ops registry stores the versions per cluster. The recipes read
   the versions through GitHub variables. No repo holds a second copy.

## Non-goals

- Do **not** change the deploy behavior of any cluster. Default values keep the
   current behavior.
- Do **not** touch `deploy-setup.yaml`, `deploy-microservice.yaml`, or
   `dagger-setup.yaml`. These workflows call the dagger modules directly. They
   do not use the Justfile. Refactor them later, or retire them.
- Do **not** change any service repository (`modware-order`, `modware-stock`,
   and the rest) in Phase 0.
- Do **not** pin any ref to a tag. Pinning is a decision for the modware
   service plan, item 8. This refactor makes pinning possible.

## Current state

All facts below come from the local checkouts of both repositories.

| Piece | Today | Problem |
|-------|-------|---------|
| Tool versions in `dagger-of-dcr/Justfile` | `kops_version := "1.29.2"`, `kubectl_version := "1.28.8"`, `pulumi_version := "3.108.0"` — global constants | Every cluster gets the same version. dcr-kube1 (Kubernetes 1.35.8) needs kOps 1.35.x |
| Recipe arguments | Positional. `deploy-backend` takes 7 values in a fixed order | The reader cannot see what a value means. Secrets sit in the argument list |
| Identity values | `.env` file plus `set dotenv-load`. The workflows write this file | A hidden dependency sits between the caller and the recipe |
| cluster-ops ref | `pulumiOpsBranch = "develop"` — a constant in `pulumi-ops/dagger/main.go` | CI always clones cluster-ops develop. Pinning is impossible without a code change |
| Callers in `dictyBase/workflows` | `composite-deploy.yaml`, `deploy-buildless-backend.yaml`, `build-publish-image.yaml` call `just` with positional values | Adding one argument forces every caller to change in the same moment |
| Cluster Kubernetes versions | experiments 1.28.8, dcr-kube1 1.35.8 | The fixed tool set fits experiments only |

## The fact that makes the plan safe

A `just` recipe with `[arg(...)]` declarations accepts **both** calling styles:

- positional: `just deploy-backend name state …`
- named: `just deploy-backend --cluster name --cluster-state state …`

The workflows already set `JUST_UNSTABLE: 1`. This variable enables the named
style.

This fact lets the two repositories change **in sequence**. The old callers
keep working during the change. No deploy breaks in the window between the two
merges.

## Work items

### P0.1 dagger-of-dcr: named arguments and tool versions

One pull request in `dictybase-docker/dagger-of-dcr`.

**Changes**

1. Convert each recipe to `[arg(...)]` declarations:
   `deploy-backend`, `deploy-buildless-backend`, `deploy-frontend`,
   `export-kubectl`, `build-publish-image`, `build-publish-arangopg-image`,
   `lint-repo`.
2. Add tool arguments with **defaults equal to the current constants**:
   `--kops-version` (1.29.2), `--kubectl-version` (1.28.8),
   `--pulumi-version` (3.108.0), `--cluster-ops-ref` (`develop`).
3. Convert the identity values to named arguments. Give each argument a default
   that reads the old environment variable
   (`env_var_or_default("APP", "")` and the rest). The `.env` path keeps
   working.
4. Change `pulumi-ops/dagger/main.go`: replace the `pulumiOpsBranch` constant
   with a function parameter. The parameter default stays `develop`.
5. Rewrite the recipe header comments in the flag style. Add a README section
   that names the per-cluster versions.

**Compatibility guarantee** — a caller that uses positional values or the
`.env` file works without a change.

**Verification** — no canary needed at this point. The callers did not change.
Run `just deploy-backend --help` style checks locally; run one local
`export-kubectl` against the experiments cluster.

### P0.2 workflows: named callers and version inputs

One pull request in `dictyBase/workflows`.

**Changes**

1. `composite-deploy.yaml` — add inputs with **defaults equal to the P0.1
   defaults**: `kops_version`, `kubectl_version`, `pulumi_version`,
   `cluster_ops_ref`, `dagger_ref` (the checkout ref for dagger-of-dcr).
2. Call the recipe with named arguments only:
   ```yaml
   just deploy-backend \
     --cluster ${{ inputs.cluster }} \
     --cluster-state ${{ inputs.cluster_state_storage }} \
     --pulumi-state ${{ vars.PULUMI_STATE_STORAGE }} \
     --gcp-credentials-file ${{ steps.gcp_authentication.outputs.credentials_file_path }} \
     --ref ${{ inputs.ref }} \
     --kops-version ${{ inputs.kops_version }} \
     --kubectl-version ${{ inputs.kubectl_version }} \
     --pulumi-version ${{ inputs.pulumi_version }} \
     --cluster-ops-ref ${{ inputs.cluster_ops_ref }} \
     --token ${{ secrets.GH_DEPLOY_TOKEN }} \
     --user ${{ secrets.DOCKERHUB_USER }} \
     --pass ${{ secrets.DOCKER_PASS }}
   ```
3. Keep the `.env` writing step for this phase. It is the rollback path.
4. Apply the same conversion to `deploy-buildless-backend.yaml` and
   `build-publish-image.yaml`.

**Compatibility guarantee** — a caller that passes no new input gets the P0.1
defaults. Every service repository on develop keeps working without a change.

**Verification — blocking canary**

1. Merge the pull request.
2. Push one commit to `modware-order` develop. Watch the deploy job. It must
   be green. This exercises the `composite-deploy` path.
3. Push one commit to `modware-annotation` develop. Watch the deploy jobs.
   They must be green. This exercises the `deploy-buildless-backend` path.
4. Do not continue until both canaries pass.

### P0.3 Cleanup: remove the compatibility layer

Two small pull requests, one per repository. Both land only after the P0.2
canaries pass.

**Changes**

1. `dagger-of-dcr`: remove the environment defaults for the identity
   arguments. Remove `set dotenv-load`. Keep the version defaults.
2. `workflows`: delete the `.env` writing steps.
3. Run both canaries again.

**Compatibility guarantee** — none. This step breaks the old calling style on
purpose. The only callers in service repositories are the workflows themselves,
which already moved to named arguments in P0.2.

### P0.4 cluster-ops bridge: registry owns the tool versions

This item starts the work in this repository. It runs together with the
modware service plan, item 1 and item 5.

**Changes**

1. The registry file `config/clusters/<cluster>.yaml` gains three keys:
   `kops_version`, `kubectl_version`, `pulumi_version`. For dcr-kube1, use the
   current stable releases that fit Kubernetes 1.35.8 (kOps 1.35.x,
   kubectl 1.35.x). Fix the exact numbers at implementation time.
2. `just ci sync-deploy-vars` publishes the variables from the registry:
   `PROD_KOPS_VERSION`, `PROD_KUBECTL_VERSION`, `PROD_PULUMI_VERSION`, and the
   matching `DEV_STAGING_*` set for the experiments cluster.
3. The rendered `tag-build.yaml` passes these variables to the composite. The
   `ci.yml` file passes nothing. Its defaults fit the experiments cluster
   (Kubernetes 1.28.8). The lab path stays frozen.

## Order of execution

1. P0.1 — dagger-of-dcr pull request.
2. P0.2 — workflows pull request. Canaries: modware-order and
   modware-annotation develop deploys. **Blocking.**
3. P0.3 — cleanup pull requests. Canaries again.
4. P0.4 — modware service plan, items 1 through 9, with the deltas listed
   below.

No tag deploy exists before the modware service plan. Every canary rides the
develop path to the experiments cluster. Phase 0 carries no production risk.

## Acceptance

- `just -n deploy-backend` in dagger-of-dcr prints a flag list. Every value
  has a name.
- The modware-order develop deploy runs green through the named-argument path.
- The modware-annotation develop deploy runs green through the buildless path.
- `grep -n "dotenv-load" Justfile` returns nothing after P0.3.
- The workflows pass `kops_version` as an input. An override test on one canary
  (a temporary input value) shows the new version in the deploy log.
- The dcr-kube1 registry entry names kOps 1.35.x and kubectl 1.35.x.

## Risks

- **Shared `@develop` workflows** — P0.2 changes the call style for every
  service repository at once. The defaults absorb the change. The canaries
  prove it. If a canary fails, revert the workflows merge; P0.1 keeps the old
  style working.
- **Two repositories, one window** — the sequence P0.1 then P0.2 keeps the
  window safe. Do not merge in the reverse order.
- **`.env` removal in P0.3** — any unknown caller that still writes `.env`
  breaks. The workflow files are the only known callers. Search both
  repositories for other callers before the merge.
- **Version drift between the registry and the Justfile defaults** — the
  registry is the source of truth from P0.4 on. The defaults serve only as
  fallback. Document this in the reference doc from modware plan item 5.

## Effect on the modware service plan

| Modware plan item | Effect |
|-------------------|--------|
| Item 1 — registry | Schema gains `kops_version`, `kubectl_version`, `pulumi_version` |
| Item 5 — CI credentials and variables | `sync-deploy-vars` also publishes the three version variables |
| Item 8 — pin the deploy path | Shrinks. P0.1 already made `--cluster-ops-ref` an argument. Pinning becomes: pass `--workflow-ref`, `--cluster-ops-ref`, and `dagger_ref` with tag values. No dagger-of-dcr code change remains |
| Items 2, 3, 4, 6, 7, 9 | No change |