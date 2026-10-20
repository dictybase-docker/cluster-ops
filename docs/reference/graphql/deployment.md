# GraphQL Server Deployment Details

Back to: [GraphQL Server Deploy Guide](../../graphql-deploy.md)

Reference for the `graphql_server/` Pulumi program (`graphql_server/*.go`),
in-cluster service discovery, configuration schema, optional Logto
authentication (`authEnabled`), CI deploy mechanics, and troubleshooting.

## Table of Contents

- [What It Does](#what-it-does)
- [Prerequisites](#prerequisites)
  - [Checking Them](#checking-them)
- [In-Cluster Service Discovery](#in-cluster-service-discovery)
- [Stack Configuration](#stack-configuration)
  - [Creating a New Stack File](#creating-a-new-stack-file)
  - [Full Configuration Shape](#full-configuration-shape)
- [Authentication Modes](#authentication-modes)
  - [Default: Auth Disabled](#default-auth-disabled)
  - [Configuration Keys](#configuration-keys)
  - [Argument Mapping](#argument-mapping)
  - [Configure Without Auth](#configure-without-auth)
  - [Configure With Auth](#configure-with-auth)
  - [Fail-Closed Validation](#fail-closed-validation)
  - [Single-Argument Boolean Flag](#single-argument-boolean-flag)
- [Secrets and MinIO Wiring](#secrets-and-minio-wiring)
- [Deploy Behavior](#deploy-behavior)
  - [Flags](#flags)
- [GitHub Actions CI Deployment](#github-actions-ci-deployment)
  - [Wiring Production Tag Deploys](#wiring-production-tag-deploys)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

---

## What It Does

The `graphql_server/` Pulumi program (`graphql_server/main.go`) creates three
Kubernetes resources in the configured `namespace`, all derived from
`properties.name` (`graphql`):

1. **Deployment `graphql-api-server`** (`graphql_server/deployment.go`) — pod
   label `app: graphql-api-server`, container `graphql-container` listening on
   TCP port `8080` (`graphql-api`), HTTP server write timeout `60s` to match
   the `nginx` ingress proxy read timeout.
2. **Service `graphql-api`** (`graphql_server/service.go`) — `type: NodePort`,
   port `8080` → targetPort `8080`, selector `app: graphql-api-server`,
   created with `DependsOn` on the Deployment.
3. **Ingress `graphql-ingress`** (`graphql_server/ingress.go`) —
   `ingressClassName: nginx`, label from `ingress.label` (typically
   `kcert.dev/ingress: managed`), TLS secret `ingress.tlsSecret`, routing each
   host in `ingress.hosts` at `ingress.path` (`Prefix`) to `graphql-api:8080`.

The program creates **no Namespace** and **no Kubernetes Secret** — the
namespace comes from `namespace-bootstrap` ([shared namespaces](../pulumi/namespaces.md)),
and the MinIO credentials Secret is created by the `minio` stack
([MinIO install](../minio/install.md)).

---

## Prerequisites

All upstream Services must exist in the target namespace **before** the
`graphql-api-server` pod starts:

| Component | Required Resource in Namespace | Purpose | Guide |
|-----------|--------------------------------|---------|-------|
| Namespace | `dev` or `prod` | Target application namespace | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Redis | Service `redis` (`:6379`) | Caching and dataloader store | [`redis-deploy.md`](../../redis-deploy.md) |
| MinIO | Service `minio` (`:9000`) + Secret (`minio-root` or `minio`) + bucket (`editor`) | S3 asset storage | [`minio-deploy.md`](../../minio-deploy.md) |
| Stock gRPC | Service `stock-api` (`:9345`) | Stock and strain queries | [`backend-service-deploy.md`](../../backend-service-deploy.md) |
| Order gRPC | Service `order-api` (`:9250`) | Stock center order queries | [`backend-service-deploy.md`](../../backend-service-deploy.md) |
| Annotation gRPC | Service `annotation-api` (`:9250`) | Tagged annotation queries | [`backend-service-deploy.md`](../../backend-service-deploy.md) |
| Feature Annotation gRPC | Service `anno-feat-api` (`:9250`) | Sequence feature queries (`modware-annotation-feature`) | [`backend-service-deploy.md`](../../backend-service-deploy.md) |
| Content gRPC | Service `content-api` (`:9250`) | CMS page content queries | [`backend-service-deploy.md`](../../backend-service-deploy.md) |
| Ingress + TLS | `ingress-controller` (`nginx`) + `kcert` | Public HTTPS termination | — |
| Logto *(only when `authEnabled: true`)* | Public OIDC/JWKS endpoint | JWT validation and management API | [`logto-deploy.md`](../../logto-deploy.md) |

### Checking Them

Redis and MinIO have verify recipes; the five gRPC Services have none, so probe
them directly (an absent Service is reported by name):

```bash
just redis verify
just minio verify
kubectl -n prod get svc stock-api order-api annotation-api anno-feat-api content-api
```

Both verify recipes default to `--namespace prod`, Secret `minio-root`, and
node pool `--pool database --node-count 3` — on the lab `experiments` cluster
pass `--namespace dev` (plus `--secret minio` for MinIO) and the pool flags
that match that cluster. Deploy missing gRPC Services with
[`backend-service-deploy.md`](../../backend-service-deploy.md)
(`just ci deploy-backend-services`), and the `editor` bucket with
[MinIO import](../minio/import.md) if it is absent.

---

## In-Cluster Service Discovery

`graphql_server/container.go` does **not** pass `--stock-grpc-host`,
`--redis-master-service-host`, or `--s3-server` CLI flags. Instead, the
`graphql-server` binary (`cmd/graphql-server/main.go`) reads standard
Kubernetes service-discovery environment variables injected into the pod at
startup:

| Flag in `graphql-server` | Environment Variable Read | Kubernetes Service Name |
|--------------------------|---------------------------|-------------------------|
| `--stock-grpc-host` / `--stock-grpc-port` | `STOCK_API_SERVICE_HOST` / `STOCK_API_SERVICE_PORT` | `stock-api` |
| `--order-grpc-host` / `--order-grpc-port` | `ORDER_API_SERVICE_HOST` / `ORDER_API_SERVICE_PORT` | `order-api` |
| `--annotation-grpc-host` / `--annotation-grpc-port` | `ANNOTATION_API_SERVICE_HOST` / `ANNOTATION_API_SERVICE_PORT` | `annotation-api` |
| `--feature-annotation-grpc-host` / `--feature-annotation-grpc-port` | `ANNO_FEAT_API_SERVICE_HOST` / `ANNO_FEAT_API_SERVICE_PORT` | `anno-feat-api` |
| `--content-grpc-host` / `--content-grpc-port` | `CONTENT_API_SERVICE_HOST` / `CONTENT_API_SERVICE_PORT` | `content-api` |
| `--redis-master-service-host` / `--redis-master-service-port` | `REDIS_SERVICE_HOST` / `REDIS_SERVICE_PORT` | `redis` |
| `--s3-server` / `--s3-server-port` | `MINIO_SERVICE_HOST` / `MINIO_SERVICE_PORT` | `minio` |

Kubernetes only injects `<SVC>_SERVICE_HOST` and `<SVC>_SERVICE_PORT` when the
Service exists in the **same namespace** before the pod is created. If any of
the five gRPC Services is created *after* `graphql-api-server`, the pod starts
without that variable and exits with `Required flag ... not set`. Fix by
restarting the deployment after the Service exists:

```bash
kubectl -n prod rollout restart deployment/graphql-api-server
```

---

## Stack Configuration

`ReadConfig` (`graphql_server/config.go`) reads `graphql_server:properties`.
Use `graphql_server/Pulumi.experiments.yaml` as the canonical reference
(`Pulumi.dev.yaml` uses a retired `configMap`/`authKeys` structure that the Go
program no longer reads).

### Creating a New Stack File

The repository ships `Pulumi.dev.yaml` (retired schema) and
`Pulumi.experiments.yaml` (lab, namespace `dev`, KMS key
`projects/dcr-experiments/.../cryptoKeys/pulumi`) only — **no production stack
file exists yet**. There is no scaffold recipe for this
project: `just gcp-pulumi scaffold-backend-stack` renders the modware gRPC
template (`appName`/`arangodb` keys), which this program does not read.

Copy `Pulumi.experiments.yaml` by hand, drop the `secure:` blocks and the
`encryptedkey`/`secretsprovider` header, then run
`just gcp-pulumi ensure-stack --folder graphql_server` to register the stack
under the target cluster's KMS provider, and re-encrypt each secret with
`just gcp-pulumi set-secret`.

`just gcp-pulumi new-stack-from --folder graphql_server --from-stack experiments`
also copies config, but `CopyConfig` (`internal/pulumiauto/config.go`) reads
every source value decrypted before re-encrypting it — it works only for an
operator holding `decrypt` on the *source* cluster's KMS key
(`dcr-experiments`), which a production deployer normally does not.

Keys that must change per cluster:

| Key | Lab (`experiments`) | Production (`<prod-stack>`) |
|-----|---------------------|----------------------------|
| `namespace` | `dev` | `prod` |
| `ingress.hosts` | `graphql.dictybase.dev` | the production host |
| `ingress.tlsSecret` | `graphql-tls-secret` | per-cluster TLS Secret |
| `endpoints.auth` / `endpoints.store` | `*.dictybase.dev` | production Logto and storage hosts |
| `allowedOrigins` | `*.dictybase.dev` | production front-end origins |
| `secrets.minio.*` | `minio` / `user` / `pass` | `minio-root` / `rootUser` / `rootPassword` |
| `image.tag` | whatever CI last deployed | an immutable `develop-<sha>` or release tag |

### Full Configuration Shape

The auth-enabled shape, as shipped in `Pulumi.experiments.yaml`. For the
no-auth variant drop `endpoints.auth` and `secrets.auth` and set
`authEnabled: false` ([authentication modes](#authentication-modes)).

```yaml
config:
  graphql_server:properties:
    authEnabled: true
    allowedOrigins:
      - http://localhost:*
      - http://*.dictybase.dev
      - http://dictybase.dev*
    endpoints:
      auth: https://logto.dictybase.dev
      organism: https://raw.githubusercontent.com/dictyBase/migration-data/master/downloads/organisms-with-citations.staging.json
      publication: https://www.ebi.ac.uk/europepmc/webservices/rest/search
      store: https://storage.dictybase.dev
    image:
      name: dictybase/graphql-server
      tag: develop-615c758
    ingress:
      hosts:
        - graphql.dictybase.dev
      label:
        name: kcert.dev/ingress
        value: managed
      path: /
      tlsSecret: graphql-tls-secret
    logLevel: error
    name: graphql
    namespace: dev
    port: 8080
    s3Bucket:
      name: editor
      path: assets
    secrets:
      auth:
        appId:
          secure: <kms-encrypted>
        appSecret:
          secure: <kms-encrypted>
        jwksURI:
          secure: <kms-encrypted>
        jwtAudience:
          secure: <kms-encrypted>
        jwtIssuer:
          secure: <kms-encrypted>
      minio:
        name:
          secure: <kms-encrypted-secret-name>
        passKey: pass
        userKey:
          secure: <kms-encrypted-user-key-name>
```

---

## Authentication Modes

Two layers decide the authentication state, and both fail closed: the Pulumi
program turns `properties.authEnabled` into container arguments (`authFlags()`,
`graphql_server/container.go`), and the `graphql-server` binary re-validates
those arguments at startup (`validateAuthFlags()`,
`internal/app/server/server.go` in `dictyBase/graphql-server`). `authFlags()`
and `validate()` are the only readers of `endpoints.auth` and
`secrets.auth.*` in the Pulumi program.

### Default: Auth Disabled

`AuthEnabled` is a plain `bool` on `GraphqlServerConfig`
(`graphql_server/config.go`), filled by `conf.TryObject("properties", ...)`, so
an absent `authEnabled` key leaves the Go zero value — **`false`**. The
`graphql-server` CLI agrees: `--auth-enabled` is a `cli.BoolFlag`
(`cmd/graphql-server/main.go`, `authFlags()`) declared with no `Value:`, so it
is `false` unless passed. **Omitting the key is a supported configuration, not
an incomplete one.**

With `authEnabled: false` the container receives exactly one auth argument,
`--auth-enabled=false`, and:

- `middleware.NewJWTAuth` is never constructed and the JWT handler is never
  added to the chi router — every request reaches the resolvers unauthenticated;
- the registry gets `authentication.NoopClient{}`, whose every method returns
  `authentication is disabled`, so resolvers that call the Logto management API
  surface that text as a regular GraphQL error and need no conditional logic;
- `endpoints.auth` and all five `secrets.auth.*` values are never read, so
  stale or absent entries are harmless;
- no Logto deployment is required ([`logto-deploy.md`](../../logto-deploy.md)).

The shipped lab stack `Pulumi.experiments.yaml` sets `authEnabled: true` — a
stack file copied from it inherits auth-on, not the default.

### Configuration Keys

All keys live under `graphql_server:properties`. "Purpose" quotes the usage
string of the matching `graphql-server` flag.

| Key | Required when `authEnabled: true` | Set with | Purpose |
|-----|-----------------------------------|----------|---------|
| `authEnabled` | — (the switch itself) | `set-config` with `--value true` / `--value false` | Enables the JWT middleware and the real Logto management client |
| `endpoints.auth` | Yes | `set-config` (public URL, not a secret) | "base http url of logto authentication api endpoint" |
| `secrets.auth.appId` | Yes | `set-secret` | "api identifier" of the Logto application |
| `secrets.auth.appSecret` | Yes | `set-secret` | "secret to access the authentication api" |
| `secrets.auth.jwksURI` | Yes | `set-secret` | "url to retrieve JWK public key set" |
| `secrets.auth.jwtIssuer` | Yes | `set-secret` | "expected jwt issuer of the token" |
| `secrets.auth.jwtAudience` | Yes | `set-secret` | "expect jwt audience of the token" |

`set-config` writes through `pulumi config set --path`, which infers scalar
types: `--value false` lands in `Pulumi.<stack>.yaml` as the YAML boolean
`authEnabled: false`, not the string `"false"`. There is no recipe that removes
a config key — delete unwanted `secrets.auth.*` entries from the stack file by
hand.

### Argument Mapping

`authFlags()` emits these container arguments; the env-var column is the CLI's
own fallback, which this Deployment never uses — `ContainerEnvArgsArray()`
injects only `ACCESS_KEY` and `SECRET_KEY`, so auth values always arrive as
arguments.

| Key | Container Argument | `graphql-server` Flag | Env Var Fallback |
|-----|--------------------|-----------------------|------------------|
| `authEnabled` | `--auth-enabled=true` / `--auth-enabled=false` (always emitted) | `--auth-enabled` | `AUTH_ENABLED` |
| `endpoints.auth` | `--auth-api-endpoint <value>` | `--auth-api-endpoint` | `AUTH_ENDPOINT` |
| `secrets.auth.appId` | `--app-id <value>` | `--app-id` | `APPLICATION_ID` |
| `secrets.auth.appSecret` | `--app-secret <value>` | `--app-secret` | `APPLICATION_SECRET` |
| `secrets.auth.jwksURI` | `--jwks-uri <value>` | `--jwks-uri` | `JWKS_PUBLIC_URI` |
| `secrets.auth.jwtIssuer` | `--jwt-issuer <value>` | `--jwt-issuer` | `JWT_ISSUER` |
| `secrets.auth.jwtAudience` | `--jwt-audience <value>` | `--jwt-audience` | `JWT_AUDIENCE` |

The six auth-mode arguments after `--auth-enabled=true` are emitted **only** in
that mode. `graphql-server` also has an `--api-resource` flag with the built-in
default `https://default.logto.app/api`, which the Pulumi program does not
emit — the Logto management client therefore runs with that default API
resource. Changing it requires a code change in `authFlags()`, not a config key.

### Configure Without Auth

One recipe call; the auth endpoint and secrets may stay unset.

```bash
just gcp-pulumi set-config --folder graphql_server --key properties.authEnabled --value false
just gcp-pulumi preview --folder graphql_server
just gcp-pulumi create-resource --folder graphql_server
```

### Configure With Auth

Set the endpoint and all five secrets **before** flipping `authEnabled` — any
`preview` run between the flip and the last secret fails closed (see below).
`set-secret` without `--value` prompts with hidden input, keeping the value out
of shell history and process arguments.

```bash
# 1. Logto API endpoint (public URL, stored plain)
just gcp-pulumi set-config --folder graphql_server --key properties.endpoints.auth --value https://logto.dictybase.dev

# 2. The five Logto secrets, each at a hidden prompt
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.appId
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.appSecret
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.jwksURI
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.jwtIssuer
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.auth.jwtAudience

# 3. Flip the switch last
just gcp-pulumi set-config --folder graphql_server --key properties.authEnabled --value true

# 4. Apply
just gcp-pulumi preview --folder graphql_server
just gcp-pulumi create-resource --folder graphql_server
```

Switching modes rewrites the container arguments, so `create-resource` rolls
the Deployment; confirm the new state in the startup log line
([verification](#verification)).

### Fail-Closed Validation

| Gate | Runs At | Trips When | Result |
|------|---------|------------|--------|
| `validate()` (`graphql_server/config.go`, called by `ReadConfig`) | every `preview` and `create-resource`, before any resource is declared | `authEnabled: true` and any of `appId`, `appSecret`, `jwksURI`, `jwtIssuer`, `jwtAudience` is empty | `authEnabled=true but secrets.auth is missing <names>` — the program exits, no plan is produced |
| `validateAuthFlags()` (`internal/app/server/server.go`) | container startup, before the router is built | `--auth-enabled=true` and any of `jwks-uri`, `jwt-issuer`, `jwt-audience`, `auth-api-endpoint`, `app-id`, `app-secret` is empty | `--<flag> is required when auth is enabled` with exit code 2 — pod `CrashLoopBackOff` |

The code comment on `validate()` states the intent: a half-configured stack
would otherwise deploy a container that cannot boot.

**The two gates do not cover the same set.** `validate()` checks the five
secrets only; `endpoints.auth` is checked by the server alone. A stack with
`authEnabled: true` and an empty `endpoints.auth` therefore previews and
applies cleanly, then crash-loops with
`--auth-api-endpoint is required when auth is enabled`. Set the endpoint in the
same change as the secrets.

### Single-Argument Boolean Flag

`authFlags()` passes the boolean as one argument — `--auth-enabled=true` or
`--auth-enabled=false` — and passes it in **both** modes:

- `graphql-server` uses `urfave/cli` v1 (`github.com/urfave/cli`), where
  `--auth-enabled false` leaves the boolean `true` and treats `false` as a
  positional argument. The `=` form is the only safe way to pass it.
- Emitting it even when disabled makes an image predating the flag fail loudly
  on startup (`flag provided but not defined: -auth-enabled`, see
  [troubleshooting](#troubleshooting)) instead of booting with the wrong
  authentication state.

---

## Secrets and MinIO Wiring

`ContainerEnvArgsArray()` (`graphql_server/container.go`) injects `ACCESS_KEY`
and `SECRET_KEY` into the container via `secretKeyRef`:

| Container Env Var | Kubernetes Secret Name | Secret Key |
|-------------------|------------------------|------------|
| `ACCESS_KEY` | `properties.secrets.minio.name` | `properties.secrets.minio.userKey` |
| `SECRET_KEY` | `properties.secrets.minio.name` | `properties.secrets.minio.passKey` |

Note the difference between the lab (`experiments`) and production MinIO
secrets:

| Environment | MinIO Secret Name (`secrets.minio.name`) | User Key (`userKey`) | Password Key (`passKey`) |
|-------------|------------------------------------------|----------------------|--------------------------|
| `experiments` (`dev`) | `minio` | `user` | `pass` |
| Production (`prod`, [`minio-deploy.md`](../../minio-deploy.md)) | `minio-root` | `rootUser` | `rootPassword` |

To set the values on the active stack (the Logto authentication secrets are
separate — see [configure with auth](#configure-with-auth)):

```bash
just gcp-pulumi set-secret --folder graphql_server --key properties.secrets.minio.name --value minio-root
just gcp-pulumi set-config --folder graphql_server --key properties.secrets.minio.userKey --value rootUser
just gcp-pulumi set-config --folder graphql_server --key properties.secrets.minio.passKey --value rootPassword
```

All three values are Secret and key **names**, not credentials — the
credentials live in the Kubernetes Secret written by the `minio` stack. The lab
stack encrypts `name` and `userKey` but leaves `passKey: pass` plain; either
storage form works, because the program only interpolates them into
`secretKeyRef`.

---

## Deploy Behavior

`create-resource` drives the Pulumi Automation API (`cluster-ops stack up`,
`internal/pulumiauto/up.go`), not the interactive CLI: no confirmation prompt,
engine output discarded, and secret config values redacted from logs and error
text. `RunIfAllowed` (`internal/pulumiauto/preview.go`) previews first and
then:

- aborts, listing the URNs, when the plan changes a **protected** resource
  (the recipe passes no `--allow-protected`);
- skips the update entirely on a zero-change plan;
- applies the **saved plan** from that preview, so the applied plan is the
  reviewed plan.

It creates Deployment `graphql-api-server`, NodePort Service `graphql-api`
(port `8080`, `DependsOn` the Deployment), and Ingress `graphql-ingress`.
Nothing waits for the rollout — the checks in [Verification](#verification) are
the operator's job. Upgrades are the same two commands after changing
`properties.image.tag`.

Manual deployment uses the standard `gcp-pulumi` recipes:

```bash
just gcp-pulumi ensure-stack --folder graphql_server
just gcp-pulumi preview --folder graphql_server
just gcp-pulumi create-resource --folder graphql_server
```

### Flags

| Recipe | Flag | Required | Default | Notes |
|--------|------|----------|---------|-------|
| `ensure-stack` | `--folder` / `-f` | Yes | — | Pass `graphql_server` |
| `ensure-stack` | `--stack` / `-s` | No | `$PULUMI_STACK` | Creates the stack when `Pulumi.<stack>.yaml` exists; otherwise only selects an existing stack and fails when there is none |
| `set-config` | `--plaintext` | No | unset | Accepted but **unused** on the live recipe — `set-config` never passes `--secret`, so values are already stored plain (only the legacy `set-config-shell` honors it) |
| `set-config` / `set-secret` | `--folder` / `-f` | Yes | — | Pass `graphql_server` |
| `set-config` / `set-secret` | `--key` / `-k` | Yes | — | Path under `graphql_server:`, e.g. `properties.image.tag` |
| `set-config` / `set-secret` | `--value` / `-v` | Yes (`set-config`) / No (`set-secret`) | Hidden prompt (`set-secret`) | Omit `--value` on `set-secret` to avoid shell history |
| `preview` / `create-resource` | `--folder` / `-f` | Yes | — | Pass `graphql_server` |
| `preview` / `create-resource` | `--stack` / `-s` | No | `$PULUMI_STACK` | Runs inside `just cluster-env` |

---

## GitHub Actions CI Deployment

In `dictyBase/graphql-server`:

1. Pushing to `develop` triggers `.github/workflows/ci.yml`: runs unit tests
   against a Redis service container, then calls
   `dictyBase/workflows/.github/workflows/composite-deploy.yaml@develop` with:
   - `app: graphql-server`
   - `project: graphql_server`
   - `stack: experiments`
   - `docker_image: graphql-server`
2. The composite workflow checks out `dictybase-docker/dagger-of-dcr@develop`
   and runs `just deploy-backend`, which:
   - Builds and publishes `dictybase/graphql-server:<ref>-<sha>` to Docker Hub.
   - Clones `dictybase-docker/cluster-ops` at branch **`develop`**.
   - Runs `pulumi config set --path properties.image.tag <tag>` in
     `graphql_server/` and executes `pulumi up`.

### Wiring Production Tag Deploys

The CI path above targets the lab `experiments` stack from a `develop` push;
production rollouts are manual (`just gcp-pulumi create-resource`) until the
service repo carries a tag workflow. `graphql-server` has **no** entry in
`config/services.yaml`, so the aggregate recipes skip it —
`just ci deploy-backend-services` and `just ci pr-deploy-workflows` iterate
that file and exit with `no services matched` for this repo. Wire it with the
three single-repo recipes instead, from the production `cluster-env` sub-shell
(`CLUSTER_NAME` and `PULUMI_STACK` supply `--cluster` and `--stack`):

```bash
just ci set-deploy-secret --repos dictyBase/graphql-server
just ci sync-deploy-vars --repos dictyBase/graphql-server
just ci render-tag-deploy --app graphql-server --project graphql_server --out bin/ci-render/graphql-server-tag-build.yaml
```

| Step | What it changes | Blast radius | Details |
|------|-----------------|--------------|---------|
| `set-deploy-secret` | Adds `graphql-server` to the selected-repository list of org secret `PROD_DEPLOY_SA_KEY`, uploading `credentials/<cluster>/deployer.json` | Merges with the repos already selected (`ci.justfile` reads the current list and re-sends it), so a single-repo run never revokes the modware repos | [CI variables](../backend/ci-variables.md#set-deploy-secret) |
| `sync-deploy-vars` | Sets the five `PROD_*` repo variables (`PROD_CLUSTER`, `PROD_KOPS_STATE_STORAGE`, `PROD_KOPS_VERSION`, `PROD_KUBECTL_VERSION`, `PROD_PULUMI_VERSION`) on `dictyBase/graphql-server` from the registry entry | Idempotent upsert per variable; warns (does not fail) when `PROD_DEPLOY_SA_KEY` is not yet visible to the repo | [CI variables](../backend/ci-variables.md#sync-deploy-vars) |
| `render-tag-deploy` | Writes `bin/ci-render/graphql-server-tag-build.yaml` in **cluster-ops**, nothing in the service repo | None until the PR below is merged | [workflow render](../backend/render-deploy-workflows.md) |

**Project to image-name mapping.** `render-tag-deploy` derives the Docker Hub
image from the Pulumi project folder: `image_name="${project//_/-}"`
(`just_modules/ci.justfile`). `--project graphql_server` therefore renders
`docker_image: graphql-server`, matching what `ci.yml` passes on the develop
path — no extra flag needed. Pass `--docker-image` / `-d` only when the image
name is not the dashed project folder. `--stack` defaults to `$PULUMI_STACK`
and must match the `stack:` key of a registry entry under
`config/clusters/*.yaml`, otherwise the recipe fails with
`no registry entry with stack '<name>'`.

**Rendered inputs** (from `config/templates/tag-build-deploy.yaml.tmpl`, stack
`dcr-kube1`, `ci_env: PROD`):

| `with:` key | Rendered value | Source |
|-------------|----------------|--------|
| `app` | `graphql-server` | `--app` |
| `project` | `graphql_server` | `--project` (the cluster-ops folder, underscore kept) |
| `docker_image` | `graphql-server` | `--project` with `_` → `-`, or `--docker-image` |
| `stack` | `dcr-kube1` | `--stack` / `$PULUMI_STACK` |
| `dockerfile` | `build/package/Dockerfile` | hardcoded in the template; not verified against `dictyBase/graphql-server` — confirm the path before merging the PR |
| `cluster`, `cluster_state_storage`, `kops_version`, `kubectl_version`, `pulumi_version` | `${{ vars.PROD_* }}` | the variables `sync-deploy-vars` publishes |
| `environment` | `production` | hardcoded — selects `PROD_DEPLOY_SA_KEY` inside `composite-deploy.yaml` |

**PR it into the service repo.** `pr-deploy-workflows` cannot do this for
`graphql-server` (not in `config/services.yaml`), so the PR against
`dictyBase/graphql-server` is manual:

1. Copy `bin/ci-render/graphql-server-tag-build.yaml` to
   `.github/workflows/tag-build.yaml`, replacing whatever tag workflow is
   there — the composite builds and publishes the image itself, so keeping an
   older `build-publish-image.yaml` call would build every tag twice.
2. Delete `.github/workflows/staging-build.yaml` in the same PR — there is no
   staging environment, and the render refuses any output mentioning it.
3. Leave `.github/workflows/ci.yml` untouched: it keeps deploying `develop`
   pushes to the `experiments` stack with the dev `DEPLOY_SA_KEY`.

Two preconditions have to hold before a tag actually deploys, and neither is
checked by these three recipes:

- `dictyBase/workflows` `composite-deploy.yaml` must already select
  `secrets.PROD_DEPLOY_SA_KEY` and `vars.PROD_PULUMI_STATE_STORAGE` for
  `inputs.environment == 'production'`
  ([workflow render](../backend/render-deploy-workflows.md)); without it the
  tag run authenticates with the dev key and fails.
- `graphql_server/Pulumi.<prod-stack>.yaml` must exist and be pushed to
  cluster-ops `develop` ([creating a new stack file](#creating-a-new-stack-file)) —
  the repository ships no production stack file, so a tag deploy would reach
  `pulumi up` with no stack config.

The render itself is covered by `scripts/test-render-tag-deploy.sh`, which
asserts the `graphql_server` → `graphql-server` mapping and the
`$PULUMI_STACK` default. The end-to-end tag deploy for this repository has not
been exercised; treat the first production tag as a supervised run.

**Ordering rule:** Any change to `graphql_server/*.go` or
`graphql_server/Pulumi.<stack>.yaml` in `cluster-ops` **must be committed and
pushed to `develop` before** merging or tagging `dictyBase/graphql-server`.
Otherwise the CI run clones the old `cluster-ops` tree and deploys with stale
flags or config.

---

## Verification

The `cluster-env` sub-shell exports `KUBECONFIG` but sets no default namespace
on the context (`.env.<env>.<cluster>` carries no namespace variable), so every
command below needs `-n <namespace>` — `prod` on production, `dev` on the lab
`experiments` stack.

1. **Rollout and running image** (the image tag must match
   `properties.image.tag` on the stack):
   ```bash
   kubectl -n prod rollout status deployment/graphql-api-server
   kubectl -n prod get deploy graphql-api-server -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
   ```
2. **Startup logs (confirm `authEnabled` state):**
   ```bash
   kubectl -n prod logs deployment/graphql-api-server --tail=20
   ```
   Expect:
   `going to start graphql server with authentication enabled <true|false>, jwt-audience ... and jwt-issuer ...`
3. **Service and Ingress** (the Ingress must show the `ingress.hosts` entries
   and an address from the `nginx` controller):
   ```bash
   kubectl -n prod get svc graphql-api
   kubectl -n prod get ingress graphql-ingress
   ```

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `authEnabled=true but secrets.auth is missing ...` during `preview` / `up` | `authEnabled` is `true` in `Pulumi.<stack>.yaml`, but one or more `secrets.auth.*` keys are unset | Set all five `properties.secrets.auth.*` secrets with `just gcp-pulumi set-secret`, or set `authEnabled: false` ([auth modes](#authentication-modes)) |
| Pod `CrashLoopBackOff` with `--auth-api-endpoint is required when auth is enabled` (exit code 2) | `authEnabled: true` with an empty `properties.endpoints.auth` — the Pulumi `validate()` gate does not check that key | `just gcp-pulumi set-config --folder graphql_server --key properties.endpoints.auth --value <logto-url>`, then re-apply |
| Pod `CrashLoopBackOff` with `Required flag "stock-grpc-host" not set` (or `order`, `annotation`, `feature-annotation`, `content`) | One of the five gRPC Services did not exist in the namespace when the pod started | Deploy the missing gRPC Service (`stock-api`, `order-api`, `annotation-api`, `anno-feat-api`, `content-api`), then run `kubectl -n <ns> rollout restart deployment/graphql-api-server` |
| Pod `CrashLoopBackOff` with `flag provided but not defined: -auth-enabled` | Deployed `dictybase/graphql-server` image predates the `--auth-enabled` flag | Update `properties.image.tag` to a current image built from `develop` |
| Pod `CreateContainerConfigError` referencing Secret `minio` or `minio-root` | `properties.secrets.minio.name` or its `userKey`/`passKey` does not match the Secret in the namespace | Check `kubectl -n <ns> get secret` in the namespace; on prod (`minio-deploy.md`) use Secret `minio-root` with keys `rootUser` and `rootPassword` |
| Pod `CrashLoopBackOff` on Redis connection (`REDIS_SERVICE_HOST` empty or connection refused) | Service `redis` was absent when the pod started or Redis is down | Run `just redis verify`, then `kubectl -n <ns> rollout restart deployment/graphql-api-server` |
| CI deploy on `graphql-server` ignores recent `cluster-ops` changes | `cluster-ops` changes are local or on an unmerged branch (`dagger-of-dcr` always clones `cluster-ops@develop`) | Merge and push `cluster-ops` `develop` first, then re-run the GitHub Actions workflow |
| Upstream `502 Bad Gateway` on slow strain/stock queries | Query exceeded server write deadline or ArangoDB I/O bottleneck | Ensure image includes the `60s` `graphqlServerWriteTimeout` and verify ArangoDB health (`just arangodb verify`) |
