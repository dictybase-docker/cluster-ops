# Cilium Gateway API and cert-manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `gateway/` Pulumi project, `log-to` optional Ingress toggle, `just` recipes, contract tests, and operator documentation to expose `frontend_application`, `graphql_server`, `minio`, and `log-to` through Cilium Gateway API and `cert-manager`.

**Architecture:** One unified `gateway/` Pulumi project creates the `cert-manager.io/v1` ACME `Issuer` (with `http01.gatewayHTTPRoute`), the `gateway.networking.k8s.io/v1` `Gateway` (`gatewayClassName: cilium`), and the `HTTPRoute` resources for each enabled service in the application namespace. A new `just_modules/gateway.justfile` module and `just gcp-pulumi scaffold-gateway-stack` recipe manage CRD installation, stack scaffolding, preflight checks, deployment, and verification.

**Tech Stack:** Go 1.26, `github.com/IBM/fp-go/v2`, Pulumi Go SDK v3, `pulumi-kubernetes` SDK v4, `crds/kubernetes/certmanager/v1`, `just`, `yq`, `jq`, `kubectl`.

**Spec:** `docs/plans/cilium-gateway-deploy.md`

## Global Constraints

- `namespace-bootstrap` is the sole owner of namespaces (`prod` and `operators`); `gateway/` must never create a `Namespace`.
- Keep all TLS Secret names (`tlsSecret`, `issuer.secretName`) and service names plain text in `Pulumi.<stack>.yaml`.
- Do not modify `ingress/`, `ingress-controller/`, `redirect-ingress/`, `kcert/`, or `cert-issuer/` (`dcr-experiments` stays untouched).
- Every `just` recipe must pass `just lint-recipes` and every doc must pass `just docs docs-lint` (ASD-STE100 + `docs/STYLE.md`).
- All Go code in this repository must pass `golangci-lint fmt`, `golangci-lint run ./...`, `go test ./...`, and `gopls check -severity=hint`.

## Review Focus

- Unreplaced `<...>` template placeholders in `issuer.email`, `hosts`, or `tlsSecret` must fail config validation closed before Pulumi touches the cluster, but only for routes where `enabled: true` (and always for `issuer.email`).
- A stack with all four routes (`frontend`, `graphql`, `minio`, `logto`) set to `enabled: false` must fail validation with `at least one route must be enabled`.
- `log-to` with `ingress.enabled: false` (or absent `ingress:` block) must skip creating `logto-ingress` in Pulumi and skip the Ingress checks in `just logto check` and `just logto verify`, while `log-to/Pulumi.experiments.yaml` and `log-to/Pulumi.dev.yaml` keep `ingress.enabled: true`.
- Each enabled route in `gateway/` must generate both a dedicated HTTPS listener on the `Gateway` (`https-frontend`, `https-graphql`, `https-minio`, `https-logto`) with `tls.certificateRefs` and a matching `HTTPRoute` whose `parentRefs` point to the `Gateway`.
- `just gateway check` must fail closed when `GatewayClass/cilium` is missing or not `Accepted`, when `cert-manager` lacks `--enable-gateway-api`, or when an enabled route's backend `Service` is absent in the namespace.

---

### Task 1: Enable Cilium Gateway API in kOps Manifests and Make `log-to` Ingress Optional

**Files:**
- Modify: `config/kops/dcr-kube1/cluster.yaml`
- Modify: `config/kops/_starter/cluster.yaml.tmpl`
- Modify: `log-to/config.go`
- Modify: `log-to/ingress.go`
- Modify: `log-to/Pulumi.experiments.yaml`
- Modify: `log-to/Pulumi.dev.yaml`
- Create: `log-to/config_test.go`
- Modify: `just_modules/logto.justfile`
- Modify: `scripts/test-logto-recipes.sh`

**Interfaces:**
- Produces: `LogtoConfig.Ingress.Enabled bool` (`false` by default; `CreateIngress` returns `nil, nil` when `false`).
- Produces: `just logto check` and `just logto verify` support `ingress.enabled: false` (default) and `ingress.enabled: true`.

- [ ] **Step 1: Write the failing test in `log-to/config_test.go` and `scripts/test-logto-recipes.sh`**

