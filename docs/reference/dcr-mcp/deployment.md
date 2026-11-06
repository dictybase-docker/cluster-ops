# dcr-mcp Server Deployment Details

Back to: [dcr-mcp Server Deploy Guide](../../dcr-mcp-deploy.md)

This document gives the details for the `dcr-mcp/` Pulumi project and its deployment workflow: the Kubernetes resources, the upstream GraphQL prerequisite, the stack scaffold recipe, the server CLI flags, the routing comparison between the production Cilium Gateway and the legacy `nginx` Ingress, the CI/CD release-tag pipeline, the verification probes, and the troubleshooting table.

## Table of Contents

- [What the Project Creates](#what-the-project-creates)
- [Prerequisites](#prerequisites)
- [In-Cluster Service Discovery](#in-cluster-service-discovery)
- [Create the Stack](#create-the-stack)
  - [Baseline Stack File](#baseline-stack-file)
  - [Keys That Change per Cluster](#keys-that-change-per-cluster)
- [GraphQL Endpoint](#graphql-endpoint)
- [Server Options and Container Flags](#server-options-and-container-flags)
  - [Stateless Streamable HTTP and Caching](#stateless-streamable-http-and-caching)
  - [Rate Limiting and Proxy Headers](#rate-limiting-and-proxy-headers)
  - [CORS Origins](#cors-origins)
  - [Citation Links emitted by Tools](#citation-links-emitted-by-tools)
- [Routing Architecture: Gateway vs Legacy Ingress](#routing-architecture-gateway-vs-legacy-ingress)
  - [Production Gateway Route (Recommended)](#production-gateway-route-recommended)
  - [Legacy Ingress Mode (`dcr-experiments` Only)](#legacy-ingress-mode-dcr-experiments-only)
  - [Turn the Legacy Ingress Off](#turn-the-legacy-ingress-off)
- [Deploy Behavior](#deploy-behavior)
  - [Recipe Flags](#recipe-flags)
- [CI Deployment](#ci-deployment)
- [Tag Deploy Setup](#tag-deploy-setup)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

---

## What the Project Creates

The `dcr-mcp/` Pulumi program creates two resources in the target namespace, and one optional Ingress resource when `properties.ingress.enabled` is `true`. Each resource name derives from `properties.name` (default `dcr-mcp`).

| Order | Resource | Name | Details |
|-------|----------|------|---------|
| 1 | Deployment | `dcr-mcp-api-server` | Pod label `app: dcr-mcp-api-server`. One container `dcr-mcp-container` (`dictybase/dcr-mcp:<tag>`). TCP port `8080`. HTTP readiness and liveness probes on `GET /healthz`. |
| 2 | Service | `dcr-mcp-api` | Type `NodePort`. Port `8080` to target port `8080`. Depends on Deployment `dcr-mcp-api-server`. |
| 3 | Ingress | `dcr-mcp-ingress` | **Optional (`properties.ingress.enabled: false` by default).** Created only when `properties.ingress.enabled` is `true` for legacy `nginx` + `kcert` clusters. See [routing architecture](#routing-architecture-gateway-vs-legacy-ingress). |

The project creates **no Namespace**, **no Kubernetes Secret**, and **no PersistentVolumeClaim**:
- The `namespace-bootstrap` project owns the namespace ([shared namespaces](../pulumi/namespaces.md)).
- All 12 MCP tools (`autocomplete_stocks`, `search_stocks`, `find_stocks_by_gene`, `get_strain`, `get_plasmid`, `get_plasmid_sequence`, `suggest_phenotype_terms`, `list_strains_with_phenotype`, `check_availability`, `get_publication`, `search`, and `fetch`) and all 4 built-in research prompts (`find_stocks_for_gene`, `compare_strains`, `phenotype_survey`, and `order_checklist`) perform read-only queries against the upstream dictyBase GraphQL server over HTTP and require no credentials.

---

## Prerequisites

Verify each component below before you deploy `dcr-mcp`.

| Component | Requirement | Check | Guide |
|-----------|-------------|-------|-------|
| Cluster shell | Active `cluster-env` sub-shell | `echo "$PULUMI_STACK"` | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Cluster registry | `config/clusters/<cluster>.yaml` exists and validates | `just gcp-cluster registry-show` | [Cluster registry](../backend/cluster-registry.md) |
| Shared namespaces | `namespace-bootstrap` applied (`appNamespace` matches `properties.namespace`) | `pulumi -C namespace-bootstrap stack output appNamespace` | [`pulumi-setup.md`](../../pulumi-setup.md) |
| GraphQL server | Service `graphql-api` (`:8080`) and Deployment `graphql-api-server` serving `/graphql` | `kubectl -n <namespace> get svc graphql-api && kubectl -n <namespace> rollout status deployment/graphql-api-server` | [`graphql-deploy.md`](../../graphql-deploy.md) |
| Cilium Gateway (Production) | `Gateway` `dictycr-gateway` and `Issuer` `letsencrypt-issuer` deployed | `just gateway verify` | [`gateway-deploy.md`](../../gateway-deploy.md) |
| `nginx` + `kcert` (Legacy only) | Ingress controller and `kcert` certificate manager | **Only when `ingress.enabled` is `true`.** | — |

---

## In-Cluster Service Discovery

By default, Pulumi passes no `--graphql-endpoint`, `--graphql-host`, or `--graphql-port` flags to the container. The `dcr-mcp` binary reads the standard Kubernetes service-discovery environment variables for Service `graphql-api` and constructs `http://<host>:<port>/graphql`:

| Service | Environment variables the binary reads | Flag that gets the value | Default |
|---------|----------------------------------------|--------------------------|---------|
| `graphql-api` | `GRAPHQL_API_SERVICE_HOST` (`GRAPHQL_API_PORT_8080_TCP_ADDR`) | `--graphql-host` | — (required unless `--graphql-endpoint` is set) |
| `graphql-api` | `GRAPHQL_API_SERVICE_PORT` (`GRAPHQL_API_PORT_8080_TCP_PORT`) | `--graphql-port` | `8080` |

Kubernetes writes these variables into a pod only when Service `graphql-api` exists in the same namespace before the pod starts. If `dcr-mcp-api-server` starts before `graphql-api` exists (and `properties.graphqlEndpoint` is not set), the container exits with `graphql-endpoint or GRAPHQL_API_SERVICE_HOST must be set`. Restart the Deployment after `graphql-api` exists:

```bash
kubectl -n <namespace> rollout restart deployment/dcr-mcp-api-server
```

---

## Create the Stack

Run `scaffold-dcr-mcp-stack` to initialize the stack with the cluster KMS key and render `dcr-mcp/Pulumi.<stack>.yaml` from `config/templates/dcr-mcp-stack.yaml.tmpl` and the cluster registry entry (`config/clusters/<cluster>.yaml`).

`config/clusters/` ships `config/clusters/dcr-kube1.yaml` (`stack: dcr-kube1`). If the target cluster does not yet have a `config/clusters/<cluster>.yaml` file with `stack: <stack>` (for example, `dcr-experiments` with `stack: experiments`, or a newly provisioned cluster), register it first ([cluster registry](../backend/cluster-registry.md#creating-an-entry-register-cluster)):

```bash
# Register the cluster in config/clusters/<cluster>.yaml if not yet registered
just gcp-cluster register-cluster --env <env> --cluster <cluster>

# Scaffold dcr-mcp/Pulumi.<stack>.yaml
just gcp-pulumi scaffold-dcr-mcp-stack
```

Behavior:

1. Resolves the cluster registry entry matching `--stack` (`$PULUMI_STACK`) and reads `namespace` and `kms_secrets_provider`.
2. Refuses to overwrite an existing `dcr-mcp/Pulumi.<stack>.yaml` (describe-then-create).
3. Runs `pulumi -C dcr-mcp stack init <stack> --secrets-provider <kms>` and appends the rendered `config:` block (`ingress.enabled: false`, `stateless: true`, `rateLimit: 120`, `image.tag: bootstrap`). By default `graphqlEndpoint` is omitted so the container uses Kubernetes Service discovery (`GRAPHQL_API_SERVICE_HOST` and `GRAPHQL_API_SERVICE_PORT`).
4. When `--graphql-endpoint <url>` is passed, writes `properties.graphqlEndpoint` into `dcr-mcp/Pulumi.<stack>.yaml`.
5. Validates that `name` is `dcr-mcp`, `namespace` matches the registry, and no `:latest` tag appears in the rendered file.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Must match a `stack:` key in `config/clusters/*.yaml` |
| `--graphql-endpoint` | No | *(omitted — in-cluster `GRAPHQL_API_SERVICE_HOST`)* | Optional full URL override for `--graphql-endpoint` |
| `--port` / `-p` | No | `8080` | Container and Service TCP port |
| `--rate-limit` | No | `120` | Maximum `/mcp` requests per client IP per minute |

When `dcr-mcp/Pulumi.<stack>.yaml` already exists in Git, select the stack with:

```bash
just gcp-pulumi ensure-stack --folder dcr-mcp
```

### Baseline Stack File

The scaffold template writes this configuration block. Replace `bootstrap` in `properties.image.tag` with an immutable release tag or `develop-<sha>` tag before you run `preview` or `create-resource`.

```yaml
config:
  dcr-mcp:properties:
    allowedOrigins:
      - "*"
    command: start-server
    host: 0.0.0.0
    image:
      name: dictybase/dcr-mcp
      tag: <immutable-tag>
    ingress:
      enabled: false
    name: dcr-mcp
    namespace: prod
    port: 8080
    rateLimit: 120
    stateless: true
```

**Keep all configuration values in plain text.** `dcr-mcp` uses no secrets or credentials.

### Keys That Change per Cluster

`dcr-mcp` is not deployed on the legacy `dcr-experiments` cluster by default (`dcr-mcp/` ships no `Pulumi.experiments.yaml`). The table below compares the recommended production Gateway posture against a legacy `dcr-experiments` Ingress posture if you deploy it there.

| Key | Production (`dcr-kube1`, Recommended) | Legacy (`dcr-experiments`, Optional) |
|-----|---------------------------------------|--------------------------------------|
| `namespace` | `prod` | `dev` |
| `graphqlEndpoint` | omitted (resolved in-cluster from `GRAPHQL_API_SERVICE_HOST:GRAPHQL_API_SERVICE_PORT`, or set to `https://graphql.dictycr.org/graphql`) | omitted (resolved in-cluster from `GRAPHQL_API_SERVICE_HOST:GRAPHQL_API_SERVICE_PORT`, or set to `https://graphql.dictybase.dev/graphql`) |
| `ingress.enabled` | `false` (routed via `gateway/` `routes.mcp`) | `true` (creates `nginx` Ingress `dcr-mcp-ingress`) |
| `ingress.hosts` | omitted (set on `gateway/` `routes.mcp.hosts.0`, e.g. `mcp.dictycr.org`) | `<mcp-host>` (e.g. `mcp.dictybase.dev`, dedicated host) |
| `ingress.path` | omitted (`gateway/` `routes.mcp.path` is `/`, no subpath) | `/` (root path, no subpath) |
| `ingress.tlsSecret` | omitted (`gateway/` `routes.mcp.tlsSecret` is `mcp-tls`) | `<mcp-tls-secret>` (e.g. `mcp-tls-secret`, managed by `kcert`) |
| `ingress.label` | omitted | `name: kcert.dev/ingress`, `value: managed` |
| `image.tag` | Immutable release tag (`v1.1.0`) or `develop-<sha>` | Immutable release tag or `develop-<sha>` |

---

## GraphQL Endpoint

`dcr-mcp` resolves its upstream GraphQL URL in this priority order:

1. **Explicit endpoint override (`--graphql-endpoint` / `GRAPHQL_ENDPOINT`):** When `properties.graphqlEndpoint` is set in `dcr-mcp/Pulumi.<stack>.yaml`, Pulumi passes `--graphql-endpoint <url>` to the container. The Pulumi config validator rejects `<...>` placeholders (`dcr-mcp:properties contains placeholder in graphqlEndpoint`).
2. **In-cluster Kubernetes Service discovery (default):** When `properties.graphqlEndpoint` is omitted, Pulumi omits `--graphql-endpoint`. The `dcr-mcp` binary reads `--graphql-host` (`GRAPHQL_API_SERVICE_HOST` or `GRAPHQL_API_PORT_8080_TCP_ADDR`) and `--graphql-port` (`GRAPHQL_API_SERVICE_PORT` or `GRAPHQL_API_PORT_8080_TCP_PORT`, default `8080`) and builds `http://<host>:<port>/graphql`. This keeps all GraphQL traffic inside the cluster network without hair-pinning through the external LoadBalancer.

If neither `--graphql-endpoint` nor `GRAPHQL_API_SERVICE_HOST` is set, the container fails closed at startup with `graphql-endpoint or GRAPHQL_API_SERVICE_HOST must be set`.

---

## Server Options and Container Flags

The `dcr-mcp` binary uses `urfave/cli/v3` with the subcommand `start-server` (aliases `serve` and `start`). Every CLI flag below belongs to the `start-server` subcommand, so the Pulumi program passes `start-server` first, followed by the flags (and includes `--graphql-endpoint <graphqlEndpoint>` only when `properties.graphqlEndpoint` is non-empty):

```text
start-server [--graphql-endpoint <graphqlEndpoint>] --host <host> --port <port> --stateless=<stateless> --rate-limit <rateLimit> --allowed-origins <origin>...
```

| Pulumi config key | CLI flag | Env var in `dcr-mcp` | Default | Validation / Purpose |
|-------------------|----------|----------------------|---------|----------------------|
| `properties.command` | subcommand | — | `start-server` | Subcommand (`start-server`, `serve`, or `start`) |
| `properties.graphqlEndpoint` | `--graphql-endpoint` (`--endpoint`) | `GRAPHQL_ENDPOINT` | *(omitted)* | Optional GraphQL URL override; takes precedence over `--graphql-host` |
| — (Kubernetes Service env) | `--graphql-host` | `GRAPHQL_API_SERVICE_HOST`, `GRAPHQL_API_PORT_8080_TCP_ADDR` | *(none)* | Kubernetes `graphql-api` Service host (used when `--graphql-endpoint` is not set) |
| — (Kubernetes Service env) | `--graphql-port` | `GRAPHQL_API_SERVICE_PORT`, `GRAPHQL_API_PORT_8080_TCP_PORT` | `8080` | Kubernetes `graphql-api` Service port |
| `properties.host` | `--host` | `MCP_HOST` | `0.0.0.0` | HTTP bind address inside the container |
| `properties.port` | `--port` | `MCP_PORT` | `8080` | TCP port (`0`–`65535`) for `/mcp` and `/healthz` |
| `properties.stateless` | `--stateless=<bool>` | `MCP_STATELESS` | `true` | Streamable HTTP stateless mode (`--stateless=true` or `--stateless=false`) |
| `properties.rateLimit` | `--rate-limit` | `MCP_RATE_LIMIT` | `120` | Max `/mcp` requests per client IP per minute (`>= 1`) |
| `properties.allowedOrigins` | `--allowed-origins` (repeatable) | `ALLOWED_ORIGINS` | `["*"]` | Allowed CORS origins for browser MCP clients |

### Stateless Streamable HTTP and Caching

- **`stateless: true` (default):** Passed as a single argument `--stateless=true`. In stateless mode, `mcp.NewStreamableHTTPHandler` requires no in-memory session affinity across requests, so any pod replica behind the Kubernetes Service, Ingress, or Cilium Gateway can serve any request without sticky sessions.
- **Per-replica LRU cache:** Each `dcr-mcp` pod caches successful `get_strain` and `get_plasmid` GraphQL lookups in memory for `10 minutes` (up to `1,024` entries per entity type). Failed lookups are never cached.

### Rate Limiting and Proxy Headers

- `dcr-mcp` enforces a per-IP rate limit (`properties.rateLimit`, default `120` requests per minute) on `/mcp`.
- `GET /healthz` is exempt from rate limiting so Kubernetes readiness and liveness probes never trigger HTTP `429 Too Many Requests`.
- The router extracts the client IP from `X-Forwarded-For` when the request arrives from a trusted private or loopback proxy hop (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.0/8`, `::1/128`, `fc00::/7`), which matches both the Cilium Gateway Envoy proxy and the legacy `nginx` Ingress controller.

### CORS Origins

- Browser-based MCP clients send preflight `OPTIONS /mcp` requests with `Content-Type`, `Accept`, `Mcp-Session-Id`, and `MCP-Protocol-Version`. `dcr-mcp` allows and exposes those headers automatically.
- Server-to-server connectors (Claude.ai, ChatGPT, Claude Code, Codex CLI, Cursor) ignore CORS headers. Keep `allowedOrigins: ["*"]` unless you want to restrict browser origins to specific domains.

### Citation Links Emitted by Tools

`dcr-mcp` formats clickable citation URLs in tool responses using fixed base domains compiled into `internal/tools/links.go`:
- Strain pages: `https://dictycr.org/strains/<DBS_ID>`
- Plasmid pages: `https://dictycr.org/plasmids/<DBP_ID>`
- PubMed pages: `https://pubmed.ncbi.nlm.nih.gov/<PMID>/`
- DOI links: `https://doi.org/<DOI>`

---

## Routing Architecture: Gateway vs Legacy Ingress

In both production and legacy environments, **`dcr-mcp` is always deployed to its own dedicated hostname at root path `/` (no URL subpath)**. Because the binary mounts its MCP handler at `/mcp` and its health check at `/healthz`, routing `path: /` (`PathPrefix`) on `<mcp-host>` exposes:
- `https://<mcp-host>/mcp` — the Streamable HTTP MCP endpoint that AI clients connect to.
- `https://<mcp-host>/healthz` — the JSON health endpoint (`{"status":"ok"}`).

| Environment | Cluster | How external traffic reaches `dcr-mcp-api:8080` | `dcr-mcp` `ingress.enabled` | TLS Certificate Manager |
|-------------|---------|-------------------------------------------------|-----------------------------|-------------------------|
| **Production (Recommended)** | `dcr-kube1` (and new clusters) | Central Cilium `Gateway` `dictycr-gateway` + `HTTPRoute` `mcp-route` (`gateway/` stack) | `false` (default) | `cert-manager` (`Issuer` `letsencrypt-issuer` -> Secret `mcp-tls`) |
| **Legacy (Optional)** | `dcr-experiments` | Per-app `nginx` `Ingress` `dcr-mcp-ingress` (`dcr-mcp/` stack) | `true` | `kcert` (`kcert.dev/ingress: managed` -> Secret `<mcp-tls-secret>`) |

### Production Gateway Route (Recommended)

On production clusters, keep `properties.ingress.enabled` set to `false` in `dcr-mcp/Pulumi.<stack>.yaml`. After you deploy `dcr-mcp` (so Service `dcr-mcp-api` exists in the namespace), enable the `mcp` route on the `gateway/` stack ([`gateway-deploy.md`](../../gateway-deploy.md)):

```bash
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value true
just gateway deploy
```

What the `gateway/` stack creates when `routes.mcp.enabled` is `true`:
1. An HTTP listener (`http-mcp-0` on port `80`) and an HTTPS listener (`https-mcp-0` on port `443` with `tlsSecret: mcp-tls`) on `Gateway` `dictycr-gateway` for `<mcp-host>`.
2. `HTTPRoute` `mcp-route`, which routes `PathPrefix` `/` on `<mcp-host>` to Service `dcr-mcp-api` on port `8080`.
3. Once your DNS `A` record for `<mcp-host>` points to the Gateway IP (`just gateway show-ip`), `cert-manager` completes the Let's Encrypt HTTP-01 challenge and writes the certificate to Secret `mcp-tls`.

### Legacy Ingress Mode (`dcr-experiments` Only)

`dcr-mcp` is not deployed on `dcr-experiments` by default (`config/clusters/dcr-experiments.yaml` and `dcr-mcp/Pulumi.experiments.yaml` are not checked in). If you need to deploy `dcr-mcp` on `dcr-experiments` (or another legacy cluster without Cilium Gateway API), register the cluster first with `just gcp-cluster register-cluster --env dev --cluster dcr-experiments --ci-env DEV_STAGING`, scaffold the stack with `just gcp-pulumi scaffold-dcr-mcp-stack`, and turn on the per-app `nginx` Ingress inside the `dcr-mcp/` stack.

Set `hosts.0`, `path`, `tlsSecret`, and `label` **before** you set `ingress.enabled` to `true`. The Pulumi config check fails closed with `ingress.enabled=true but ingress is missing hosts, path, tlsSecret` if any required field is empty, and rejects `<...>` placeholders.

```bash
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.path --value /
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.tlsSecret --value <mcp-tls-secret>
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.label.name --value kcert.dev/ingress
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.label.value --value managed
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.enabled --value true
just gcp-pulumi preview --folder dcr-mcp
just gcp-pulumi create-resource --folder dcr-mcp
```

Always set `properties.ingress.path` to `/` (no subpath) and give `properties.ingress.hosts.0` its own hostname (for example, `mcp.dictybase.dev`).

### Turn the Legacy Ingress Off

**CAUTION: Setting `properties.ingress.enabled` to `false` on a stack that currently has `dcr-mcp-ingress` deletes the Ingress resource.** If you are migrating a cluster from `nginx` Ingress to the Cilium Gateway, deploy the Gateway route first, update DNS, and review `just gcp-pulumi preview --folder dcr-mcp` before applying.

```bash
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.enabled --value false
just gcp-pulumi preview --folder dcr-mcp
```

---

## Deploy Behavior

`create-resource` drives the Pulumi Automation API (`go run ./cmd/cluster-ops stack up`). It runs a preview first, blocks any plan that deletes or replaces a protected resource, skips the update when the plan has zero changes, and otherwise applies the exact saved plan.

### Recipe Flags

| Recipe | Flag | Required | Default | Notes |
|--------|------|----------|---------|-------|
| `scaffold-dcr-mcp-stack` | `--stack` / `-s` | No | `$PULUMI_STACK` | Must match a `stack:` key in `config/clusters/*.yaml` |
| `scaffold-dcr-mcp-stack` | `--graphql-endpoint` | No | *(omitted — in-cluster `GRAPHQL_API_SERVICE_HOST`)* | Optional upstream GraphQL URL override |
| `scaffold-dcr-mcp-stack` | `--port` / `-p` | No | `8080` | Container and Service TCP port |
| `scaffold-dcr-mcp-stack` | `--rate-limit` | No | `120` | Requests per client IP per minute on `/mcp` |
| `ensure-stack`, `preview`, `create-resource`, `set-config` | `--folder` / `-f` | Yes | — | Pass `dcr-mcp` |
| `ensure-stack`, `preview`, `create-resource`, `set-config` | `--stack` / `-s` | No | `$PULUMI_STACK` | `ensure-stack` fails closed if `Pulumi.<stack>.yaml` is absent |
| `set-config` | `--key` / `-k` | Yes | — | Config path under `dcr-mcp:`, e.g. `properties.image.tag` (dotted list indices like `.0` rewrite to `[0]`) |
| `set-config` | `--value` / `-v` | Yes | — | Plaintext value to write |

---

## CI Deployment

In `dictyBase/dcr-mcp`, `.github/workflows/ci.yml` runs unit tests and Codecov coverage on pushes to `develop` only — it does **not** deploy to `dcr-experiments`, because `dcr-mcp` is not deployed on the legacy lab cluster.

Production deployments run on release tags through `.github/workflows/tag-build.yaml` (rendered by `just ci render-tag-deploy`), which calls `dictyBase/workflows/.github/workflows/composite-deploy.yaml`. When a release tag is pushed to `dictyBase/dcr-mcp`, the composite workflow:
1. Builds the distroless image (`build/package/Dockerfile`, UPX-compressed static binary `/usr/local/bin/app`) and publishes `dictybase/dcr-mcp:<tag>` to Docker Hub.
2. Authenticates to GCP with `secrets.PROD_DEPLOY_SA_KEY` and exports the cluster kubeconfig from `vars.PROD_KOPS_STATE_STORAGE`.
3. Clones `dictybase-docker/cluster-ops` at branch **`develop`**, selects stack `dcr-kube1` in folder `dcr-mcp/` from `vars.PROD_PULUMI_STATE_STORAGE`, sets `properties.image.tag` to `<tag>`, and runs `pulumi up`.

**Push order: always commit and push `dcr-mcp/Pulumi.<stack>.yaml` (and any `dcr-mcp/*.go` changes) to cluster-ops `develop` before you cut a release tag in `dictyBase/dcr-mcp`.**

---

## Tag Deploy Setup

`dcr-mcp` is not listed in `config/services.yaml` (which is scoped to ArangoDB-backed modware gRPC services that run `check-backend-prereqs` against `svc/arangodb`). Therefore, `just ci deploy-backend-services` and `just ci pr-deploy-workflows` do not iterate over `dcr-mcp`. Instead, run the individual `just ci` recipes in the production `cluster-env` sub-shell:

```bash
# 1. Once per cluster: mint or reuse credentials/<cluster>/deployer.json and preflight it
just ci create-deploy-key

# 2. Once per cluster: publish org variable PROD_PULUMI_STATE_STORAGE and render composite-deploy.yaml
just ci render-composite-deploy

# 3. Add dictyBase/dcr-mcp to PROD_DEPLOY_SA_KEY selected repositories (merges with existing repos)
just ci set-deploy-secret --repos dictyBase/dcr-mcp

# 4. Publish PROD_CLUSTER, PROD_KOPS_STATE_STORAGE, PROD_KOPS_VERSION, PROD_KUBECTL_VERSION, PROD_PULUMI_VERSION
just ci sync-deploy-vars --repos dictyBase/dcr-mcp

# 5. Render the deploy-only tag-build.yaml workflow for dictyBase/dcr-mcp
just ci render-tag-deploy --app dcr-mcp --project dcr-mcp --out bin/ci-render/dcr-mcp-tag-build.yaml
```

| Recipe | What it does | Reference |
|--------|--------------|-----------|
| `just ci create-deploy-key` | Creates or reuses SA `deployer` (`deployer-roles.txt`) and `credentials/<cluster>/deployer.json`, then runs `check-deploy-credentials` (IAM permissions, kOps kubeconfig export, KMS encrypt/decrypt round-trip). | [Deploy credentials](../backend/ci-credentials.md) |
| `just ci render-composite-deploy` | Sets org variable `PROD_PULUMI_STATE_STORAGE` and renders `bin/ci-render/composite-deploy.yaml` so `environment: production` routes `PROD_DEPLOY_SA_KEY` and `PROD_PULUMI_STATE_STORAGE`. | [Workflow render](../backend/render-deploy-workflows.md) |
| `just ci set-deploy-secret` | Upserts `PROD_DEPLOY_SA_KEY` on org `dictyBase` and merges `dcr-mcp` into the secret's selected-repository list without revoking existing repositories. | [CI variables](../backend/ci-variables.md#set-deploy-secret) |
| `just ci sync-deploy-vars` | Sets the five `PROD_*` repository variables on `dictyBase/dcr-mcp` from `config/clusters/<cluster>.yaml` and verifies secret visibility. | [CI variables](../backend/ci-variables.md#sync-deploy-vars) |
| `just ci render-tag-deploy` | Renders `bin/ci-render/dcr-mcp-tag-build.yaml` (`app: dcr-mcp`, `project: dcr-mcp`, `docker_image: dcr-mcp`, `environment: production`, `runner: backend`) and runs the `composite-deploy.yaml` input-drift guard. | [Workflow render](../backend/render-deploy-workflows.md#render-tag-deploy-flags) |

To land the rendered workflow in `dictyBase/dcr-mcp`:
1. Copy `bin/ci-render/dcr-mcp-tag-build.yaml` to `.github/workflows/tag-build.yaml` in `dictyBase/dcr-mcp`.
2. Open a pull request against `develop`, merge it, and fast-forward `main`/`master` before cutting the next release tag.

---

## Verification

Run these checks from the `cluster-env` sub-shell (`<namespace>` is `prod` on `dcr-kube1`, `dev` on legacy clusters):

```bash
# 1. Confirm Deployment rollout and running image tag
kubectl -n <namespace> rollout status deployment/dcr-mcp-api-server
kubectl -n <namespace> get deploy dcr-mcp-api-server \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'

# 2. Confirm Service dcr-mcp-api exposes port 8080
kubectl -n <namespace> get svc dcr-mcp-api

# 3. On Gateway clusters: verify Issuer, Gateway, HTTPRoute mcp-route, and Secret mcp-tls
just gateway verify

# 4. Probe the public health endpoint (expect {"status":"ok"})
curl -fsSL https://<mcp-host>/healthz

# 5. Probe the Streamable HTTP MCP initialize handshake
curl -fsSL -X POST https://<mcp-host>/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"verify","version":"1.0.0"}}}'
```

Once `https://<mcp-host>/mcp` responds to `initialize`, connect your AI assistant (Claude.ai, Claude Desktop, ChatGPT, Claude Code, Codex CLI, Cursor, Windsurf, or GitHub Copilot) using the MCP URL `https://<mcp-host>/mcp`.

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `ERROR: no stack name` | Recipe run outside the `cluster-env` sub-shell | Enter `just cluster-env --env <env> --cluster <cluster>` or pass `--stack <name>`. |
| `ERROR: no registry entry with stack '<stack>' (checked config/clusters/*.yaml)` | You are in a shell for an unregistered cluster (for example, `dcr-experiments` with `PULUMI_STACK=experiments`), or `config/clusters/<cluster>.yaml` has not been created yet | Enter the registered cluster shell (`just cluster-env --env prod --cluster dcr-kube1`), or create the registry entry first with `just gcp-cluster register-cluster --env <env> --cluster <cluster>` ([cluster registry](../backend/cluster-registry.md#creating-an-entry-register-cluster)). |
| `ERROR: stack '<stack>' does not exist in dcr-mcp, and there is no Pulumi.<stack>.yaml to initialize it from` | Stack config file has not been scaffolded yet | Run `just gcp-pulumi scaffold-dcr-mcp-stack` ([create the stack](#create-the-stack)). |
| `dcr-mcp:properties is missing namespace, image.tag` | Required base field is empty in `dcr-mcp/Pulumi.<stack>.yaml` | Set the missing key with `just gcp-pulumi set-config --folder dcr-mcp --key properties.<key> --value <val>`. |
| `dcr-mcp:properties contains placeholder in image.tag, graphqlEndpoint` | `image.tag` or `graphqlEndpoint` still contains `<...>` placeholder text | Pin a real tag (and replace or remove the placeholder `graphqlEndpoint`) with `just gcp-pulumi set-config`. |
| Pod exits with `graphql-endpoint or GRAPHQL_API_SERVICE_HOST must be set` | `dcr-mcp-api-server` started before Service `graphql-api` existed in the namespace and `properties.graphqlEndpoint` is not set | Deploy `graphql_server` first ([`graphql-deploy.md`](../../graphql-deploy.md)), then run `kubectl -n <namespace> rollout restart deployment/dcr-mcp-api-server`. |
| `ErrImagePull` / `ImagePullBackOff` on `dictybase/dcr-mcp:bootstrap` | Stack was applied before replacing the `bootstrap` placeholder tag | Resolve the latest tag with `just ci latest-tag --repo dictyBase/dcr-mcp`, set `properties.image.tag`, and re-run `create-resource`. |
| `ingress.enabled=true but ingress is missing hosts, path, tlsSecret` | Legacy Ingress was enabled before setting `hosts`, `path`, and `tlsSecret` | Set `ingress.hosts.0`, `ingress.path` (`/`), `ingress.tlsSecret`, and `ingress.label.*` before setting `ingress.enabled` to `true` ([legacy Ingress mode](#legacy-ingress-mode-dcr-experiments-only)). |
| `just gateway check` fails with `Error from server (NotFound): services "dcr-mcp-api" not found` | `routes.mcp.enabled` was set to `true` in `gateway/` before deploying `dcr-mcp/` | Deploy `dcr-mcp` first (`just gcp-pulumi create-resource --folder dcr-mcp`), then run `just gateway deploy`. |
| `just gateway check` fails with `route 'mcp' is enabled but contains an empty or placeholder host or tlsSecret` | `routes.mcp.hosts.0` still holds `<mcp-host>` in `gateway/Pulumi.<stack>.yaml` | Run `just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>`. |
| Tool calls fail with `listStockSuggestions query failed` or `searchStocks query failed` | Upstream `graphql-api` Service or `properties.graphqlEndpoint` override is unreachable | Verify `graphql-api-server` is healthy (`kubectl -n <namespace> rollout status deployment/graphql-api-server`) and check `properties.graphqlEndpoint` (if set) in `dcr-mcp/Pulumi.<stack>.yaml`. |
| Client receives HTTP `429 Too Many Requests` on `/mcp` | Client IP exceeded `properties.rateLimit` requests per minute | Raise `properties.rateLimit` with `just gcp-pulumi set-config --folder dcr-mcp --key properties.rateLimit --value <n>` and re-apply. |
| `just ci deploy-backend-services --services dcr-mcp` fails with `no services matched` | `config/services.yaml` only lists ArangoDB-backed modware gRPC services | Use the individual recipes in [tag deploy setup](#tag-deploy-setup). |
| Tag deploy workflow runs in GitHub Actions but deploys nothing or fails on missing stack | `dcr-mcp/Pulumi.<stack>.yaml` was not committed and pushed to cluster-ops `develop` | Commit and push `dcr-mcp/Pulumi.<stack>.yaml` to `develop`, then re-run the tag workflow (`gh run rerun <run-id> --repo dictyBase/dcr-mcp`). |
