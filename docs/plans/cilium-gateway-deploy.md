# Plan: Cilium Gateway API and cert-manager for Public Service Routing

**Status**: Draft — pending approval.

## Table of Contents

- [Goal](#goal)
- [Non-goals](#non-goals)
- [Constraints](#constraints)
- [Approaches](#approaches)
- [Design Decisions](#design-decisions)
  - [1. Cluster Prerequisites (Cilium Gateway API & Gateway API CRDs)](#1-cluster-prerequisites-cilium-gateway-api--gateway-api-crds)
  - [2. Unified `gateway/` Pulumi Project](#2-unified-gateway-pulumi-project)
  - [3. Optional Ingress Toggle on `log-to/`](#3-optional-ingress-toggle-on-log-to)
  - [4. Scaffold Template and `just` Recipes](#4-scaffold-template-and-just-recipes)
  - [5. Documentation Map](#5-documentation-map)
- [What Is Left to the Builder](#what-is-left-to-the-builder)

---

## Goal

Expose public HTTP/HTTPS services (`frontend_application`, `graphql_server`, `minio`, and optionally `log-to`) on `dcr-kube1` and future clusters through **Kubernetes Gateway API** (`gateway.networking.k8s.io/v1`) backed by **Cilium Gateway API** (`gatewayClassName: cilium`) and **cert-manager** (`cert-manager.io/v1` ACME `Issuer` with `http01.gatewayHTTPRoute`).

All hostnames, TLS secret names, and the ACME email are operator configuration inputs in `gateway/Pulumi.<stack>.yaml`, scaffolded from a template and configured via `just` recipes. Nothing is deployed to the live cluster during this task.

## Non-goals

- Do **not** modify `ingress/`, `ingress-controller/`, `redirect-ingress/`, `kcert/`, or `cert-issuer/` — the legacy `nginx` + `kcert` Ingress setup on `dcr-experiments` remains untouched.
- Do **not** include domain redirect (`dictycr.org` → `dictybase.dev`) in the Gateway project at this point.
- Do **not** deploy or mutate the live `dcr-kube1` or `dcr-experiments` clusters from this session — write the code, templates, recipes, contract tests, and documentation only.

## Constraints

- `namespace-bootstrap` is the sole owner of the `prod` (`appNamespace`) and `operators` namespaces. `gateway/` must never create a `Namespace` resource.
- Kubernetes `secretKeyRef` and TLS `Secret` name pointers (`tlsSecret`, `issuer.secretName`) must stay plain text in `Pulumi.<stack>.yaml` so preflight and verify recipes can inspect them with `yq`.
- Every `just` recipe must pass `just lint-recipes` (`quote()` around bare interpolations, `mktemp` with `XXXXXX`, no hardcoded `--stack prod`, no ambient `$GOOGLE_CLOUD_PROJECT`).
- All documentation under `docs/` must follow `docs/STYLE.md` and ASD-STE100, and pass `just docs docs-lint`.

## Approaches

### Approach A: Unified `gateway/` Pulumi Project (Selected)

One Pulumi project `gateway/` manages the ACME `Issuer` (`cert-manager.io/v1`), the shared Cilium `Gateway` (`gateway.networking.k8s.io/v1`), and the `HTTPRoute` resources for each enabled service (`frontend`, `graphql`, `minio`, `logto`) in the application namespace.

- **Pros:** Single source of truth for the `Gateway` name, listener names, TLS secret names, `Issuer` `parentRefs`, and `HTTPRoute` `parentRefs`. Matches the existing design contract in `docs/reference/graphql/deployment.md` where `graphql_server` sets `ingress.enabled: false` and delegates external routing to a central Gateway stack.
- **Cons:** Enabling or changing a single service route runs `pulumi up` on `gateway/`.

### Approach B: Split `cert-issuer/` + `gateway/` + Per-Service `HTTPRoute`

Extend `cert-issuer/` for Gateway HTTP-01, keep `gateway/` for the `Gateway` resource only, and embed `HTTPRoute` resources inside `graphql_server/`, `minio/`, and `log-to/`.

- **Rejected because:** `Gateway` HTTPS listeners and TLS `certificateRefs` live on the `Gateway` resource itself, so changing a service hostname would require coordinated edits across two Pulumi projects.

## Design Decisions

### 1. Cluster Prerequisites (Cilium Gateway API & Gateway API CRDs)

1. **kOps cluster spec (`config/kops/dcr-kube1/cluster.yaml` and `config/kops/_starter/cluster.yaml.tmpl`):**
   Enable Cilium Gateway API in the Cilium networking block:
   ```yaml
   networking:
     cilium:
       enableNodePort: true
       etcdManaged: true
       gatewayAPI:
         enabled: true
   ```
2. **Gateway API standard CRDs & cert-manager Gateway controller gate:**
   - Cilium requires the standard Gateway API CRDs (`gatewayclasses`, `gateways`, `httproutes`, `referencegrants`) in the cluster before the Cilium operator reconciles `GatewayClass/cilium`.
   - `cert-manager` requires `--enable-gateway-api` on its controller args so its HTTP-01 solver can create temporary `HTTPRoute` challenge resources attached to the `Gateway`.
   - Provide a `just gateway install-crds` (or `prepare-cluster`) recipe and preflight check in `just_modules/gateway.justfile` that applies the pinned Gateway API standard CRDs and verifies `GatewayClass/cilium` and `cert-manager` Gateway API readiness.

### 2. Unified `gateway/` Pulumi Project

Create `gateway/` (`Pulumi.yaml`, `main.go`, `config.go`, `issuer.go`, `gateway.go`, `routes.go`, `config_test.go`):

- **Resources created in `properties.namespace` (`prod`):**
  1. `cert-manager.io/v1:Issuer` (using `crds/kubernetes/certmanager/v1`):
     - Name: `properties.issuer.name` (default `letsencrypt-issuer`).
     - ACME server: `properties.issuer.server` (default `https://acme-v02.api.letsencrypt.org/directory`).
     - Email: `properties.issuer.email` (operator input, required).
     - Private key Secret: `properties.issuer.secretName` (default `letsencrypt-account-key`).
     - Solver: `http01.gatewayHTTPRoute` with `parentRefs` pointing to `Gateway` `properties.name` in `properties.namespace`.
  2. `gateway.networking.k8s.io/v1:Gateway` (via `apiextensions.CustomResource` or typed resource):
     - Name: `properties.name` (default `dictycr-gateway`).
     - Annotation: `cert-manager.io/issuer: <issuer.name>`.
     - `gatewayClassName`: `properties.gatewayClassName` (default `cilium`).
     - Listeners:
       - `http`: port `80`, protocol `HTTP`, `allowedRoutes.namespaces.from: Same` (allows cert-manager HTTP-01 solver `HTTPRoute` resources in the same namespace).
       - One `HTTPS` listener (port `443`, protocol `HTTPS`, `tls.mode: Terminate`, `certificateRefs: [{kind: Secret, name: <tlsSecret>}]`) per enabled service route (`https-frontend`, `https-graphql`, `https-minio`, `https-logto`).
  3. `gateway.networking.k8s.io/v1:HTTPRoute` for each enabled route:
     - **`frontend`** (`enabled: bool`): attaches to `properties.name`, matches `routes.frontend.hosts`, routes path prefixes (`/`, `/stockcenter`, `/publication`, `/gene`) to Services (`frontpage-api`, `stockcenter-api`, `publication-api`, `genomepage-api` on port `3000`).
     - **`graphql`** (`enabled: bool`): attaches to `properties.name`, matches `routes.graphql.hosts`, routes path `/` (`PathPrefix`) to `graphql-api:8080`.
     - **`minio`** (`enabled: bool`): attaches to `properties.name`, matches `routes.minio.hosts`, routes path `/` (`PathPrefix`) to `minio:9000`.
     - **`logto`** (`enabled: bool`): attaches to `properties.name`, matches `routes.logto.hosts`, routes path `/` (`PathPrefix`) to `logto-api:3001` (`logto-admin:3002` stays internal).
- **Fail-closed validation (`config.go`):**
  - Rejects empty `namespace`, `issuer.email`, or unreplaced `<...>` angle-bracket placeholders.
  - Requires at least one route to have `enabled: true`.
  - For each route where `enabled: true`, validates that `hosts`, `tlsSecret`, and backend service/port fields are non-empty and free of `<...>` placeholders. Disabled routes (`enabled: false`) ignore empty or placeholder host/TLS fields so operators can enable services incrementally.

### 3. Optional Ingress Toggle on `log-to/`

- Add `Enabled bool` to `IngressConfig` in `log-to/config.go`.
- In `log-to/ingress.go`, guard `CreateIngress`: if `!lt.Config.Ingress.Enabled`, return `nil, nil` without creating `logto-ingress` (matching `graphql_server/ingress.go` and `minio/main.go`).
- Set `ingress.enabled: true` in `log-to/Pulumi.experiments.yaml` and `log-to/Pulumi.dev.yaml` so existing lab stacks behave identically.
- Update `just_modules/logto.justfile` (`check` and `verify`) and `scripts/test-logto-recipes.sh` so `ingress.backendHosts` / `ingress.tlsSecret` and `kubectl get ingress logto-ingress` are only required when `ingress.enabled` is `true` (default `false` on Gateway clusters).

### 4. Scaffold Template and `just` Recipes

1. **Template `config/templates/gateway-stack.yaml.tmpl`:**
   Renders `gateway/Pulumi.<stack>.yaml` with `gatewayClassName: cilium`, `issuer` block (`<acme-email>` placeholder), and `routes` for `frontend`, `graphql`, `minio`, and `logto` (with `enabled: false` by default on `logto` and operator placeholders `<frontend-host>`, `<graphql-host>`, `<minio-host>`, `<logto-host>`).
2. **Scaffold recipe `just gcp-pulumi scaffold-gateway-stack`:**
   Follows `scaffold-graphql-stack` / `scaffold-event-messenger-stack`: looks up `namespace` and `kms_secrets_provider` in `config/clusters/<cluster>.yaml`, refuses to overwrite an existing `gateway/Pulumi.<stack>.yaml`, initializes the stack with KMS, and appends the rendered template.
3. **Gateway module `just_modules/gateway.justfile` (`mod gateway` in `Justfile`):**
   - `just gateway install-crds`: applies the Kubernetes Gateway API standard CRDs and enables `--enable-gateway-api` on `cert-manager` if needed.
   - `just gateway check`: read-only preflight (validates `PULUMI_*` env, `gateway/Pulumi.<stack>.yaml` syntax and enabled route fields, `namespace-bootstrap` `appNamespace` match, `GatewayClass/cilium` Accepted status, `cert-manager` pods, and backend Services for enabled routes).
   - `just gateway deploy`: composite flow (`check` → `ensure-stack` → `preview` → `create-resource` → `verify`).
   - `just gateway verify`: checks `Issuer` Ready condition, `Gateway` Programmed/Accepted status and LoadBalancer IP, TLS `Certificate`/`Secret` readiness for enabled routes, and `HTTPRoute` Accepted/ResolvedRefs conditions.

### 5. Documentation Map

Following `docs/STYLE.md` and ASD-STE100:

- **New guide:** `docs/gateway-deploy.md` (TOC, Quick Reference, numbered sections for CRDs/Cluster Prereqs, Scaffold Stack, Configure Issuer, Configure Frontend Route, Configure GraphQL Route, Configure MinIO Route, Configure Logto Route (Optional), Deploy, Verify, Troubleshooting, Related Documents).
- **New reference doc:** `docs/reference/gateway/deployment.md` (resources created, Cilium + cert-manager architecture, recipes & flags, per-route configuration tables, TLS issuance flow, verification checks, troubleshooting table).
- **Updates to existing docs:**
  - `docs/graphql-deploy.md` and `docs/reference/graphql/deployment.md` (replace the note "The central Gateway stack is not in this repository" with a link to `docs/gateway-deploy.md`).
  - `docs/logto-deploy.md` and `docs/reference/logto/deployment.md` (document `ingress.enabled: false` default for Gateway clusters and link to `docs/gateway-deploy.md`).
  - `docs/minio-deploy.md` and `docs/reference/minio/install.md` (link to `docs/gateway-deploy.md` for public S3 exposure).
  - `README.md` (add `docs/gateway-deploy.md` to the deployment guides table).

## What Is Left to the Builder

- Exact Go struct tags and unit tests in `gateway/config_test.go` and `gateway/gateway_test.go`.
- Contract test script `scripts/test-gateway-recipes.sh` wired into `just check` and `scripts/test-backend-scaffold.sh` test case for `scaffold-gateway-stack`.
