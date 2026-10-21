# GraphQL Server Deployment Details

Back to: [GraphQL Server Deploy Guide](../../graphql-deploy.md)

This document gives the details for the `graphql_server/` Pulumi project: the resources, the prerequisites, the configuration keys, the optional Ingress, the optional Logto authentication, the CI path, and the error messages.

## Table of Contents

- [What the Project Creates](#what-the-project-creates)
- [Prerequisites](#prerequisites)
- [In-Cluster Service Discovery](#in-cluster-service-discovery)
- [Create the Stack](#create-the-stack)
- [Authentication Mode](#authentication-mode)
- [Ingress Mode](#ingress-mode)
- [MinIO Secret Keys](#minio-secret-keys)
- [Deploy Behavior](#deploy-behavior)
- [CI Deployment](#ci-deployment)
- [Tag Deploy Setup](#tag-deploy-setup)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

---

## What the Project Creates

The program creates two resources in the namespace, and one more resource when you turn the Ingress on. Each name comes from `properties.name`, which is `graphql` on every shipped stack.

| Order | Resource | Name | Details |
|-------|----------|------|---------|
| 1 | Deployment | `graphql-api-server` | Pod label `app: graphql-api-server`. One container `graphql-container`. TCP port `8080`. |
| 2 | Service | `graphql-api` | Type `NodePort`. Port `8080` to target port `8080`. Depends on the Deployment. |
| 3 | Ingress | `graphql-ingress` | **Optional.** The program creates it only when `properties.ingress.enabled` is `true`. See [ingress mode](#ingress-mode). |

The program creates no Namespace and no Kubernetes Secret. The `namespace-bootstrap` project creates the namespace ([shared namespaces](../pulumi/namespaces.md)). The `minio` project creates the MinIO credentials Secret ([MinIO install](../minio/install.md)).

---

## Prerequisites

Each upstream Service must exist in the target namespace before the `graphql-api-server` pod starts.

| Component | Resource in the namespace | Purpose | Guide |
|-----------|---------------------------|---------|-------|
| Namespace | `dev` or `prod` | Target namespace | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Redis | Service `redis` (`:6379`) | Cache and dataloader store | [`redis-deploy.md`](../../redis-deploy.md) |
| MinIO | Service `minio` (`:9000`), one Secret, bucket `editor` | S3 asset storage | [`minio-deploy.md`](../../minio-deploy.md) |
| modware gRPC | Services `stock-api` (`:9345`), `order-api`, `annotation-api`, `anno-feat-api`, and `content-api` (all `:9250`) | Stock, order, annotation, feature, and content queries | [`backend-service-deploy.md`](../../backend-service-deploy.md) |
| `nginx` controller and `kcert` | Ingress controller and TLS issuer | Public HTTPS. **Only when `ingress.enabled` is `true`.** | — |
| Logto | Public OIDC and JWKS endpoint | JWT checks and management API. Only when `authEnabled` is `true`. | [`logto-deploy.md`](../../logto-deploy.md) |

Each backend project names its Service `<appName>-api`. The project `modware-annotation-feature` has no production stack file in this repository, so `anno-feat-api` needs a new stack file before the first production deploy.

### Check the Prerequisites

Redis and MinIO have verify recipes. The five gRPC Services have none, so query them with `kubectl`.

```bash
just redis verify
just minio verify
kubectl -n <namespace> get svc stock-api order-api annotation-api anno-feat-api content-api
```

Both verify recipes default to `--namespace prod`, Secret `minio-root`, and `--pool database --node-count 3`. On the lab `experiments` cluster, pass `--namespace dev` and `--secret minio`. Deploy a missing gRPC Service with `just ci deploy-backend-services`. Create a missing `editor` bucket with [MinIO import](../minio/import.md).

---

## In-Cluster Service Discovery

Pulumi passes no host or port arguments to the container. The `graphql-server` binary reads the standard Kubernetes service-discovery variables instead.

| Service | Environment variables the binary reads | Flag that gets the value |
|---------|---------------------------------------|--------------------------|
| `stock-api` | `STOCK_API_SERVICE_HOST` / `STOCK_API_SERVICE_PORT` | `--stock-grpc-host` / `--stock-grpc-port` |
| `order-api` | `ORDER_API_SERVICE_HOST` / `ORDER_API_SERVICE_PORT` | `--order-grpc-host` / `--order-grpc-port` |
| `annotation-api` | `ANNOTATION_API_SERVICE_HOST` / `ANNOTATION_API_SERVICE_PORT` | `--annotation-grpc-host` / `--annotation-grpc-port` |
| `anno-feat-api` | `ANNO_FEAT_API_SERVICE_HOST` / `ANNO_FEAT_API_SERVICE_PORT` | `--feature-annotation-grpc-host` / `--feature-annotation-grpc-port` |
| `content-api` | `CONTENT_API_SERVICE_HOST` / `CONTENT_API_SERVICE_PORT` | `--content-grpc-host` / `--content-grpc-port` |
| `redis` | `REDIS_SERVICE_HOST` / `REDIS_SERVICE_PORT` | `--redis-master-service-host` / `--redis-master-service-port` |
| `minio` | `MINIO_SERVICE_HOST` / `MINIO_SERVICE_PORT` | `--s3-server` / `--s3-server-port` |

Kubernetes writes these variables into a pod only when the Service exists in the same namespace before the pod starts. If a Service starts later, the pod exits with `Required flag ... not set`. Restart the Deployment after the Service exists.

```bash
kubectl -n <namespace> rollout restart deployment/graphql-api-server
```

---

## Create the Stack

The repository ships the lab stack file `Pulumi.experiments.yaml` and a retired `Pulumi.dev.yaml`. The program does not read the retired schema. No production stack file exists yet, so a new cluster needs these three steps.

**1. Create the stack.** This recipe needs no stack file. The backend bucket and the KMS key come from `config/clusters/<cluster>.yaml`.

```bash
just gcp-pulumi new-stack --folder graphql_server
```

**2. Write the `config:` block** into `graphql_server/Pulumi.<stack>.yaml`. Use the template below. Keep the `secretsprovider:` and `encryptedkey:` lines at the top of the file. Edit the file by hand, because each `set-config` call writes one leaf key only.

**3. Encrypt the name of the MinIO Secret.** Only `set-secret` can write the encrypted block.

```bash
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.minio.name --value minio-root
```

Then go back to the guide for the authentication mode, the Ingress mode, `preview`, and `create-resource`.

`ensure-stack` cannot do step 1. It creates a stack only when `graphql_server/Pulumi.<stack>.yaml` is already on disk. Without that file it fails with `there is no Pulumi.<stack>.yaml to initialize it from`. The two recipes in that error message also fail across GCP projects:

- `fork-stack` copies `Pulumi.experiments.yaml`. The copy keeps the `dcr-experiments` KMS header and every encrypted value, and the target cluster cannot decrypt them.
- `new-stack-from` reads the source stack through the target backend. The `experiments` stack is in a different backend, so the read fails with `no stack named 'experiments' found`.

Both recipes stay useful inside one GCP project and one backend.

### Baseline Stack File

This template has **no `ingress:` block** and no auth keys, so the stack creates the Deployment and the Service only. Replace each `<...>` value. The production hostnames are an operator input, because no file in this repository records one.

```yaml
config:
  graphql_server:properties:
    authEnabled: false
    allowedOrigins:
      - https://<frontend-host>
    endpoints:
      organism: https://raw.githubusercontent.com/dictyBase/migration-data/master/downloads/organisms-with-citations.staging.json
      publication: https://www.ebi.ac.uk/europepmc/webservices/rest/search
      store: https://<storage-host>
    image:
      name: dictybase/graphql-server
      tag: <immutable-tag>
    logLevel: error
    name: graphql
    namespace: prod
    port: 8080
    s3Bucket:
      name: editor
      path: assets
    secrets:
      minio:
        passKey: rootPassword
        userKey: rootUser
```

For the full shape with auth on and Ingress on, read `graphql_server/Pulumi.experiments.yaml`.

### Keys That Change per Cluster

| Key | Lab (`experiments`) | Production |
|-----|---------------------|------------|
| `namespace` | `dev` | `prod` |
| `ingress.enabled` | `true` | omitted, so `false` |
| `ingress.hosts` | `graphql.dictybase.dev` | the production host, when the Ingress is on |
| `ingress.tlsSecret` | `graphql-tls-secret` | the per-cluster TLS Secret |
| `endpoints.auth` and `endpoints.store` | `*.dictybase.dev` | the production Logto and storage hosts |
| `allowedOrigins` | `*.dictybase.dev` | the production front-end origins |
| `secrets.minio.*` | `minio` / `user` / `pass` | `minio-root` / `rootUser` / `rootPassword` |
| `image.tag` | the last tag from CI | an immutable `develop-<sha>` tag or a release tag |

**The lab stack file sets `authEnabled: true` and `ingress.enabled: true`.** A stack file that you copy from it starts with auth on and Ingress on, not with the defaults.

---

## Authentication Mode

The default value of `properties.authEnabled` is **`false`**. An absent key also gives `false`. **An absent key is a supported configuration, not an incomplete one.**

With `authEnabled: false` the container gets one authentication argument, `--auth-enabled=false`. The server then adds no JWT handler, so each request reaches the resolvers without a token. A resolver that calls the Logto management API returns the GraphQL error `authentication is disabled`. The program reads no value from `endpoints.auth` or `secrets.auth.*`, so a stale value does no harm, and the cluster needs no Logto deployment.

| Key | Required when `authEnabled` is `true` | Set it with | Purpose |
|-----|---------------------------------------|-------------|---------|
| `authEnabled` | — (this key is the switch) | `set-config` | Turns the JWT handler and the Logto client on |
| `endpoints.auth` | Yes | `set-config` | Base HTTP URL of the Logto API |
| `secrets.auth.appId` | Yes | `set-secret` | API identifier of the Logto application |
| `secrets.auth.appSecret` | Yes | `set-secret` | Secret for access to the Logto API |
| `secrets.auth.jwksURI` | Yes | `set-secret` | URL of the JWK public key set |
| `secrets.auth.jwtIssuer` | Yes | `set-secret` | Expected issuer of the token |
| `secrets.auth.jwtAudience` | Yes | `set-secret` | Expected audience of the token |

`set-config` writes the value with `pulumi config set --path`, which keeps the scalar type. The value `true` becomes the YAML boolean `true`, not the string `"true"`. No recipe removes a key, so delete an unwanted key from the stack file by hand.

Set the endpoint and the five secrets **before** you set `authEnabled` to `true`. Two checks fail closed, and they do not cover the same keys:

| Check | When it runs | Condition | Result |
|-------|--------------|-----------|--------|
| Pulumi config check | each `preview` and `create-resource`, before any resource | `authEnabled` is `true` and one of the five `secrets.auth.*` values is empty | `authEnabled=true but secrets.auth is missing <names>`. The program makes no plan. |
| Container startup check | when the pod starts, before the router | `--auth-enabled=true` and one of the six auth arguments is empty | `--<flag> is required when auth is enabled` and exit code 2. The pod goes to `CrashLoopBackOff`. |

The Pulumi check does not look at `endpoints.auth`. A stack with `authEnabled: true` and an empty `endpoints.auth` applies cleanly, and then the pod goes to `CrashLoopBackOff`. Set the endpoint in the same change as the secrets.

Pulumi always passes the boolean as one argument, `--auth-enabled=true` or `--auth-enabled=false`. The binary uses `urfave/cli` v1, which reads a space-separated value as a positional argument. Pulumi passes the argument in both modes, so an old image without the flag fails at startup with `flag provided but not defined: -auth-enabled`.

---

## Ingress Mode

`properties.ingress.enabled` decides if this project owns the external routing. The default value is **`false`**. An absent `ingress.enabled` key gives `false`, and an absent `ingress:` block also gives `false`. **An absent `ingress:` block is a supported configuration, not an incomplete one.**

| `ingress.enabled` | Resources the stack creates | Who routes the external traffic | Keys the program reads |
|-------------------|-----------------------------|---------------------------------|------------------------|
| absent or `false` (default) | Deployment `graphql-api-server` and Service `graphql-api` (`:8080`) | a central Gateway stack, in front of `graphql-api:8080` | none, so a stale `ingress.*` value does no harm |
| `true` | the two resources above and Ingress `graphql-ingress` | this stack, through the `nginx` controller with a `kcert` TLS Secret | `hosts`, `path`, `tlsSecret` (required), and `label` |

**The central Gateway stack is not in this repository.** The `ingress/` project renders its `frontend` block only, and no Gateway API program exists here. With `ingress.enabled: false`, nothing in cluster-ops routes external traffic to `graphql-api`. In-cluster clients still reach the Service at `graphql-api.<namespace>.svc:8080`.

### Turn the Ingress On

Set `hosts`, `path`, and `tlsSecret` **before** you set `enabled` to `true`. The config check fails closed with `ingress.enabled=true but ingress is missing hosts, path, tlsSecret` and names only the empty keys.

The program does not check `label`, but it always writes one label `<label.name>: <label.value>` on the Ingress. An empty `label.name` sends an empty label key, and the API server rejects it at apply time. Set `kcert.dev/ingress: managed` so that `kcert` issues the TLS Secret named in `tlsSecret`.

```bash
just gcp-pulumi set-config --folder graphql_server --key properties.ingress.hosts[0] --value <graphql-host>
just gcp-pulumi set-config --folder graphql_server --key properties.ingress.path --value /
just gcp-pulumi set-config --folder graphql_server --key properties.ingress.tlsSecret --value <graphql-tls-secret>
just gcp-pulumi set-config --folder graphql_server --key properties.ingress.label.name --value kcert.dev/ingress
just gcp-pulumi set-config --folder graphql_server --key properties.ingress.label.value --value managed
just gcp-pulumi set-config --folder graphql_server --key properties.ingress.enabled --value true
just gcp-pulumi preview --folder graphql_server
just gcp-pulumi create-resource --folder graphql_server
```

The Ingress sends each host in `hosts` at `path` with path type `Prefix` to Service `graphql-api` on port `8080`. The ingress class is `nginx`.

### Turn the Ingress Off

**CAUTION: This deletes the Ingress. The public hostnames no longer point to this Service.** On a stack that has `graphql-ingress`, a change of `properties.ingress.enabled` to `false` makes the next plan delete the Ingress. Put the replacement Gateway route in place first. Read the `preview` output before you apply it.

```bash
just gcp-pulumi set-config --folder graphql_server --key properties.ingress.enabled --value false
just gcp-pulumi preview --folder graphql_server
```

---

## MinIO Secret Keys

The program injects two environment variables into the container from one Kubernetes Secret. Both variables read the Secret named in `properties.secrets.minio.name`: `minio` on the lab cluster, `minio-root` on production.

| Container variable | Config key for the Secret key | Lab value | Production value |
|--------------------|-------------------------------|-----------|------------------|
| `ACCESS_KEY` | `properties.secrets.minio.userKey` | `user` | `rootUser` |
| `SECRET_KEY` | `properties.secrets.minio.passKey` | `pass` | `rootPassword` |

```bash
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.minio.name --value minio-root
just gcp-pulumi set-config --folder graphql_server --key properties.secrets.minio.userKey --value rootUser
just gcp-pulumi set-config --folder graphql_server --key properties.secrets.minio.passKey --value rootPassword
```

All three values are names, not credentials. The credentials stay in the Kubernetes Secret that the `minio` stack writes ([MinIO deploy](../../minio-deploy.md)). Encrypted storage and plain storage both work, because the program only copies the names into the pod specification.

---

## Deploy Behavior

`create-resource` drives the Pulumi Automation API, not the interactive CLI. It asks for no confirmation, and it hides secret values in the logs and in the error text. The recipe previews the change first, and then it does one of three things:

- It stops and lists the URNs when the plan changes a protected resource.
- It makes no update when the plan has no change.
- It applies the saved plan from that preview, so the applied plan is the plan that you read.

No recipe waits for the rollout. Run the commands in [Verification](#verification) yourself. For an upgrade, change `properties.image.tag` and run `preview` and `create-resource` again.

### Recipe Flags

| Recipe | Flag | Required | Default | Notes |
|--------|------|----------|---------|-------|
| all | `--folder` / `-f` | Yes | — | Pass `graphql_server` |
| all | `--stack` / `-s` | No | `$PULUMI_STACK` | Every recipe except `ensure-stack` falls back to the literal `dev` outside a cluster shell. `ensure-stack` fails instead. |
| `ensure-stack` | `--stack` / `-s` | No | `$PULUMI_STACK` | Creates the stack when `Pulumi.<stack>.yaml` exists. Otherwise it only selects a stack that exists, and it fails when there is none. |
| `set-config` / `set-secret` | `--key` / `-k` | Yes | — | Path under `graphql_server:`, for example `properties.image.tag` |
| `set-config` / `set-secret` | `--value` / `-v` | Yes for `set-config`, no for `set-secret` | hidden prompt on `set-secret` | Omit it on `set-secret` to keep the value out of the shell history |
| `set-config` | `--plaintext` | No | unset | Accepted but unused. `set-config` always stores the value plain. Only the retired `set-config-shell` reads the flag. |

---

## CI Deployment

A push to `develop` in `dictyBase/graphql-server` starts `.github/workflows/ci.yml`. That workflow runs the unit tests and then calls the composite deploy workflow with `app: graphql-server`, `project: graphql_server`, `stack: experiments`, and `docker_image: graphql-server`. The composite workflow builds `dictybase/graphql-server:<ref>-<sha>`, clones cluster-ops at branch **`develop`**, sets `properties.image.tag`, and runs `pulumi up`.

**Push order: send each change to `graphql_server/*.go` or `graphql_server/Pulumi.<stack>.yaml` to cluster-ops `develop` before you merge or tag `dictyBase/graphql-server`.** Otherwise the CI run clones the old tree and deploys stale config.

The workflow files in the service repository are not in this repository, so this section reports the contract, not a verified file state.

---

## Tag Deploy Setup

The CI path above deploys the lab `experiments` stack only. Production rollouts stay manual until the service repository carries a tag workflow. `graphql-server` has no entry in `config/services.yaml`, so `just ci deploy-backend-services` and `just ci pr-deploy-workflows` exit with `no services matched` for this repository. Use the three single-repository recipes from the production cluster-env sub-shell.

```bash
just ci set-deploy-secret --repos dictyBase/graphql-server
just ci sync-deploy-vars --repos dictyBase/graphql-server
just ci render-tag-deploy --app graphql-server --project graphql_server --out bin/ci-render/graphql-server-tag-build.yaml
```

| Recipe | What it changes | Risk | Details |
|--------|-----------------|--------------|---------|
| `set-deploy-secret` | Adds `graphql-server` to the repository list of the org secret `PROD_DEPLOY_SA_KEY` and uploads `credentials/<cluster>/deployer.json` | The recipe reads the current list and sends it again, so one run never removes the modware repositories | [CI variables](../backend/ci-variables.md#set-deploy-secret) |
| `sync-deploy-vars` | Sets the five `PROD_*` repository variables from the registry entry | One upsert per variable. It warns, and it does not fail, when the repository cannot see `PROD_DEPLOY_SA_KEY`. | [CI variables](../backend/ci-variables.md#sync-deploy-vars) |
| `render-tag-deploy` | Writes `bin/ci-render/graphql-server-tag-build.yaml` in cluster-ops only | None until you merge the pull request below | [workflow render](../backend/render-deploy-workflows.md) |

`render-tag-deploy` builds the Docker Hub image name from the project folder and replaces `_` with `-`. The value `graphql_server` gives `graphql-server`, so the `--docker-image` flag is not necessary. `--stack` defaults to `$PULUMI_STACK` and must match a `stack:` key in `config/clusters/*.yaml`. A wrong value fails with `no registry entry with stack '<name>'`. The template writes `dockerfile: build/package/Dockerfile` and `environment: production` as fixed values, so check the Dockerfile path in the service repository.

To finish the setup, open a pull request against `dictyBase/graphql-server`:

1. Copy `bin/ci-render/graphql-server-tag-build.yaml` to `.github/workflows/tag-build.yaml`. Replace the tag workflow that is there, because the composite workflow builds the image itself.
2. Delete `.github/workflows/staging-build.yaml` in the same pull request. There is no staging environment.
3. Do not change `.github/workflows/ci.yml`. It keeps the develop path on the `experiments` stack.

Two conditions must hold before a tag deploys, and these recipes check neither one. First, the composite workflow must select `secrets.PROD_DEPLOY_SA_KEY` and `vars.PROD_PULUMI_STATE_STORAGE` for `inputs.environment == 'production'`. Second, `graphql_server/Pulumi.<prod-stack>.yaml` must exist on cluster-ops `develop`. `scripts/test-render-tag-deploy.sh` covers the render only. No operator ran a full tag deploy for this repository, so watch the first production tag.

---

## Verification

The cluster-env sub-shell exports `KUBECONFIG`, but it sets no default namespace. Give `-n <namespace>` to each command: `prod` on production, `dev` on the lab `experiments` stack.

```bash
# 1. Rollout and image. The tag must match properties.image.tag.
kubectl -n <namespace> rollout status deployment/graphql-api-server
kubectl -n <namespace> get deploy graphql-api-server -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'

# 2. Startup log. It reports the authentication state.
kubectl -n <namespace> logs deployment/graphql-api-server --tail=20

# 3. Service. graphql-api on port 8080 is always present.
kubectl -n <namespace> get svc graphql-api

# 4. Ingress. Run this only when the stack sets ingress.enabled to true.
kubectl -n <namespace> get ingress graphql-ingress
```

The log line reads `going to start graphql server with authentication enabled <true|false>, jwt-audience ... and jwt-issuer ...`. With the Ingress on, step 4 must list the `ingress.hosts` entries and an address from the `nginx` controller. With the default `ingress.enabled: false`, step 4 returns `NotFound`, and you verify the external route on the Gateway stack instead.

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `ERROR: stack '<stack>' does not exist in graphql_server, and there is no Pulumi.<stack>.yaml to initialize it from` | First deploy on this cluster. `ensure-stack` creates a stack only from a stack file that exists. | Run `just gcp-pulumi new-stack --folder graphql_server`, then write the `config:` block ([create the stack](#create-the-stack)). Ignore the `fork-stack` and `new-stack-from` hints in the message. |
| `failed to read graphql config: missing required configuration variable 'graphql_server:properties'` | The stack exists in the backend, but the stack file has no `config:` block. | Paste the [baseline stack file](#baseline-stack-file) under the `secretsprovider:` header. Run `preview` again. |
| `ingress.enabled=true but ingress is missing hosts, path, tlsSecret`, or any subset of those names | `ingress.enabled` is `true`, and one of the three keys is empty. | Set the named keys, or set `properties.ingress.enabled` to `false` ([ingress mode](#ingress-mode)). |
| `preview` plans to delete `graphql-ingress` | An operator set `ingress.enabled` to `false`, or removed the `ingress:` block. | This is correct only when a Gateway route replaces the Ingress. Otherwise set `ingress.enabled` back to `true` before you apply ([turn the ingress off](#turn-the-ingress-off)). |
| The Ingress apply fails on an invalid `metadata.labels` key | `ingress.enabled` is `true`, and `ingress.label.name` is empty. | Set `properties.ingress.label.name` to `kcert.dev/ingress` and `properties.ingress.label.value` to `managed`. |
| `authEnabled=true but secrets.auth is missing ...` | `authEnabled` is `true`, and one of the five `secrets.auth.*` keys is empty. | Set all five secrets with `just gcp-pulumi set-secret`, or set `authEnabled` to `false` ([authentication mode](#authentication-mode)). |
| Pod `CrashLoopBackOff` with `--auth-api-endpoint is required when auth is enabled` | `authEnabled` is `true`, and `properties.endpoints.auth` is empty. The Pulumi check does not look at that key. | Set `properties.endpoints.auth` to the Logto URL and apply again. |
| Pod `CrashLoopBackOff` with `Required flag "stock-grpc-host" not set`, or the same error for `order`, `annotation`, `feature-annotation`, or `content` | One gRPC Service did not exist when the pod started. | Deploy the missing Service, then run `kubectl -n <namespace> rollout restart deployment/graphql-api-server`. |
| Pod `CrashLoopBackOff` with `flag provided but not defined: -auth-enabled` | The image is older than the `--auth-enabled` flag. | Set `properties.image.tag` to a current image from `develop`. |
| Pod `CreateContainerConfigError` on Secret `minio` or `minio-root` | `properties.secrets.minio.name`, `userKey`, or `passKey` does not match the Secret in the namespace. | Run `kubectl -n <namespace> get secret`. On production, use `minio-root` with the keys `rootUser` and `rootPassword` ([MinIO secret keys](#minio-secret-keys)). |
| Pod `CrashLoopBackOff` on the Redis connection | Service `redis` was absent when the pod started, or Redis is down. | Run `just redis verify`, then restart the Deployment. |
| `502 Bad Gateway` on a slow strain or stock query | The query exceeded the server write deadline, or ArangoDB is slow. | Run `just arangodb verify`. Then make the query smaller, or raise the deadline in the server image. |
| A CI deploy ignores a recent cluster-ops change | The cluster-ops change is local, or it is on an unmerged branch. CI always clones `cluster-ops@develop`. | Merge and push cluster-ops `develop`, then run the workflow again. |
