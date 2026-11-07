# dcr-mcp Server Deployment Details

Back to: [dcr-mcp Server Deploy Guide](../../dcr-mcp-deploy.md)

Details for the `dcr-mcp/` Pulumi project: the Kubernetes resources, the two read-only recipes (`check` and `verify`), the cluster registration prerequisite, the stack scaffold, the GraphQL endpoint resolution, the container flags, the two routing modes, the release-tag pipeline, and the troubleshooting table.

## Table of Contents

- [What the Project Creates](#what-the-project-creates)
- [Prerequisites](#prerequisites)
  - [just dcr-mcp check](#just-dcr-mcp-check)
- [Cluster Registration (One Time per Cluster)](#cluster-registration-one-time-per-cluster)
- [Create the Stack](#create-the-stack)
  - [Baseline Stack File](#baseline-stack-file)
  - [Keys That Change per Cluster](#keys-that-change-per-cluster)
- [Image Tag](#image-tag)
- [GraphQL Endpoint Resolution](#graphql-endpoint-resolution)
- [Server Options and Container Flags](#server-options-and-container-flags)
  - [Stateless Streamable HTTP and Caching](#stateless-streamable-http-and-caching)
  - [Rate Limiting and Proxy Headers](#rate-limiting-and-proxy-headers)
  - [CORS Origins](#cors-origins)
  - [Citation Links Emitted by Tools](#citation-links-emitted-by-tools)
- [Routing Architecture: Gateway vs Legacy Ingress](#routing-architecture-gateway-vs-legacy-ingress)
  - [Production Gateway Route (Recommended)](#production-gateway-route-recommended)
  - [Legacy Ingress Mode (`dcr-experiments` Only)](#legacy-ingress-mode-dcr-experiments-only)
  - [Turn the Legacy Ingress Off](#turn-the-legacy-ingress-off)
- [Deploy Behavior](#deploy-behavior)
  - [Recipe Flags](#recipe-flags)
- [Verification](#verification)
- [CI Deployment](#ci-deployment)
- [Tag Deploy Setup](#tag-deploy-setup)
- [Troubleshooting](#troubleshooting)

---

## What the Project Creates

The program creates two resources in the target namespace, and a third one only in legacy Ingress mode. Each name derives from `properties.name` (default `dcr-mcp`).

| Order | Resource | Name | Details |
|-------|----------|------|---------|
| 1 | Deployment | `dcr-mcp-api-server` | Pod label `app: dcr-mcp-api-server`. One container `dcr-mcp-container` (`dictybase/dcr-mcp:<tag>`). TCP port `8080`. Readiness and liveness probes on `GET /healthz`. |
| 2 | Service | `dcr-mcp-api` | Type `NodePort`. Port `8080` to target port `8080`. Depends on the Deployment. |
| 3 | Ingress | `dcr-mcp-ingress` | **Optional. The default is off.** Created only when `properties.ingress.enabled` is `true`. Class `nginx`. See [routing architecture](#routing-architecture-gateway-vs-legacy-ingress). |

The project creates **no Namespace**, **no Kubernetes Secret**, and **no PersistentVolumeClaim**. The `namespace-bootstrap` project owns the namespace ([shared namespaces](../pulumi/namespaces.md)). The server holds no credentials: all 12 MCP tools (`autocomplete_stocks`, `search_stocks`, `find_stocks_by_gene`, `get_strain`, `get_plasmid`, `get_plasmid_sequence`, `suggest_phenotype_terms`, `list_strains_with_phenotype`, `check_availability`, `get_publication`, `search`, `fetch`) and the 4 research prompts (`find_stocks_for_gene`, `compare_strains`, `phenotype_survey`, `order_checklist`) send read-only queries to the dictyBase GraphQL server over HTTP.

---

## Prerequisites

| Component | Requirement | Check | Guide |
|-----------|-------------|-------|-------|
| Cluster shell | Active `cluster-env` sub-shell | `echo "$PULUMI_STACK"` | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Cluster registry | `config/clusters/<cluster>.yaml` with a `stack:` key equal to `$PULUMI_STACK` | `just gcp-cluster registry-show` | [Cluster registration](#cluster-registration-one-time-per-cluster) |
| Shared namespaces | `namespace-bootstrap` applied | `just gcp-pulumi apply-namespaces` | [`pulumi-setup.md`](../../pulumi-setup.md) |
| GraphQL server | Service `graphql-api` and Deployment `graphql-api-server` | `just dcr-mcp check` | [`graphql-deploy.md`](../../graphql-deploy.md) |
| Cilium Gateway (production) | `Gateway` `dictycr-gateway` and `Issuer` `letsencrypt-issuer` | `just gateway verify` | [`gateway-deploy.md`](../../gateway-deploy.md) |
| `nginx` + `kcert` (legacy only) | Ingress controller and certificate manager | Only when `ingress.enabled` is `true` | — |

### just dcr-mcp check

The recipe is read-only and changes nothing. It needs `KUBECONFIG` and `yq`, and it does not call Pulumi.

```bash
just dcr-mcp check
```

Behavior:

1. Resolves the stack name from `--stack` or `$PULUMI_STACK`, and fails when neither is set.
2. Searches `config/clusters/*.yaml` for the entry whose `stack:` key equals that stack name. A missing entry fails with `Error: no registry entry with stack '<stack>'` and the `register-cluster` command to run.
3. Reads `namespace` from that entry, unless `--namespace` gives another one, and rejects an empty or placeholder value.
4. Checks that the namespace exists, and names `apply-namespaces` when it is absent.
5. Checks that Service `graphql-api` exists in the namespace, and names `graphql_server` when it is absent.
6. Waits for `deployment/graphql-api-server` to roll out inside `--timeout`.

On success it prints the stack and namespace, then `Next: just gcp-pulumi scaffold-dcr-mcp-stack`.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Selects the registry entry by its `stack:` key |
| `--namespace` / `-n` | No | `namespace` from the registry entry | Use it only to check another namespace |
| `--timeout` / `-t` | No | `120s` | Rollout wait for `graphql-api-server`. Accepts `s`, `m`, or `h`. |

---

## Cluster Registration (One Time per Cluster)

Every scaffold recipe reads `config/clusters/*.yaml` and matches the entry whose `stack:` key equals the active stack. A cluster gets its entry one time, during cluster bootstrap, and never again. `config/clusters/` ships `dcr-kube1.yaml` (`stack: dcr-kube1`) only.

`register-cluster` reads files; it creates nothing in the cloud. It therefore runs **after** the env file and the tool manifest exist and carry the Pulumi lines, and **inside** the target cluster's own sub-shell, where `CLUSTER_ENV` and `CLUSTER_NAME` default `--env` and `--cluster`:

```bash
# Outside any sub-shell: write .env.<env>.<cluster> and .tool-versions.<env>.<cluster>
just create-cluster-env --env <env> --cluster <cluster> --project <project-id>

# Enter the sub-shell for that cluster, and stay there
just cluster-env --env <env> --cluster <cluster>

# Fill the PULUMI_* lines of the env file (once per GCP project)
just gcp-pulumi bootstrap-backend

# Write config/clusters/<cluster>.yaml — no flags needed inside the sub-shell
just gcp-cluster register-cluster
```

Full behavior, the eleven keys, and the flag table: [cluster registry](../backend/cluster-registry.md). Cluster bootstrap order: [`pulumi-setup.md`](../../pulumi-setup.md).

**`ERROR: no registry entry with stack '<stack>' (checked config/clusters/*.yaml)` has two causes.** Both appear the same way, so check the shell first:

| Cause | Symptom | Fix |
|-------|---------|-----|
| Wrong sub-shell | `echo "$PULUMI_STACK"` prints a stack with no entry, for example `experiments` in the `dcr-experiments` shell | Leave with `exit`, then enter the production shell: `just cluster-env --env prod --cluster dcr-kube1` |
| Unregistered cluster | `echo "$PULUMI_STACK"` prints the expected stack, and `config/clusters/<cluster>.yaml` is absent | Run `just gcp-cluster register-cluster` in that cluster's sub-shell, as shown above |

`register-cluster` is describe-then-create: an existing entry is never overwritten. Editing the registry by hand stays a manual action.

---

## Create the Stack

`scaffold-dcr-mcp-stack` initializes the stack with the registry KMS key and renders `dcr-mcp/Pulumi.<stack>.yaml` from `config/templates/dcr-mcp-stack.yaml.tmpl`.

```bash
just gcp-pulumi scaffold-dcr-mcp-stack
```

Behavior:

1. Resolves the stack name from `--stack` or `$PULUMI_STACK`.
2. Finds the registry entry whose `stack:` key matches, and reads `namespace` and `kms_secrets_provider` from it. A missing entry fails ([cluster registration](#cluster-registration-one-time-per-cluster)).
3. Refuses to overwrite an existing `dcr-mcp/Pulumi.<stack>.yaml`.
4. Runs `pulumi stack init` with the registry KMS key, then appends the rendered `config:` block.
5. Appends `graphqlEndpoint` only when `--graphql-endpoint` gives a URL. Without the flag the key stays absent, and the server reads `graphql-api` inside the cluster ([endpoint resolution](#graphql-endpoint-resolution)).
6. Validates that `name` is `dcr-mcp`, that `namespace` equals the registry value, and that the file holds no `:latest` tag.

It prints `Next: just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value <immutable-tag>`.

When `dcr-mcp/Pulumi.<stack>.yaml` already exists in Git, select the stack instead:

```bash
just gcp-pulumi ensure-stack --folder dcr-mcp
```

### Baseline Stack File

```yaml
config:
  dcr-mcp:properties:
    allowedOrigins:
      - "*"
    command: start-server
    host: 0.0.0.0
    image:
      name: dictybase/dcr-mcp
      tag: bootstrap
    ingress:
      enabled: false
    name: dcr-mcp
    namespace: <registry namespace>
    port: <--port, default 8080>
    rateLimit: <--rate-limit, default 120>
    stateless: true
```

**Every value stays plain text.** The server uses no secret and no credential, so `set-secret` has no use in this project.

### Keys That Change per Cluster

`dcr-mcp` is not deployed on the legacy `dcr-experiments` cluster: the project ships no `Pulumi.experiments.yaml`, and `config/clusters/` has no `dcr-experiments.yaml`. The second column describes the posture to use if you deploy it there.

| Key | Production (`dcr-kube1`) | Legacy `nginx` cluster |
|-----|--------------------------|------------------------|
| `namespace` | `prod` | `dev` |
| `graphqlEndpoint` | absent — read in-cluster | absent — read in-cluster |
| `ingress.enabled` | `false`; the `gateway/` stack routes `routes.mcp` | `true`; the stack creates `dcr-mcp-ingress` |
| `ingress.hosts` | absent; set `gateway/` `routes.mcp.hosts.0` | dedicated host, for example `mcp.dictybase.dev` |
| `ingress.path` | absent; the Gateway route uses `/` | `/` |
| `ingress.tlsSecret` | absent; the Gateway route uses `mcp-tls` | `kcert`-managed Secret name |
| `ingress.label` | absent | `name: kcert.dev/ingress`, `value: managed` |
| `image.tag` | release tag or `develop-<sha>` | release tag or `develop-<sha>` |

---

## Image Tag

The template writes `image.tag: bootstrap`, which is a placeholder, not a published tag. A deploy that keeps it ends in `ErrImagePull`, and `just dcr-mcp verify` fails early with `tag is 'bootstrap'`.

```bash
just ci latest-tag --repo dictyBase/dcr-mcp
just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value <immutable-tag>
```

The Pulumi config check also rejects a tag that still holds `<...>` placeholder text, and the scaffold rejects `:latest`. A production stack always carries an immutable tag, because a CI tag deploy rewrites this one key and nothing else ([CI deployment](#ci-deployment)).

---

## GraphQL Endpoint Resolution

The server resolves one GraphQL URL at startup, in this order:

1. **Explicit override.** When `properties.graphqlEndpoint` holds a URL, Pulumi passes `--graphql-endpoint <url>`. The flag also reads `GRAPHQL_ENDPOINT`. The value is used as given.
2. **In-cluster Service discovery (default).** When the key is absent, Pulumi passes no endpoint flag. Kubernetes gives the pod the address of Service `graphql-api`, and the server builds `http://<host>:<port>/graphql` from it. The port defaults to `8080`. Traffic stays inside the cluster network and does not leave through the external load balancer.
3. **Neither.** The container fails closed at startup with `graphql-endpoint or GRAPHQL_API_SERVICE_HOST must be set`.

| Flag | Source environment variables | Default | Purpose |
|------|------------------------------|---------|---------|
| `--graphql-endpoint` (alias `--endpoint`) | `GRAPHQL_ENDPOINT` | *(none)* | Full URL. Wins over the two flags below. |
| `--graphql-host` | `GRAPHQL_API_SERVICE_HOST`, `GRAPHQL_API_PORT_8080_TCP_ADDR` | *(none)* | Address of Service `graphql-api` |
| `--graphql-port` | `GRAPHQL_API_SERVICE_PORT`, `GRAPHQL_API_PORT_8080_TCP_PORT` | `8080` | Port of Service `graphql-api` |

**Kubernetes gives these variables only to a pod that starts after Service `graphql-api` exists.** A pod that started first keeps no address and exits. That is why `just dcr-mcp check` runs before the scaffold. After `graphql-api` exists, restart the Deployment:

```bash
kubectl -n <namespace> rollout restart deployment/dcr-mcp-api-server
```

**Change of behavior.** An earlier `dcr-mcp` build carried a compiled-in default endpoint, `https://graphql.dictybase.dev/graphql`. Current builds carry no default. A production pod that loses its endpoint now fails at startup instead of reading the lab server. Confirm an upgrade from such an older image with [verification](#verification).

---

## Server Options and Container Flags

The binary uses the subcommand `start-server` (aliases `serve` and `start`). Pulumi passes the subcommand first, then the flags, and includes `--graphql-endpoint` only when `properties.graphqlEndpoint` is non-empty:

```text
start-server [--graphql-endpoint <url>] --host <host> --port <port> --stateless=<bool> --rate-limit <n> --allowed-origins <origin>...
```

| Pulumi config key | CLI flag | Environment variable | Default | Purpose |
|-------------------|----------|----------------------|---------|---------|
| `properties.command` | subcommand | — | `start-server` | `start-server`, `serve`, or `start` |
| `properties.graphqlEndpoint` | `--graphql-endpoint` | `GRAPHQL_ENDPOINT` | *(absent)* | [Endpoint override](#graphql-endpoint-resolution) |
| `properties.host` | `--host` | `MCP_HOST` | `0.0.0.0` | Bind address inside the container |
| `properties.port` | `--port` | `MCP_PORT` | `8080` | TCP port for `/mcp` and `/healthz`. Range `0`–`65535`. |
| `properties.stateless` | `--stateless=<bool>` | `MCP_STATELESS` | `true` | Streamable HTTP stateless mode |
| `properties.rateLimit` | `--rate-limit` | `MCP_RATE_LIMIT` | `120` | Maximum `/mcp` requests for each client IP address in one minute. Minimum `1`. |
| `properties.allowedOrigins` | `--allowed-origins` (repeatable) | `ALLOWED_ORIGINS` | `["*"]` | CORS origins for browser clients |

The Pulumi program applies the same defaults when a key is absent, so an omitted key and the default value give the same container arguments.

### Stateless Streamable HTTP and Caching

- **`stateless: true` (default)** keeps no session state between requests, so any replica answers any request. No sticky session is necessary behind the Service, the Ingress, or the Gateway.
- **The response cache is per replica.** Each pod keeps successful `get_strain` and `get_plasmid` results in memory for 10 minutes, up to 1,024 entries for each entity type. Failed lookups are not cached. Two replicas therefore hold two independent caches.

### Rate Limiting and Proxy Headers

- The limit applies to `/mcp` for each client IP address.
- `GET /healthz` is exempt, so the readiness and liveness probes never receive HTTP `429`.
- The client IP address comes from `X-Forwarded-For` only when the request arrives from a trusted private or loopback hop (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.0/8`, `::1/128`, `fc00::/7`). Both the Gateway Envoy proxy and the `nginx` Ingress controller are inside those ranges.

### CORS Origins

- Browser MCP clients send a preflight `OPTIONS /mcp` request with `Content-Type`, `Accept`, `Mcp-Session-Id`, and `MCP-Protocol-Version`. The server allows and exposes those headers.
- Server-to-server connectors (Claude.ai, ChatGPT, Claude Code, Codex CLI, Cursor) ignore CORS headers. Keep `["*"]` unless you must limit browser origins.

### Citation Links Emitted by Tools

Tool responses carry fixed citation base domains, which are compiled into the image and are not configurable:

| Target | URL pattern |
|--------|-------------|
| Strain page | `https://dictycr.org/strains/<DBS_ID>` |
| Plasmid page | `https://dictycr.org/plasmids/<DBP_ID>` |
| PubMed record | `https://pubmed.ncbi.nlm.nih.gov/<PMID>/` |
| DOI | `https://doi.org/<DOI>` |

---

## Routing Architecture: Gateway vs Legacy Ingress

In both modes **`dcr-mcp` takes one dedicated hostname and the root path `/`, never a URL subpath.** The binary serves `/mcp` and `/healthz` at the root of its own host, so a `PathPrefix` of `/` on `<mcp-host>` exposes `https://<mcp-host>/mcp` for AI clients and `https://<mcp-host>/healthz` for the health probe.

| Mode | Cluster | Path to `dcr-mcp-api:8080` | `ingress.enabled` | Certificate manager |
|------|---------|----------------------------|-------------------|---------------------|
| Gateway (recommended) | `dcr-kube1` and new clusters | `Gateway` `dictycr-gateway` + `HTTPRoute` `mcp-route` from the `gateway/` stack | `false` | `cert-manager`, `Issuer` `letsencrypt-issuer` to Secret `mcp-tls` |
| Legacy (optional) | `dcr-experiments` and other `nginx` clusters | `Ingress` `dcr-mcp-ingress` from the `dcr-mcp/` stack | `true` | `kcert`, label `kcert.dev/ingress: managed` |

### Production Gateway Route (Recommended)

Keep `properties.ingress.enabled` at `false`. Enable the route after the Service exists, because `just gateway check` fails when a route points at a missing Service:

```bash
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value true
just gateway deploy
```

The `gateway/` stack then creates an HTTP listener (`http-mcp-0`, port `80`) and an HTTPS listener (`https-mcp-0`, port `443`, Secret `mcp-tls`) for `<mcp-host>`, plus `HTTPRoute` `mcp-route` from `PathPrefix` `/` to `dcr-mcp-api:8080`. `cert-manager` completes the Let's Encrypt HTTP-01 challenge and writes `mcp-tls` only after the DNS `A` record for `<mcp-host>` resolves to the address from `just gateway show-ip` ([Gateway MCP route](../gateway/deployment.md#mcp-route)).

### Legacy Ingress Mode (`dcr-experiments` Only)

Register the cluster first ([cluster registration](#cluster-registration-one-time-per-cluster)), scaffold the stack, then set `hosts.0`, `path`, `tlsSecret`, and `label` **before** `enabled`. The config check fails closed with `ingress.enabled=true but ingress is missing hosts, path, tlsSecret` when a field is empty, and rejects `<...>` placeholder text in `hosts` or `tlsSecret`.

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

### Turn the Legacy Ingress Off

**CAUTION: On a stack that already has `dcr-mcp-ingress`, setting `enabled` to `false` deletes the Ingress and ends external traffic through `nginx`.** To migrate a cluster to the Gateway, deploy the Gateway route first, move the DNS record, then read the preview before you apply.

```bash
just gcp-pulumi set-config --folder dcr-mcp --key properties.ingress.enabled --value false
just gcp-pulumi preview --folder dcr-mcp
```

---

## Deploy Behavior

`create-resource` drives the Pulumi Automation API. It previews first, blocks any plan that deletes or replaces a protected resource, skips the update when the plan has no change, and otherwise applies the exact saved plan. For an upgrade, change `properties.image.tag` and run `preview` and `create-resource` again.

### Recipe Flags

| Recipe | Flag | Required | Default | Notes |
|--------|------|----------|---------|-------|
| `scaffold-dcr-mcp-stack` | `--stack` / `-s` | No | `$PULUMI_STACK` | Must match a `stack:` key in `config/clusters/*.yaml` |
| `scaffold-dcr-mcp-stack` | `--graphql-endpoint` | No | *(omitted — read in-cluster)* | Writes `properties.graphqlEndpoint` |
| `scaffold-dcr-mcp-stack` | `--port` / `-p` | No | `8080` | Container and Service TCP port |
| `scaffold-dcr-mcp-stack` | `--rate-limit` | No | `120` | Requests for each client IP address in one minute |
| `ensure-stack`, `preview`, `create-resource`, `set-config` | `--folder` / `-f` | Yes | — | Pass `dcr-mcp` |
| `ensure-stack`, `preview`, `create-resource`, `set-config` | `--stack` / `-s` | No | `$PULUMI_STACK` | `ensure-stack` fails closed when `Pulumi.<stack>.yaml` is absent |
| `set-config` | `--key` / `-k` | Yes | — | Path under `dcr-mcp:properties`. A dotted index such as `.0` becomes `[0]`. |
| `set-config` | `--value` / `-v` | Yes | — | Plain-text value |

---

## Verification

`just dcr-mcp verify` is read-only. It changes nothing, and you can run it at any time. It reads `dcr-mcp/Pulumi.<stack>.yaml`, so no flag is necessary in the cluster-env sub-shell.

```bash
just dcr-mcp verify
just gateway verify
```

The recipe performs these actions:

1. Fails when the tag is empty, is the placeholder `bootstrap`, or holds `<...>` placeholder text.
2. Waits for `deployment/dcr-mcp-api-server` to roll out.
3. Compares the running container image with `<image.name>:<image.tag>` from the stack file.
4. Checks that `service/dcr-mcp-api` exists.

It prints one `PASS` or `FAIL` line, and on success `Next: just gateway verify`.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Selects `dcr-mcp/Pulumi.<stack>.yaml` |
| `--namespace` / `-n` | No | `properties.namespace` from the stack file | Use it only to check another namespace |
| `--timeout` / `-t` | No | `120s` | Rollout wait for the Deployment. Accepts `s`, `m`, or `h`. |

The recipe does not call Pulumi and does not need the Pulumi credentials. It needs `KUBECONFIG` and `yq`. The Gateway check passes only after the DNS record resolves and `cert-manager` writes `mcp-tls` ([Gateway MCP route](../gateway/deployment.md#mcp-route)).

After both recipes pass, probe the public host from any machine:

```bash
# Health endpoint — expect {"status":"ok"}
curl -fsSL https://<mcp-host>/healthz

# Streamable HTTP initialize handshake
curl -fsSL -X POST https://<mcp-host>/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"verify","version":"1.0.0"}}}'
```

Once `initialize` answers, connect an AI assistant (Claude.ai, Claude Desktop, ChatGPT, Claude Code, Codex CLI, Cursor, Windsurf, or GitHub Copilot) with the MCP URL `https://<mcp-host>/mcp`.

---

## CI Deployment

In `dictyBase/dcr-mcp`, `.github/workflows/ci.yml` runs the unit tests and the coverage upload on pushes to `develop`. It deploys nothing, because `dcr-mcp` does not run on the legacy lab cluster.

A release tag triggers `.github/workflows/tag-build.yaml`, which calls `composite-deploy.yaml` in `dictyBase/workflows`. That workflow:

1. Builds the distroless image from `build/package/Dockerfile` and publishes `dictybase/dcr-mcp:<tag>` to Docker Hub.
2. Authenticates to GCP with `secrets.PROD_DEPLOY_SA_KEY` and exports the cluster kubeconfig from `vars.PROD_KOPS_STATE_STORAGE`.
3. Clones `dictybase-docker/cluster-ops` at branch **`develop`**, selects the stack in folder `dcr-mcp/` from `vars.PROD_PULUMI_STATE_STORAGE`, sets `properties.image.tag` to the tag, and runs `pulumi up`.

**Push order: commit and push `dcr-mcp/Pulumi.<stack>.yaml` to cluster-ops `develop` before you cut the release tag.** The clone at step 3 sees only what `develop` holds, so an unpushed stack file deploys nothing.

---

## Tag Deploy Setup

`config/services.yaml` lists the ArangoDB-backed modware gRPC services only, and `dcr-mcp` is not one of them. `just ci deploy-backend-services` and `just ci pr-deploy-workflows` therefore skip it, and the five recipes below do the same work for one repository. Run them in the production `cluster-env` sub-shell.

```bash
# 1. Mint or reuse credentials/<cluster>/deployer.json, then preflight it
just ci create-deploy-key

# 2. Publish the org variable PROD_PULUMI_STATE_STORAGE and render composite-deploy.yaml
just ci render-composite-deploy

# 3. Add dictyBase/dcr-mcp to the selected repositories of PROD_DEPLOY_SA_KEY
just ci set-deploy-secret --repos dictyBase/dcr-mcp

# 4. Publish the five <CI_ENV>_* repository variables from the registry entry
just ci sync-deploy-vars --repos dictyBase/dcr-mcp

# 5. Render the deploy-only tag workflow
just ci render-tag-deploy --app dcr-mcp --project dcr-mcp --out bin/ci-render/dcr-mcp-tag-build.yaml
```

Steps 1 and 2 are per cluster, and they are shared with every other service on that cluster — skip them when [`backend-service-deploy.md`](../../backend-service-deploy.md) already ran them. Step 5 renders `app: dcr-mcp`, `project: dcr-mcp`, `docker_image: dcr-mcp`, `environment: production`, `runner: backend`.

What each recipe does, and the variables it writes:
[deploy credentials](../backend/ci-credentials.md) for steps 1 and 3,
[CI variables](../backend/ci-variables.md) for steps 3 and 4,
[workflow render](../backend/render-deploy-workflows.md) for steps 2 and 5.

The rendered file is inert until it lands in the service repository. Copy `bin/ci-render/dcr-mcp-tag-build.yaml` to `.github/workflows/tag-build.yaml` in `dictyBase/dcr-mcp`, open a pull request against `develop`, and merge it before the next release tag.

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `no stack name — set PULUMI_STACK (via cluster env) or pass --stack` | The recipe ran outside the `cluster-env` sub-shell | Enter `just cluster-env --env <env> --cluster <cluster>`, or pass `--stack <name>`. |
| `no registry entry with stack '<stack>' (checked config/clusters/*.yaml)` | Wrong sub-shell, or the cluster has no registry entry | Compare `echo "$PULUMI_STACK"` with the two causes in [cluster registration](#cluster-registration-one-time-per-cluster). |
| `Error: namespace '<ns>' not found` from `just dcr-mcp check` | `namespace-bootstrap` has not run on this cluster | Run `just gcp-pulumi apply-namespaces`. |
| `Error: service/graphql-api not found` from `just dcr-mcp check` | The upstream GraphQL server is not deployed in that namespace | Deploy it ([`graphql-deploy.md`](../../graphql-deploy.md)), then run `just dcr-mcp check` again. |
| `Error: yq not found on PATH — run prepare-tools` | The pinned tools are not installed for this cluster | Run `just prepare-tools` in the sub-shell. |
| `ERROR: stack '<stack>' does not exist in dcr-mcp, and there is no Pulumi.<stack>.yaml to initialize it from` | First deploy on this cluster. `ensure-stack` creates a stack only from a stack file that exists. | Run `just gcp-pulumi scaffold-dcr-mcp-stack` ([create the stack](#create-the-stack)). |
| `ERROR: dcr-mcp/Pulumi.<stack>.yaml already exists — refusing to overwrite` | The stack file exists | Run `ensure-stack`, or edit the file with `set-config`. |
| `dcr-mcp:properties is missing namespace, image.tag` | A required key is empty | Set it with `just gcp-pulumi set-config --folder dcr-mcp --key properties.<key> --value <val>`. |
| `dcr-mcp:properties contains placeholder in image.tag, graphqlEndpoint` | The value still holds `<...>` text | Pin a real tag ([image tag](#image-tag)), and set or remove `graphqlEndpoint`. |
| Pod exits with `graphql-endpoint or GRAPHQL_API_SERVICE_HOST must be set` | The pod started before Service `graphql-api` existed, and no endpoint override is set | Deploy `graphql_server`, then run `kubectl -n <namespace> rollout restart deployment/dcr-mcp-api-server` ([endpoint resolution](#graphql-endpoint-resolution)). |
| `FAIL dcr-mcp: tag is 'bootstrap'` | The placeholder tag was never replaced | Pin a tag ([image tag](#image-tag)), then apply again. |
| `FAIL dcr-mcp: running image <a>, stack file says <b>` | The stack file changed and `create-resource` did not run, or a CI run set another tag | Run `preview` and `create-resource` again. |
| `FAIL dcr-mcp: service/dcr-mcp-api not found` | The Service was deleted, or `properties.name` changed | Run `create-resource` again ([deploy behavior](#deploy-behavior)). |
| `ErrImagePull` with a real tag | The tag does not exist on Docker Hub | Resolve the published tag with `just ci latest-tag --repo dictyBase/dcr-mcp`. |
| `ingress.enabled=true but ingress is missing hosts, path, tlsSecret` | The Ingress was enabled before the four keys were set | Set `hosts.0`, `path`, `tlsSecret`, and `label.*` first ([legacy Ingress mode](#legacy-ingress-mode-dcr-experiments-only)). |
| `just gateway check` reports `services "dcr-mcp-api" not found` | `routes.mcp.enabled` was set before the `dcr-mcp` stack was applied | Run `just gcp-pulumi create-resource --folder dcr-mcp`, then `just gateway deploy`. |
| `just gateway check` reports an empty or placeholder host for route `mcp` | `routes.mcp.hosts.0` still holds `<mcp-host>` | Set the real hostname with `set-config` on the `gateway` folder. |
| Tool calls fail with `listStockSuggestions query failed` or `searchStocks query failed` | The upstream GraphQL server is unreachable from the pod | Check `graphql-api-server` with `just dcr-mcp check`, and check `properties.graphqlEndpoint` when it is set. |
| Clients receive HTTP `429` on `/mcp` | One client IP address passed `properties.rateLimit` in a minute | Raise `properties.rateLimit`, then apply again. |
| `just ci deploy-backend-services --services dcr-mcp` reports no match | `config/services.yaml` lists ArangoDB-backed services only | Use the recipes in [tag deploy setup](#tag-deploy-setup). |
| A tag deploy runs in GitHub Actions but changes nothing | `dcr-mcp/Pulumi.<stack>.yaml` was not pushed to cluster-ops `develop` | Push the stack file, then re-run the workflow ([CI deployment](#ci-deployment)). |