Create `log-to/config_test.go` testing `CreateIngress` when `Ingress.Enabled` is `false` (creates zero `kubernetes:networking.k8s.io/v1:Ingress` resources) and when `Ingress.Enabled` is `true` (creates `logto-ingress`). Add a test case in `scripts/test-logto-recipes.sh` verifying `just logto check` and `just logto verify` pass when `ingress.enabled` is `false` with no `backendHosts`/`tlsSecret`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./log-to/... && ./scripts/test-logto-recipes.sh`
Expected: FAIL (`Ingress.Enabled` undefined / `logto check` fails on empty `ingress.backendHosts`).

- [ ] **Step 3: Implement `Ingress.Enabled` in `log-to/`, update `just_modules/logto.justfile`, and enable `gatewayAPI` in kOps manifests**

1. In `config/kops/dcr-kube1/cluster.yaml` and `config/kops/_starter/cluster.yaml.tmpl`, add `gatewayAPI: { enabled: true }` under `spec.networking.cilium`.
2. In `log-to/config.go`, add `Enabled bool` to `IngressConfig`.
3. In `log-to/ingress.go`, return `nil, nil` at the start of `CreateIngress` when `!lt.Config.Ingress.Enabled`.
4. In `log-to/Pulumi.experiments.yaml` and `log-to/Pulumi.dev.yaml`, add `enabled: true` under `ingress:`.
5. In `just_modules/logto.justfile`, read `ingress_enabled=$(yq -r '.config."log-to:properties".ingress.enabled // "false"' "$CFG_FILE")` and only validate/verify `ingress.backendHosts[0]`, `ingress.tlsSecret`, and `kubectl get ingress logto-ingress` when `[[ "$ingress_enabled" == "true" ]]`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./log-to/... && ./scripts/test-logto-recipes.sh && ./scripts/test-bootstrap-bundle.sh`
Expected: PASS

---

### Task 2: Create the `gateway/` Pulumi Project with fp-go Validation and Resource Builders

**Files:**
- Create: `gateway/Pulumi.yaml`
- Create: `gateway/config.go`
- Create: `gateway/issuer.go`
- Create: `gateway/gateway.go`
- Create: `gateway/routes.go`
- Create: `gateway/main.go`
- Create: `gateway/main_test.go`

**Interfaces:**
- Consumes: `crds/kubernetes/certmanager/v1` (`NewIssuer`, `IssuerArgs`), `github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions` (`NewCustomResource`), `github.com/IBM/fp-go/v2` (`A`, `E`, `F`, `O`, `P`).
- Produces:
  - `GatewayConfig` struct reading `gateway:properties`:
    - `Name string` (default `dictycr-gateway`)
    - `Namespace string`
    - `GatewayClassName string` (default `cilium`)
    - `Issuer IssuerConfig` (`Name`, `Email`, `Server`, `SecretName`)
    - `Routes RoutesConfig` (`Frontend FrontendRouteConfig`, `Graphql ServiceRouteConfig`, `Minio ServiceRouteConfig`, `Logto ServiceRouteConfig`)
  - `(c *GatewayConfig) Validate() error` (pure validation via `E.Either[error, *GatewayConfig]` using fp-go predicates and `O`/`E` combinators).
  - `(g *GatewayStack) Install(ctx *pulumi.Context) error` creating:
    - `cert-manager.io/v1:Issuer` named `cfg.Issuer.Name` with `http01.gatewayHTTPRoute.parentRefs` referencing `cfg.Name` (`kind: Gateway`, `namespace: cfg.Namespace`).
    - `gateway.networking.k8s.io/v1:Gateway` named `cfg.Name` with annotation `cert-manager.io/issuer: cfg.Issuer.Name`, `http` listener on port `80`, and one `https-<route>` listener on port `443` per enabled route (one listener per host if multiple hosts are configured, or hostname set per listener).
    - `gateway.networking.k8s.io/v1:HTTPRoute` per enabled route (`frontend-route`, `graphql-route`, `minio-route`, `logto-route`) depending on the `Gateway`.

- [ ] **Step 1: Write the failing unit and Pulumi mock tests in `gateway/main_test.go`**

Test cases in `gateway/main_test.go`:
1. `TestConfigValidateDefaultsAndSuccess`: applies defaults (`name: dictycr-gateway`, `gatewayClassName: cilium`, `issuer.name: letsencrypt-issuer`, `issuer.server: https://acme-v02.api.letsencrypt.org/directory`, `issuer.secretName: letsencrypt-account-key`, default service ports/paths) and validates when `frontend`, `graphql`, and `minio` are enabled and `logto` is disabled (with `<logto-host>` placeholder ignored while disabled).
2. `TestConfigValidateFailures`:
   - missing `namespace` or `issuer.email`
   - `<acme-email>` placeholder in `issuer.email`
   - all routes `enabled: false` (`at least one route must be enabled`)
   - enabled route with empty `hosts`, empty `tlsSecret`, or `<graphql-host>` placeholder
   - enabled `frontend` route with empty `services`
