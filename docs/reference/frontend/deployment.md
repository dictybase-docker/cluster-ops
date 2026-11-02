# Frontend Applications Deployment Details

Back to: [Frontend Applications Deploy Guide](../../frontend-deploy.md)

This document gives the details for the `frontend_application/` Pulumi project: the resources, the prerequisites, the stack file, the image tags, the routing, the deploy behavior, the CI deployment, and the error messages.

## Table of Contents

- [What the Project Creates](#what-the-project-creates)
- [Prerequisites](#prerequisites)
- [Create the Stack](#create-the-stack)
  - [Baseline Stack File](#baseline-stack-file)
  - [Keys That Change per Cluster](#keys-that-change-per-cluster)
- [Image Tags](#image-tags)
- [Routing](#routing)
  - [Reuse a Hostname from Another Cluster](#reuse-a-hostname-from-another-cluster)
- [Stack Files](#stack-files)
- [Deploy Behavior](#deploy-behavior)
- [CI Deployment](#ci-deployment)
- [Baked-in Endpoints](#baked-in-endpoints)
- [Tag Deploy Setup](#tag-deploy-setup)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

---

## What the Project Creates

The program creates two resources in the namespace for each entry in `properties.apps`. The default stack has four apps, so it creates eight resources. Each name comes from the app `name`.

| Order | Resource | Name | Details |
|-------|----------|------|---------|
| 1 | Deployment | `<name>-api-server` | One replica. Pod label `app: <name>-api-server`. One container `<name>` (`<image>:<tag>`). TCP port `properties.port`. |
| 2 | Service | `<name>-api` | Default type `ClusterIP`. Port `properties.port` to the container port. Depends on the Deployment. |

The program creates no Namespace, no Ingress, no Secret, and no probe. It sets no resource limits, no arguments, and no environment variables. The `namespace-bootstrap` project creates the namespace ([shared namespaces](../pulumi/namespaces.md)).

---

## Prerequisites

| Component | Resource in the namespace | Purpose | Guide |
|-----------|---------------------------|---------|-------|
| Namespace | `dev` or `prod` | Target namespace | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Gateway | `dictycr-gateway` | Public HTTPS routing. Needed for step 4 of the guide only. | [`gateway-deploy.md`](../../gateway-deploy.md) |

The project needs no datastore and no backend Service at deploy time. The program passes no connection setting to the containers. Each image holds the GraphQL endpoint and the other settings ([baked-in endpoints](#baked-in-endpoints)).

---

## Create the Stack

Run `scaffold-frontend-stack` to create a new cluster stack file from `config/templates/frontend-stack.yaml.tmpl` and the cluster registry entry (`config/clusters/<cluster>.yaml`).

```bash
just gcp-pulumi scaffold-frontend-stack
```

The recipe performs these actions:

1. Reads `namespace` and `kms_secrets_provider` from the registry entry that matches `--stack` (`$PULUMI_STACK`).
2. Refuses to overwrite an existing `frontend_application/Pulumi.<stack>.yaml`.
3. Initializes the stack with `pulumi stack init --secrets-provider` and appends the rendered `config:` block.
4. Checks that the file holds four apps, the registry namespace, and no `:latest` tag.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Must match a `stack:` key in `config/clusters/*.yaml` |
| `--port` / `-p` | No | `3000` | Container and Service port for every app. The Gateway route expects `3000`. |

When `frontend_application/Pulumi.<stack>.yaml` already exists, select the stack with:

```bash
just gcp-pulumi ensure-stack --folder frontend_application
```

### Baseline Stack File

The template has four apps, and each app has the placeholder tag `bootstrap`. Replace each tag before the first deploy ([image tags](#image-tags)).

```yaml
config:
  frontend_application:properties:
    apps:
      - image: dictybase/dicty-frontpage
        name: frontpage
        tag: bootstrap
      - image: dictybase/publication
        name: publication
        tag: bootstrap
      - image: dictybase/stock-center
        name: stockcenter
        tag: bootstrap
      - image: dictybase/genomepage
        name: genomepage
        tag: bootstrap
    logLevel: error
    namespace: prod
    port: 3000
```

**Keep the four app names.** The Service names `frontpage-api`, `stockcenter-api`, `publication-api`, and `genomepage-api` are the backends of the Gateway `frontend-route`. A different name leaves a route with no backend.

The program reads `logLevel` into its configuration but passes it to no container.

### Keys That Change per Cluster

| Key | Lab (`experiments`) | Production |
|-----|---------------------|------------|
| `namespace` | `dev` | `prod` |
| `apps.*.tag` | the last CI tag | an immutable `develop-<sha>` tag or a release tag |

The shipped lab stack files are older than the template. `Pulumi.experiments.yaml` uses the same key shape. `Pulumi.dev.yaml` uses the key `log-level`, and `Pulumi.staging.yaml` has no `frontend_application:properties` prefix and no `genomepage` app. Do not copy either file to a new cluster. Use `scaffold-frontend-stack`.

---

## Image Tags

Each app has its own tag at `properties.apps.<index>.tag`. The index follows the order of the stack file.

| Index | App | Image | Service |
|-------|-----|-------|---------|
| `0` | `frontpage` | `dictybase/dicty-frontpage` | `frontpage-api` |
| `1` | `publication` | `dictybase/publication` | `publication-api` |
| `2` | `stockcenter` | `dictybase/stock-center` | `stockcenter-api` |
| `3` | `genomepage` | `dictybase/genomepage` | `genomepage-api` |

`set-config` rewrites `apps.0.tag` to `apps[0].tag`, so `zsh` does not expand the brackets. Use an immutable tag. A mutable tag such as `latest` does not restart a pod when the image changes, and `scaffold-frontend-stack` rejects it.

To upgrade one app, change its tag and run `preview` and `create-resource` again. The other apps stay unchanged in the plan.

---

## Routing

The `frontend_application/` stack creates no Ingress. The Gateway stack owns the external routing.

| Path prefix | Backend Service | Port |
|-------------|-----------------|------|
| `/` | `frontpage-api` | `3000` |
| `/stockcenter` | `stockcenter-api` | `3000` |
| `/publication` | `publication-api` | `3000` |
| `/gene` | `genomepage-api` | `3000` |

The route table and the `enabled` toggle are in [Gateway details](../gateway/deployment.md#frontend-route). Deploy `frontend_application` before you turn on the route, so each backend Service exists. In-cluster clients reach a Service at `<name>-api.<namespace>.svc:3000`.

On the lab `dcr-experiments` cluster, the separate `ingress/` project routes the front-end hostnames through `nginx`. That project is outside this guide.

### Reuse a Hostname from Another Cluster

Use this procedure when the public hostname already resolves to a different IP address. The `just gateway deploy` recipe does not change DNS, so the old site keeps the traffic until you change the `A` record.

1. Lower the TTL of the `A` record one day before the cutover.
2. Run `just gateway deploy`. The Gateway and the route exist, but the hostname still points to the old IP address.
3. Run `just gateway show-ip`. Change the `A` record to this IP address.
4. Run `just gateway verify`. It passes when the TLS `Secret` exists.
5. Raise the TTL again.

The ACME `Issuer` uses HTTP-01. cert-manager can issue the certificate only after the hostname resolves to the new Gateway IP address. HTTPS on the new Gateway fails for about 1–2 minutes after the change. If cert-manager tried before the change, the challenge failed and a backoff delays the retry. Delete the pending `Order` to force a new attempt.

Resolvers keep the old IP address until the TTL expires. To roll back, set the `A` record to the old IP address.

---

## Stack Files

The stack file `frontend_application/Pulumi.<stack>.yaml` holds the image tags and the namespace. The file holds no readable secret. The `encryptedkey` line is the data key that the cluster KMS key protects.

Commit and push the stack file after each change. The pushed file is the record of which tag runs on the cluster. Push the `gateway/Pulumi.<stack>.yaml` file in the same commit when you set the frontend hostname.

---

## Deploy Behavior

`create-resource` drives the Pulumi Automation API, not the interactive CLI. It asks for no confirmation, and it hides secret values in the logs and in the error text. The recipe previews the change first, and then it does one of three things:

- It stops and lists the URNs when the plan changes a protected resource.
- It makes no update when the plan has no change.
- It applies the saved plan from that preview, so the applied plan is the plan that you read.

No recipe waits for the rollout. Run the commands in [Verification](#verification) yourself.

### Recipe Flags

| Recipe | Flag | Required | Default | Notes |
|--------|------|----------|---------|-------|
| all | `--folder` / `-f` | Yes | — | Pass `frontend_application` |
| all | `--stack` / `-s` | No | `$PULUMI_STACK` | Every recipe except `ensure-stack` falls back to the literal `dev` outside a cluster shell. `ensure-stack` fails instead. |
| `ensure-stack` | `--stack` / `-s` | No | `$PULUMI_STACK` | Creates the stack when `Pulumi.<stack>.yaml` exists. Otherwise it only selects a stack that exists, and it fails when there is none. |
| `set-config` | `--key` / `-k` | Yes | — | Path under `frontend_application:`, for example `properties.apps.0.tag` |
| `set-config` | `--value` / `-v` | Yes | — | Plain value |

---

## CI Deployment

A push to `develop` in `dictyBase/frontendx` starts `.github/workflows/ci.yml`. That workflow calls the composite deploy workflow with `application_type: frontend`, `project: frontend_application`, `stack: experiments`, and `runner: dcr-frontendx-beefy`. The `app`, `docker_image`, and `dockerfile` inputs are colon-separated lists with one entry per app.

The composite workflow builds each image from `docker/Dockerfile.<app>` and publishes `dictybase/<image>:<tag>`. It clones cluster-ops at branch **`develop`** and sets `properties.apps[<n>].tag` for every list entry. **All apps get the same tag.** Then it runs `pulumi up`.

| Input | Value in `ci.yml` |
|-------|-------------------|
| `app` | `frontpage:stockcenter:publication:genomepage` |
| `docker_image` | `dicty-frontpage:stock-center:publication:genomepage` |
| `dockerfile` | `docker/Dockerfile.frontpage:docker/Dockerfile.stock-center:docker/Dockerfile.publication:docker/Dockerfile.genomepage` |

**The list must have as many entries as `properties.apps`.** A shorter list leaves the last apps on their old tag.

**Push order: send each change to `frontend_application/*.go` or `frontend_application/Pulumi.<stack>.yaml` to cluster-ops `develop` before you merge or tag `dictyBase/frontendx`.** Otherwise the CI run clones the old tree and deploys stale config.

The workflow files are not in this repository, so this section reports the contract, not a verified file state.

---

## Baked-in Endpoints

The images hold all endpoint settings. The program passes none to the pods. The Dockerfile in `frontendx` builds each app with `--mode=${BUILD_STATE}`. The composite workflow sets `BUILD_STATE` to the deployment environment, so a production tag reads `apps/<app>/.env.production`.

Those files name lab and staging hosts, not production hosts. This table shows the `GRAPHQL` and `LOGTO` values when this document was written.

| App | GraphQL endpoint in `.env.production` | Logto endpoint in `.env.production` |
|-----|---------------------------------------|-------------------------------------|
| `dicty-frontpage` | `https://graphql.dictybase.dev` | `https://logto.dictybase.dev` |
| `stock-center` | `https://graphql.dictybase.dev` | `https://logto.dictybase.dev` |
| `publication` | `https://graphql.dictybase.dev` | `https://logto.dictybase.dev` |
| `genome-page` | `https://staginggraphql.dictycr.org` | `https://staginglogto.dictycr.org` |

**A production tag deploy with unpatched files makes the production front ends call the lab or staging back ends.** The front-end URLs (`VITE_APP_FRONTPAGE_URL` and the similar keys) also name `dictybase.dev`, and `apps/genome-page/.env.production` uses legacy `NEXT_PUBLIC_*` keys unless replaced with `VITE_*`. Run `just frontend pr-deploy-workflow` before you cut the first production tag. The recipe reads the frontend and GraphQL hosts from `gateway/Pulumi.<stack>.yaml`, updates `apps/*/.env.production`, and opens the pull request in `dictyBase/frontendx`.

The `develop` push to the `experiments` stack uses the development build mode (`apps/<app>/.env.development`), so `just frontend pr-deploy-workflow` leaves `.env.development` untouched.

---

## Tag Deploy Setup

The CI path above deploys the lab `experiments` stack only. `frontendx` has no entry in `config/services.yaml`, so `just ci deploy-backend-services` and `just ci pr-deploy-workflows` exit with `no services matched` for this repository. Use the single-repository CI recipes and `just frontend pr-deploy-workflow` from the production cluster-env sub-shell.

```bash
just ci create-deploy-key
just ci render-composite-deploy
just ci set-deploy-secret --repos dictyBase/frontendx
just ci sync-deploy-vars --repos dictyBase/frontendx
just frontend pr-deploy-workflow
```

| Recipe | What it changes | Risk | Details |
|--------|-----------------|------|---------|
| `create-deploy-key` | Creates or reuses SA `deployer` and `credentials/<cluster>/deployer.json`, then runs the credential preflight | A new key accumulates in the project. Re-runs reuse the file. | [Deploy credentials](../backend/ci-credentials.md) |
| `render-composite-deploy` | Sets org variable `PROD_PULUMI_STATE_STORAGE` and renders `composite-deploy.yaml` | None until a pull request merges the file into `dictyBase/workflows` | [Workflow render](../backend/render-deploy-workflows.md) |
| `set-deploy-secret` | Adds `frontendx` to the repository list of the org secret `PROD_DEPLOY_SA_KEY` | The recipe sends the current list again, so no repository loses access | [CI variables](../backend/ci-variables.md#set-deploy-secret) |
| `sync-deploy-vars` | Sets the five `PROD_*` repository variables from the registry entry | One upsert per variable | [CI variables](../backend/ci-variables.md#sync-deploy-vars) |
| `pr-deploy-workflow` | Renders `bin/ci-render/frontendx-tag-build.yaml` (`runner: dcr-frontendx-prod`), sets `runner: dcr-frontendx-beefy` in `.github/workflows/ci.yml`, updates `apps/*/.env.production` from `gateway/Pulumi.<stack>.yaml`, removes `.github/workflows/staging.yml`, and opens a pull request in `dictyBase/frontendx` | None until you merge the pull request | [Workflow render](../backend/render-deploy-workflows.md#render-tag-deploy-flags) |

`pr-deploy-workflow` calls `render-tag-deploy` with `--application-type frontend` and `--runner dcr-frontendx-prod`, which makes the composite run the frontend deploy on the production runner. The default `microservice` runs the backend deploy, which sets `properties.image.tag` and changes no front-end image. Check that `dcr-frontendx-prod` can reach the production cluster and `dcr-frontendx-beefy` can reach the `experiments` cluster.

To finish the setup, merge the pull request in `dictyBase/frontendx`, fast-forward `master` to `develop`, and push a release tag. Do not change `apps/*/.env.development`. It keeps the `develop` push path on the `experiments` stack.

Two conditions must hold before a tag deploys, and these recipes check neither one. First, the composite workflow must select `secrets.PROD_DEPLOY_SA_KEY` and `vars.PROD_PULUMI_STATE_STORAGE` for `inputs.environment == 'production'`. Second, `frontend_application/Pulumi.<prod-stack>.yaml` must exist on cluster-ops `develop`. No operator ran a full tag deploy for this repository, so watch the first production tag.

---

## Verification

`just frontend verify` is a read-only check. It changes nothing, and you can run it at any time. It reads `frontend_application/Pulumi.<stack>.yaml`, so no flag is necessary in the cluster-env sub-shell.

```bash
just frontend verify
just gateway verify
```

The recipe performs these actions for each entry in `properties.apps`:

1. Fails when the tag is the placeholder `bootstrap`.
2. Waits for `deployment/<name>-api-server` to roll out.
3. Compares the running image with `<image>:<tag>` from the stack file.
4. Checks that `service/<name>-api` exists.

It prints one `PASS` or `FAIL` line for each app and checks every app before it exits. The exit code is non-zero when any app fails. On success it prints `Next: just gateway verify`.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Selects `frontend_application/Pulumi.<stack>.yaml` |
| `--namespace` / `-n` | No | `properties.namespace` from the stack file | Use it only to check another namespace |
| `--timeout` / `-t` | No | `120s` | Rollout wait for each Deployment. Accepts `s`, `m`, or `h`. |

The recipe does not call Pulumi and does not need the Pulumi credentials. It needs `KUBECONFIG` and `yq`. The Gateway check passes only after the DNS record resolves and `cert-manager` writes `frontend-tls` ([Gateway details](../gateway/deployment.md#frontend-route)).

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `ERROR: stack '<stack>' does not exist in frontend_application, and there is no Pulumi.<stack>.yaml to initialize it from` | First deploy on this cluster. `ensure-stack` creates a stack only from a stack file that exists. | Run `just gcp-pulumi scaffold-frontend-stack` ([create the stack](#create-the-stack)). |
| `ERROR: frontend_application/Pulumi.<stack>.yaml already exists — refusing to overwrite` | The stack file exists. | Run `ensure-stack`, or edit the file with `set-config`. |
| `failed to read frontend config` | `properties` is absent, or the key prefix is not `frontend_application:`. | Compare the file with the [baseline stack file](#baseline-stack-file). |
| `FAIL <app>: tag is the placeholder 'bootstrap'` | The first deploy ran before you replaced the placeholder tags. | Set the tag ([image tags](#image-tags)) and apply again. |
| `FAIL <app>: deployment/<app>-api-server did not roll out` | The pod does not become ready in the timeout. | Run `kubectl -n <namespace> describe deployment <app>-api-server`, or raise `--timeout`. |
| `FAIL <app>: running image <a>, stack file says <b>` | The stack file changed and `create-resource` did not run, or a CI run set a different tag. | Run `preview` and `create-resource` again. |
| `FAIL <app>: service/<app>-api not found` | The Service was deleted or the app name differs. | Run `create-resource` again ([deploy behavior](#deploy-behavior)). |
| Pod `ImagePullBackOff` with a real tag | The tag does not exist on Docker Hub for that image. | Check the tag in the image registry, then fix `properties.apps.<index>.tag`. |
| A CI run changes only some apps | The `app` list has fewer entries than `properties.apps`. | Make the three lists match the four apps ([CI deployment](#ci-deployment)). |
| The production site calls `dictybase.dev` or staging hosts | The image was built from `.env.production` with lab hosts ([baked-in endpoints](#baked-in-endpoints)). | Fix the hosts in `frontendx`, cut a new tag, and deploy again. |
| A tag deploy changes no image | The rendered workflow lacks `application_type: frontend`, so the backend deploy ran. | Render again with `--application-type frontend` ([tag deploy setup](#tag-deploy-setup)). |
| `404` or `no healthy upstream` on a path prefix | The Service of that app does not exist, or the app name differs from the Gateway route. | Run `just frontend verify`. Keep the four app names ([baseline stack file](#baseline-stack-file)). |
| `just gateway verify` reports a missing `frontend-tls` Secret | The DNS `A` record does not resolve yet, so `cert-manager` cannot finish the challenge. | Point the frontend hostname at the Gateway IP address ([Gateway guide](../../gateway-deploy.md)). |
