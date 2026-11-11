# dcr-mcp Server Deployment Details

Back to: [dcr-mcp Server Deploy Guide](../../dcr-mcp-deploy.md)

Details for the `dcr-mcp/` Pulumi project: the Kubernetes resources, the two read-only recipes (`check` and `verify`), the cluster registration prerequisite, the stack scaffold, the bootstrap order that gives the project its first image, the GraphQL endpoint resolution, the container flags, the two routing modes, the two CI pipelines, and the troubleshooting table.

## Table of Contents

- [What the Project Creates](#what-the-project-creates)
- [Prerequisites](#prerequisites)
  - [just dcr-mcp check](#just-dcr-mcp-check)
- [Cluster Registration (One Time per Cluster)](#cluster-registration-one-time-per-cluster)
  - [Supported Target Clusters](#supported-target-clusters)
- [Create the Stack](#create-the-stack)
  - [Baseline Stack File](#baseline-stack-file)
  - [Keys That Change per Cluster](#keys-that-change-per-cluster)
- [Bootstrap Order on a New Cluster](#bootstrap-order-on-a-new-cluster)
  - [Why the Order Is Circular Without It](#why-the-order-is-circular-without-it)
- [Image Tag](#image-tag)
  - [Find a Published Tag](#find-a-published-tag)
- [GraphQL Endpoint Resolution](#graphql-endpoint-resolution)
- [Server Options and Container Flags](#server-options-and-container-flags)
  - [Stateless Streamable HTTP and Caching](#stateless-streamable-http-and-caching)
  - [Rate Limiting and Proxy Headers](#rate-limiting-and-proxy-headers)
  - [CORS Origins](#cors-origins)
  - [Citation Links Emitted by Tools](#citation-links-emitted-by-tools)
- [Routing Architecture: Gateway vs Legacy Ingress](#routing-architecture-gateway-vs-legacy-ingress)
  - [Production Gateway Route (Recommended)](#production-gateway-route-recommended)
  - [Legacy Ingress Mode (`dcr-experiments`)](#legacy-ingress-mode-dcr-experiments)
  - [Turn the Legacy Ingress Off](#turn-the-legacy-ingress-off)
- [Deploy Behavior](#deploy-behavior)
  - [Recipe Flags](#recipe-flags)
- [Verification](#verification)
- [CI Deployment](#ci-deployment)
  - [Developer CI on `develop`](#developer-ci-on-develop)
  - [Release Tag CI](#release-tag-ci)
  - [Push Order](#push-order)
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

Every scaffold recipe reads `config/clusters/*.yaml` and matches the entry whose `stack:` key equals the active stack. A cluster gets its entry one time, during cluster bootstrap, and never again. A cluster without an entry fails the scaffold with `ERROR: no registry entry with stack '<stack>' (checked config/clusters/*.yaml).`

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

**`ERROR: no registry entry with stack '<stack>' (checked config/clusters/*.yaml)` has two causes.** Both appear the same way, so read `echo "$PULUMI_STACK"` first and compare it with the table below:

| Cause | Symptom | Fix |
|-------|---------|-----|
| Unregistered cluster | `$PULUMI_STACK` holds the expected stack, and `config/clusters/<cluster>.yaml` is absent | Run `just gcp-cluster register-cluster` in that cluster's sub-shell, as shown above |
| Wrong sub-shell | `$PULUMI_STACK` holds a stack that belongs to another cluster, or is empty | Leave with `exit`, then enter the correct shell: `just cluster-env --env <env> --cluster <cluster>` |

`register-cluster` is describe-then-create and idempotent: when `config/clusters/<cluster>.yaml` already exists, the recipe keeps the existing entry without overwriting it, validates it with `registry-show`, and exits `0`. Editing the registry by hand stays a manual action.

### Supported Target Clusters

Both entries exist in `config/clusters/`. The `stack:` key of the entry is the Pulumi stack name, and it does not need to equal the cluster name.

| Registry entry | `stack` | `namespace` | `ci_env` | External route | Pinned tool versions |
|----------------|---------|-------------|----------|----------------|----------------------|
| `dcr-kube1.yaml` | `dcr-kube1` | `prod` | `PROD` | Cilium Gateway + `cert-manager` ([Gateway route](#production-gateway-route-recommended)) | kOps `1.36.1`, kubectl `1.35.8`, Pulumi `3.255.0` |
| `dcr-experiments.yaml` | `experiments` | `dev` | `DEV` | `nginx` Ingress + `kcert` ([legacy Ingress](#legacy-ingress-mode-dcr-experiments)) | kOps `1.29.2`, kubectl `1.28.8`, Pulumi `3.108.0` |

`experiments` is the stack that the developer CI workflow of `dictyBase/dcr-mcp` deploys on each push to `develop` ([developer CI](#developer-ci-on-develop)). `PULUMI_STACK=experiments` in the `dcr-experiments` sub-shell is therefore correct, not a shell mistake.

---

## Create the Stack

`scaffold-dcr-mcp-stack` initializes the stack with the registry KMS key and renders `dcr-mcp/Pulumi.<stack>.yaml` from `config/templates/dcr-mcp-stack.yaml.tmpl`. The recipe does two things that the CI deploy depends on: it creates the stack **in the Pulumi backend** of that cluster, and it writes the stack file that CI reads from Git ([bootstrap order](#bootstrap-order-on-a-new-cluster)).

```bash
just gcp-pulumi scaffold-dcr-mcp-stack
```

Behavior:

1. Resolves the stack name from `--stack` or `$PULUMI_STACK`.
2. Finds the registry entry whose `stack:` key matches, and reads `namespace` and `kms_secrets_provider` from it. A missing entry fails ([cluster registration](#cluster-registration-one-time-per-cluster)).
3. Keeps an existing `dcr-mcp/Pulumi.<stack>.yaml` without overwriting it and exits `0`.
4. Initializes or selects the stack with the registry KMS key, then appends the rendered `config:` block.
5. Appends `graphqlEndpoint` only when `--graphql-endpoint` gives a URL. Without the flag the key stays absent, and the server reads `graphql-api` inside the cluster ([endpoint resolution](#graphql-endpoint-resolution)).
6. Validates that `name` is `dcr-mcp`, that `namespace` equals the registry value, and that the file holds no `:latest` tag.

It prints `Next: configure stack options, push <stack file> to cluster-ops develop, or pin properties.image.tag`.

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

`dcr-mcp/` ships no `Pulumi.<stack>.yaml` for either cluster yet, so both columns describe a stack that the scaffold creates. The `experiments` column follows the pattern that `graphql_server/Pulumi.experiments.yaml` already uses on the same cluster.

| Key | Production (`dcr-kube1`) | Legacy (`experiments`) |
|-----|--------------------------|------------------------|
| `namespace` | `prod` | `dev` |
| `graphqlEndpoint` | absent — read in-cluster | absent — read in-cluster |
| `ingress.enabled` | `false`; the `gateway/` stack routes `routes.mcp` | `true`; the stack creates `dcr-mcp-ingress` |
| `ingress.hosts` | absent; set `gateway/` `routes.mcp.hosts.0` | dedicated host, for example `mcp.dictybase.dev` |
| `ingress.path` | absent; the Gateway route uses `/` | `/` |
| `ingress.tlsSecret` | absent; the Gateway route uses `mcp-tls` | `kcert`-managed Secret name |
| `ingress.label` | absent | `name: kcert.dev/ingress`, `value: managed` |
| `image.tag` | `develop-<sha>` until a release tag exists | `develop-<sha>` written by the developer CI run |

---

## Bootstrap Order on a New Cluster

The first setup of `dcr-mcp` has no published image, no release tag, and no stack file in Git. These six steps are the only order that works. Steps 1, 2, 3, and 6 run in the cluster-env sub-shell of the target cluster. Step 5 runs in GitHub Actions.

| Step | Action | Why it is in this position |
|------|--------|----------------------------|
| 1 | `just gcp-cluster register-cluster` (one time), then `just dcr-mcp check` | Every scaffold recipe resolves the namespace and the KMS key from the registry entry ([cluster registration](#cluster-registration-one-time-per-cluster)) |
| 2 | `just gcp-pulumi scaffold-dcr-mcp-stack` | Creates the stack in the Pulumi backend and writes `dcr-mcp/Pulumi.<stack>.yaml` with `image.tag: bootstrap`. CI cannot select a stack that does not exist. |
| 3 | `set-config` for the optional keys, including `ingress.*` on `experiments` | A later CI run overwrites `image.tag` only, so every other key must be correct before the first `pulumi up` |
| 4 | Commit and push `dcr-mcp/Pulumi.<stack>.yaml` to cluster-ops `develop` | CI clones this repository at `develop` and applies the stack file that the clone holds ([CI deployment](#ci-deployment)) |
| 5 | Push or merge into `develop` in `dictyBase/dcr-mcp` | The run publishes `dictybase/dcr-mcp:develop-<sha>` to Docker Hub, then sets `properties.image.tag` in its own clone and runs `pulumi up` |
| 6 | `set-config properties.image.tag <develop-sha>`, then push to cluster-ops `develop` | The CI clone is thrown away, so the deployed tag reaches Git only when you write it there ([image tag](#image-tag)) |

**`bootstrap` in the pushed stack file of step 4 is correct.** CI replaces the tag in its clone before `pulumi up`, so the placeholder never reaches a pod. Step 6 makes the repository agree with the cluster, which is what `just dcr-mcp verify` compares.

### Why the Order Is Circular Without It

Three dependencies make every other order fail:

- **No tag to pin.** `dictyBase/dcr-mcp` carries no semver tag, so `just ci latest-tag --repo dictyBase/dcr-mcp` fails with `ERROR: no semver tags found for dictyBase/dcr-mcp.` Pinning a tag before the first build is not possible.
- **No image to pull.** Docker Hub receives `dictybase/dcr-mcp:<tag>` only from a CI run. A local `create-resource` with any invented tag ends in `ErrImagePull`.
- **CI reads this repository, not your working copy.** The deploy step clones `dictybase-docker/cluster-ops` at `develop`. A stack file that stays on your machine is invisible to it.

The two failures that follow a wrong order are distinct, and the error text identifies which one happened:

| Missing piece | What CI does | Error |
|---------------|--------------|-------|
| Step 2 skipped (no stack in the Pulumi backend) | Fails at the config step, before `pulumi up` | `error: no stack named '<stack>' found` |
| Step 4 skipped (stack file not pushed) | Writes a stack file that holds `image.tag` only, then fails in `pulumi up` | `dcr-mcp:properties is missing namespace` |

In both cases the image publish step already finished, because it runs before the deploy step. The `develop-<sha>` tag is therefore on Docker Hub, and you can finish the rollout with `preview` and `create-resource` after you repair the missing step.

---

## Image Tag

The template writes `image.tag: bootstrap`, which is a placeholder, not a published tag. A deploy that keeps it ends in `ErrImagePull`, and `just dcr-mcp verify` fails early with `tag is 'bootstrap'`. The Pulumi config check also rejects a tag that holds `<...>` placeholder text, and the scaffold rejects `:latest`.

```bash
just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value <immutable-tag>
```

Two tag forms are valid here, and each one identifies one commit:

| Form | Who writes it | Source |
|------|---------------|--------|
| `develop-<7-char sha>` | The developer CI run on `develop` | The commit at the head of `develop` in `dictyBase/dcr-mcp` |
| `<semver>` (for example `v1.2.0`) | A release tag run, after [tag deploy setup](#tag-deploy-setup) | The Git tag name, used verbatim |

### Find a Published Tag

`just ci latest-tag --repo dictyBase/dcr-mcp` reads GitHub release tags. It is the right command **after** the first release tag exists, and it fails with `ERROR: no semver tags found for dictyBase/dcr-mcp.` until then. Until that day, build the `develop-<sha>` tag from the head of `develop`:

```bash
# Tag that the last successful develop run published
echo "develop-$(gh api repos/dictyBase/dcr-mcp/commits/develop --jq '.sha[0:7]')"

# Release tags, after the first one exists
just ci latest-tag --repo dictyBase/dcr-mcp
```

The CI run shortens the commit hash to 7 characters, so the two values always agree when the run deployed the current head of `develop`. At the time of writing, Docker Hub holds one `dictybase/dcr-mcp` tag, `develop-4e47267`, and `dictyBase/dcr-mcp` holds no release tag.

A production stack always carries one of these two forms, never `latest`, because a CI deploy rewrites this one key and nothing else ([CI deployment](#ci-deployment)).

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
| Legacy (optional) | `dcr-experiments` (stack `experiments`) and other `nginx` clusters | `Ingress` `dcr-mcp-ingress` from the `dcr-mcp/` stack | `true` | `kcert`, label `kcert.dev/ingress: managed` |

### Production Gateway Route (Recommended)

Keep `properties.ingress.enabled` at `false`. Enable the route after the Service exists, because `just gateway check` fails when a route points at a missing Service:

```bash
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.hosts.0 --value <mcp-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.mcp.enabled --value true
just gateway deploy
```

The `gateway/` stack then creates an HTTP listener (`http-mcp-0`, port `80`) and an HTTPS listener (`https-mcp-0`, port `443`, Secret `mcp-tls`) for `<mcp-host>`, plus `HTTPRoute` `mcp-route` from `PathPrefix` `/` to `dcr-mcp-api:8080`. `cert-manager` completes the Let's Encrypt HTTP-01 challenge and writes `mcp-tls` only after the DNS `A` record for `<mcp-host>` resolves to the address from `just gateway show-ip` ([Gateway MCP route](../gateway/deployment.md#mcp-route)).

### Legacy Ingress Mode (`dcr-experiments`)

The `experiments` stack owns its external route, because that cluster has no Gateway. Register the cluster first ([cluster registration](#cluster-registration-one-time-per-cluster)), scaffold the stack, then set `hosts.0`, `path`, `tlsSecret`, and `label` **before** `enabled`. The config check fails closed with `ingress.enabled=true but ingress is missing hosts, path, tlsSecret` when a field is empty, and rejects `<...>` placeholder text in `hosts` or `tlsSecret`.

Set these keys before the first CI run ([bootstrap order](#bootstrap-order-on-a-new-cluster)): a CI deploy rewrites `image.tag` only, so an unconfigured Ingress stays absent until you push the keys and the next run applies them. `graphql_server/Pulumi.experiments.yaml` on the same cluster shows the house pattern: host `graphql.dictybase.dev`, `tlsSecret: graphql-tls-secret`, label `kcert.dev/ingress: managed`.

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

Two pipelines build and deploy `dcr-mcp`, and both of them call `composite-deploy.yaml` in `dictyBase/workflows`. One exists today; the other needs [tag deploy setup](#tag-deploy-setup) first.

| Pipeline | Trigger | Workflow file | Target stack | Image tag |
|----------|---------|---------------|--------------|-----------|
| Developer CI | Push or merge into `develop` in `dictyBase/dcr-mcp` | `.github/workflows/ci.yml` (exists) | `experiments` on `dcr-experiments` | `develop-<7-char sha>` |
| Release tag | A Git tag in `dictyBase/dcr-mcp` | `.github/workflows/tag-build.yaml` (not added yet) | `$PULUMI_STACK` of the rendered file, for example `dcr-kube1` | The tag name, verbatim |

Both pipelines perform the same four actions in this order, and the order explains every partial failure:

1. Build the distroless image from `build/package/Dockerfile`, and publish it to Docker Hub.
2. Export the cluster kubeconfig from the kOps state bucket of the target cluster.
3. Clone `dictybase-docker/cluster-ops` at branch **`develop`**, then run `pulumi -C dcr-mcp -s <stack> config set --path properties.image.tag <tag>` inside that clone.
4. Run `pulumi -C dcr-mcp -s <stack> up` against the cloned tree.

**The clone is thrown away at the end of the run.** The new tag never returns to this repository, so record it with `set-config` and push it ([image tag](#image-tag)). When step 1 succeeds and step 4 fails, the image stays on Docker Hub, and you can pin that tag and apply the stack from your shell.

### Developer CI on `develop`

`.github/workflows/ci.yml` in `dictyBase/dcr-mcp` runs on pushes to `develop`. The `test` job runs `go test`, and the `call-build-deploy` job then calls `dictyBase/workflows/.github/workflows/composite-deploy.yaml@develop` with these inputs:

| Input | Value | Effect |
|-------|-------|--------|
| `app`, `project`, `docker_image` | `dcr-mcp` | Folder `dcr-mcp/` in this repository, image `dictybase/dcr-mcp` |
| `stack` | `experiments` | Needs `config/clusters/dcr-experiments.yaml` and `dcr-mcp/Pulumi.experiments.yaml` |
| `cluster` | `vars.DEV_STAGING_CLUSTER` | The `dcr-experiments` kOps cluster |
| `cluster_state_storage` | `vars.DEV_STAGING_KOPS_STATE_STORAGE` | `gs://kops-state-dcr-experiments/` |
| `dockerfile` | `build/package/Dockerfile` | Distroless build context in the service repository |
| `environment` | *(omitted — defaults to `development`)* | Selects `secrets.DEPLOY_SA_KEY` and the org variable `PULUMI_STATE_STORAGE` (`gs://pulumi-state-dcr-experiments`), **not** the `PROD_*` pair |

Each organization variable that this path reads (`DEV_STAGING_CLUSTER`, `DEV_STAGING_KOPS_STATE_STORAGE`, `PULUMI_STATE_STORAGE`) and the credential `DEPLOY_SA_KEY` already exist for the lab cluster, with visibility for all repositories. **No `just ci` recipe is necessary before the first `develop` build.** The workflow also omits `kops_version`, `kubectl_version`, and `pulumi_version`, and the composite defaults (`1.29.2`, `1.28.8`, `3.108.0`) are the versions that `config/clusters/dcr-experiments.yaml` pins.

### Release Tag CI

A release tag deploys only after a pull request adds the rendered `tag-build.yaml` to `dictyBase/dcr-mcp` ([tag deploy setup](#tag-deploy-setup)). That file sets `environment: production`, which routes the composite workflow to `secrets.PROD_DEPLOY_SA_KEY` and `vars.PROD_PULUMI_STATE_STORAGE` (`gs://pulumi-state-dcr-kube1`), and it reads the cluster from the `<CI_ENV>_*` repository variables that `just ci sync-deploy-vars` publishes. `PROD_DEPLOY_SA_KEY` is visible to selected repositories only, so `just ci set-deploy-secret --repos dictyBase/dcr-mcp` must add this repository to that list before the first production tag.

### Push Order

**Push `dcr-mcp/Pulumi.<stack>.yaml` to cluster-ops `develop` before you start a build in `dictyBase/dcr-mcp`.** The clone at step 3 sees only what `develop` holds. The same rule covers each later change to the stack file or to `dcr-mcp/*.go`: push it first, or the next run deploys the older tree. [Bootstrap order](#bootstrap-order-on-a-new-cluster) gives the full first-setup sequence.

The workflow files of the service repository are not in this repository. The inputs above come from `.github/workflows/ci.yml` in `dictyBase/dcr-mcp`, from the composite workflow in `dictyBase/workflows`, and from the deploy module that the composite workflow calls, all read at the time of writing.

---

## Tag Deploy Setup

This setup is for release tag deploys to a production cluster. It is not necessary for the `develop` path on `experiments`, which already has its organization variables ([developer CI](#developer-ci-on-develop)).

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
| `ERROR: no registry entry with stack '<stack>' (checked config/clusters/*.yaml)` from a scaffold recipe | The cluster has no registry entry, or the sub-shell belongs to another cluster | Run `just gcp-cluster register-cluster` one time, or `just gcp-cluster registry-show` to read the existing entry ([supported clusters](#supported-target-clusters)). |
| `Error: namespace '<ns>' not found` from `just dcr-mcp check` | `namespace-bootstrap` has not run on this cluster | Run `just gcp-pulumi apply-namespaces`. |
| `Error: service/graphql-api not found` from `just dcr-mcp check` | The upstream GraphQL server is not deployed in that namespace | Deploy it ([`graphql-deploy.md`](../../graphql-deploy.md)), then run `just dcr-mcp check` again. |
| `Error: yq not found on PATH — run prepare-tools` | The pinned tools are not installed for this cluster | Run `just prepare-tools` in the sub-shell. |
| `ERROR: stack '<stack>' does not exist in dcr-mcp, and there is no Pulumi.<stack>.yaml to initialize it from` | First deploy on this cluster. `ensure-stack` creates a stack only from a stack file that exists. | Run `just gcp-pulumi scaffold-dcr-mcp-stack` ([create the stack](#create-the-stack)). |
| `Stack config dcr-mcp/Pulumi.<stack>.yaml already exists — keeping existing config.` | The stack file already exists | Edit the file with `set-config`, or run `ensure-stack` to select the stack. |
| `dcr-mcp:properties is missing namespace, image.tag` | A required key is empty | Set it with `just gcp-pulumi set-config --folder dcr-mcp --key properties.<key> --value <val>`. |
| `dcr-mcp:properties is missing namespace` in a CI run only | `dcr-mcp/Pulumi.<stack>.yaml` is absent from cluster-ops `develop`, so CI deployed a file that holds `image.tag` alone | Push the stack file, then re-run the workflow ([bootstrap order](#bootstrap-order-on-a-new-cluster)). |
| `error: no stack named '<stack>' found` in a CI run | The stack was never initialized in the Pulumi backend of that cluster | Run `just gcp-pulumi scaffold-dcr-mcp-stack` in the cluster-env sub-shell, push the stack file, then re-run the workflow. |
| `ERROR: no semver tags found for dictyBase/dcr-mcp.` | The repository carries no release tag yet, so `just ci latest-tag` has nothing to print | Use the `develop-<sha>` tag that the developer CI run published ([find a published tag](#find-a-published-tag)). |
| `dcr-mcp:properties contains placeholder in image.tag, graphqlEndpoint` | The value still holds `<...>` text | Pin a real tag ([image tag](#image-tag)), and set or remove `graphqlEndpoint`. |
| Pod exits with `graphql-endpoint or GRAPHQL_API_SERVICE_HOST must be set` | The pod started before Service `graphql-api` existed, and no endpoint override is set | Deploy `graphql_server`, then run `kubectl -n <namespace> rollout restart deployment/dcr-mcp-api-server` ([endpoint resolution](#graphql-endpoint-resolution)). |
| `FAIL dcr-mcp: tag is 'bootstrap'` | The placeholder tag was never replaced | Pin a tag ([image tag](#image-tag)), then apply again. |
| `FAIL dcr-mcp: running image <a>, stack file says <b>` | The stack file changed and `create-resource` did not run, or a CI run set another tag | Run `preview` and `create-resource` again. |
| `FAIL dcr-mcp: service/dcr-mcp-api not found` | The Service was deleted, or `properties.name` changed | Run `create-resource` again ([deploy behavior](#deploy-behavior)). |
| `ErrImagePull` with a real tag | No CI run published that tag to Docker Hub | Read the published tag ([find a published tag](#find-a-published-tag)), then apply again. |
| `ingress.enabled=true but ingress is missing hosts, path, tlsSecret` | The Ingress was enabled before the four keys were set | Set `hosts.0`, `path`, `tlsSecret`, and `label.*` first ([legacy Ingress mode](#legacy-ingress-mode-dcr-experiments)). |
| `just gateway check` reports `services "dcr-mcp-api" not found` | `routes.mcp.enabled` was set before the `dcr-mcp` stack was applied | Run `just gcp-pulumi create-resource --folder dcr-mcp`, then `just gateway deploy`. |
| `just gateway check` reports an empty or placeholder host for route `mcp` | `routes.mcp.hosts.0` still holds `<mcp-host>` | Set the real hostname with `set-config` on the `gateway` folder. |
| Tool calls fail with `listStockSuggestions query failed` or `searchStocks query failed` | The upstream GraphQL server is unreachable from the pod | Check `graphql-api-server` with `just dcr-mcp check`, and check `properties.graphqlEndpoint` when it is set. |
| Clients receive HTTP `429` on `/mcp` | One client IP address passed `properties.rateLimit` in a minute | Raise `properties.rateLimit`, then apply again. |
| `just ci deploy-backend-services --services dcr-mcp` reports no match | `config/services.yaml` lists ArangoDB-backed services only | Use the recipes in [tag deploy setup](#tag-deploy-setup). |
| A CI run deploys, and the stack file in Git still says `bootstrap` | Expected. The CI clone sets the tag and is then thrown away. | Record the deployed tag with `set-config`, then push it ([image tag](#image-tag)). |
| The `call-build-deploy` job fails, and the image is on Docker Hub | The build step finished before the failed deploy step | Repair the missing step, then pin that tag and run `preview` and `create-resource` ([bootstrap order](#bootstrap-order-on-a-new-cluster)). |