3. `TestInstallCreatesIssuerGatewayAndEnabledHTTPRoutes`: runs `NewGatewayStack(cfg).Install(ctx)` under `pulumi.WithMocks`, asserting:
   - `cert-manager.io/v1:Issuer` `letsencrypt-issuer` is created with `gatewayHTTPRoute` solver pointing to `dictycr-gateway` in `prod`.
   - `gateway.networking.k8s.io/v1:Gateway` `dictycr-gateway` is created with `gatewayClassName: cilium`, `cert-manager.io/issuer: letsencrypt-issuer`, `http` listener on `:80`, and `HTTPS` listeners on `:443` for `frontend`, `graphql`, and `minio` (and none for disabled `logto`).
   - `gateway.networking.k8s.io/v1:HTTPRoute` resources `frontend-route` (4 path rules: `/`, `/stockcenter`, `/publication`, `/gene`), `graphql-route` (`graphql-api:8080`), and `minio-route` (`minio:9000`) are created, and `logto-route` is not created when `logto.enabled` is `false`.
4. `TestInstallWithLogtoEnabled`: verifies `logto-route` (`logto-api:3001`) and `https-logto` listener are created when `logto.enabled` is `true`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./gateway/...`
Expected: FAIL (files do not exist yet).

- [ ] **Step 3: Implement `gateway/` (`Pulumi.yaml`, `config.go`, `issuer.go`, `gateway.go`, `routes.go`, `main.go`)**

Follow the fp-go rules in `AGENTS.md`: no nested inside-out calls where `F.Pipe` applies, point-free `Option`/`Either`/`Predicate` helpers in `config.go` (`O.FromPredicate`, `O.GetOrElse`, `A.Filter`, `A.Map`), and clean `golangci-lint` + `gopls` checks.

- [ ] **Step 4: Run tests and linters to verify they pass**

Run: `golangci-lint fmt && golangci-lint run ./gateway/... && go test -v ./gateway/...`
Expected: PASS with 0 lint issues.

---

### Task 3: Add `scaffold-gateway-stack`, `just_modules/gateway.justfile`, and Contract Tests

**Files:**
- Create: `config/templates/gateway-stack.yaml.tmpl`
- Modify: `just_modules/pulumi.justfile`
- Create: `just_modules/gateway.justfile`
- Modify: `Justfile`
- Modify: `scripts/test-backend-scaffold.sh`
- Create: `scripts/test-gateway-recipes.sh`

**Interfaces:**
- Produces:
  - `just gcp-pulumi scaffold-gateway-stack [--stack <name>]`
  - `just gateway install-crds [--version <v>]` (default `v1.2.1`)
  - `just gateway check [--stack <name>] [--namespace <ns>]`
  - `just gateway deploy [--stack <name>] [--namespace <ns>]`
  - `just gateway verify [--stack <name>] [--namespace <ns>]`
  - `just test-gateway` wired into `just check` in `Justfile`

- [ ] **Step 1: Write the failing contract tests in `scripts/test-backend-scaffold.sh` and `scripts/test-gateway-recipes.sh`**

1. Append section 8 to `scripts/test-backend-scaffold.sh` testing `just gcp-pulumi scaffold-gateway-stack --stack dcr-kube1`: asserts `gateway/Pulumi.dcr-kube1.yaml` is created, `gatewayClassName` is `cilium`, `namespace` is `prod`, `routes.frontend.enabled` is `true`, `routes.graphql.enabled` is `true`, `routes.minio.enabled` is `true`, and `routes.logto.enabled` is `false`.
2. Create `scripts/test-gateway-recipes.sh` (modeled on `scripts/test-logto-recipes.sh`) testing:
   - `just gateway install-crds` applies the Gateway API standard CRDs and ensures `cert-manager` has `--enable-gateway-api`.
   - `just gateway check` succeeds when config, `namespace-bootstrap`, `GatewayClass/cilium`, `cert-manager`, and enabled backend Services (`frontpage-api`, `stockcenter-api`, `publication-api`, `genomepage-api`, `graphql-api`, `minio`) are ready; fails closed on missing `PULUMI_BACKEND_URL`, missing stack config, unreplaced `<...>` placeholder in `issuer.email` or an enabled route, all routes disabled, or missing `GatewayClass/cilium`.
   - `just gateway deploy` runs `check` → `ensure-stack` → `preview` → `create-resource` → `verify` in exact order.
   - `just gateway verify` checks `Issuer`, `Gateway` Programmed condition and LoadBalancer IP, TLS `Secret` for each enabled route, and `HTTPRoute` for each enabled route.

- [ ] **Step 2: Run the contract tests to verify they fail**

Run: `./scripts/test-backend-scaffold.sh && ./scripts/test-gateway-recipes.sh`
Expected: FAIL (`scaffold-gateway-stack` and `gateway` module not defined).

- [ ] **Step 3: Implement `config/templates/gateway-stack.yaml.tmpl`, `scaffold-gateway-stack`, `just_modules/gateway.justfile`, and wire into `Justfile`**

Ensure every interpolation in `just_modules/gateway.justfile` and `just_modules/pulumi.justfile` uses `quote()` when unquoted, `mktemp` uses `XXXXXX`, and recipe `Next:` echoes point to recipe commands (never section numbers).

- [ ] **Step 4: Run `just lint-recipes`, `scripts/test-backend-scaffold.sh`, and `scripts/test-gateway-recipes.sh`**

Run: `just lint-recipes && ./scripts/test-backend-scaffold.sh && ./scripts/test-gateway-recipes.sh`
Expected: PASS

---

### Task 4: Write Operator Guide and Reference Documentation (`docs/gateway-deploy.md` & `docs/reference/gateway/deployment.md`) and Update Cross-References

**Files:**
- Create: `docs/gateway-deploy.md`
- Create: `docs/reference/gateway/deployment.md`
- Modify: `docs/graphql-deploy.md`
- Modify: `docs/reference/graphql/deployment.md`
- Modify: `docs/logto-deploy.md`
- Modify: `docs/reference/logto/deployment.md`
- Modify: `docs/minio-deploy.md`
- Modify: `docs/reference/minio/install.md`
- Modify: `README.md`

**Interfaces:**
- Follows `docs/STYLE.md` and ASD-STE100 strictly:
  - Every numbered section in `docs/gateway-deploy.md` has 1-2 summary sentences, a `→ [details](reference/gateway/deployment.md#...)` link, and a single fenced bash block with nothing after the bash block.
  - Optional route modes (`Configure Frontend Route`, `Configure GraphQL Route`, `Configure MinIO Route`, `Configure Logto Route (Optional)`) are top-level numbered sections stating their default in the first line.
  - No internal Go identifiers (`TryObject`, struct names, file/line citations) in `docs/`.

- [ ] **Step 1: Write `docs/reference/gateway/deployment.md` and `docs/gateway-deploy.md`**

Cover in `docs/reference/gateway/deployment.md`:
- What the Project Creates (`Issuer`, `Gateway`, `HTTPRoute` resources)
- Cluster Prerequisites (Cilium `gatewayAPI.enabled: true`, Gateway API standard CRDs, `cert-manager` `--enable-gateway-api`, upstream Services)
- Recipes (`just gateway install-crds`, `just gcp-pulumi scaffold-gateway-stack`, `just gateway check`, `just gateway deploy`, `just gateway verify`) with flag tables
- Create the Stack and Baseline Configuration
- ACME Issuer Configuration
- Route Configuration (`frontend`, `graphql`, `minio`, `logto`)
- TLS Certificate Issuance Flow
- Verification
- Troubleshooting

Cover in `docs/gateway-deploy.md`:
- Status line, Table of Contents, Quick Reference
- 1. Enable Cluster Prerequisites and Install Gateway CRDs
- 2. Scaffold or Select the Stack
- 3. Configure the ACME Issuer
- 4. Configure the Frontend Route
- 5. Configure the GraphQL Route
- 6. Configure the MinIO Route
- 7. Configure the Logto Route (Optional)
- 8. Deploy the Gateway
- 9. Push the Stack File
- 10. Verify the Gateway
- 11. Troubleshooting
- 12. Related Documents

- [ ] **Step 2: Update cross-links in `docs/graphql-deploy.md`, `docs/reference/graphql/deployment.md`, `docs/logto-deploy.md`, `docs/reference/logto/deployment.md`, `docs/minio-deploy.md`, `docs/reference/minio/install.md`, and `README.md`**

- [ ] **Step 3: Run `just check` and all Go / gopls / lint gates**

Run: `golangci-lint fmt && golangci-lint run ./... && go test ./... && just check`
Expected: PASS (`All docs pass mechanical checks`, zero lint or test failures).
