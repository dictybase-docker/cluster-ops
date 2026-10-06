# Plan: Migrate Check/Probe/Verify/Validate Recipes to Go

**Status**: Proposed

## Table of Contents
- [Goal](#goal)
- [Non-goals](#non-goals)
- [Current state](#current-state)
- [Inventory](#inventory)
  - [Backend/Pulumi Checks](#backendpulumi-checks)
  - [Deployer Access Checks](#deployer-access-checks)
  - [Datastore Pool/Verify Checks](#datastore-poolverify-checks)
  - [Cluster Validation Checks](#cluster-validation-checks)
  - [Recipes Staying in Shell](#recipes-staying-in-shell)
- [Brittleness catalog](#brittleness-catalog)
- [Design](#design)
  - [Package layout](#package-layout)
  - [Shared foundation](#shared-foundation)
  - [Output contract](#output-contract)
  - [Integration points](#integration-points)
  - [Secrets handling](#secrets-handling)
- [Phasing](#phasing)
  - [Phase 1: Foundation + Backend Deploy Gate](#phase-1-foundation--backend-deploy-gate)
  - [Phase 2: Datastore Checks](#phase-2-datastore-checks)
  - [Phase 3: Cluster Validation](#phase-3-cluster-validation)
- [Testing strategy](#testing-strategy)
- [Acceptance](#acceptance)
- [Risks](#risks)
- [Related plans](#related-plans)

## Goal

Replace brittle shell check/probe/verify/validate recipes (~25 total) with robust Go implementations using official SDKs (client-go, GCP SDKs, terratest tunnels). Fixes error conflation (kubectl exit codes lump not-found/RBAC/unreachable), credential leaks (passwords in argv), false passes (nc -z on unready port-forward), and hardcoded values (us-central1 region, fixed ports).

Three outcomes:
1. **Error classification** — not-found ≠ RBAC ≠ unreachable (via client-go `apierrors` + net error wrapping)
2. **Never-short-circuit contract** — list every failure (current shell exits on first miss)
3. **Testable units** — fake clientsets + mocked GCP calls replace PATH mocks

## Non-goals

- Do **not** migrate read-only operational helpers that are already simple kubectl wrappers (`cluster-status`, which runs `kubectl version && kubectl cluster-info && kubectl get nodes`)
- Do **not** migrate recipes already using Go binaries (`drift-manifests` calls `bin/cluster-ops kops normalize`)
- Do **not** migrate pure data validation with no external calls (`registry-show` — yq-only checks on YAML structure)
- Do **not** change the visual output format — keep `scripts/lib/check-helpers.sh` PASS/FAIL/WARN/INFO lines (`CHECK_COLOR` env var)
- Do **not** add interactive features — checks remain fully automated, zero-prompt

## Current state

| Layer | Recipe Count | Implementation | Problem |
|-------|--------------|----------------|---------|
| Backend/Pulumi | 4 | Shell + scripts | nc -z false-pass, password in curl argv, error conflation |
| Deployer access | 2 | Shell | us-central1 hardcode, first-role-miss exit, error conflation |
| Datastore | 10 | Shell | Fixed port-forward, no classification, complex JSON wrangling |
| Cluster validation | 5 | Shell + Go (custodian) | Mixed shell/Go, no reuse between HA checks |
| Operational | 4 | Shell (staying) | Simple wrappers, no brittleness |

Total: **25 recipes**, ~21 migrating to Go across 3 phases.

Existing shell contract tests (`scripts/test-*.sh`) mock kubectl/gcloud on PATH and assert **order** (preflight before mutation) via call recording. Cannot intercept client-go — behavior moves to Go unit tests; order assertions stay in shell by mocking `go run`.

## Inventory

### Backend/Pulumi Checks

| Recipe | File:Line | Probes | Brittleness | Go Target |
|--------|-----------|--------|-------------|-----------|
| `check-backend-prereqs` | pulumi.justfile:641 | Namespace, Secret keys, ArangoDB reachability + database | Fixed port 18529 (line 708), nc -z on maybe-unready forward (716), password in curl argv (725), kubectl exit code conflation (686, 697) | `internal/preflight/backend.CheckBackendPrereqs` → `cluster-ops preflight backend-prereqs` |
| `check-backend` | pulumi.justfile:286 → scripts/pulumi/check-backend.sh | Env vars, SA key file, GCS bucket + versioning, KMS key, pulumi login | Error conflation on gcloud (sh:53,62,82) | `internal/preflight/backend.CheckBackend` → `cluster-ops preflight pulumi-backend` |
| `check-tools` | pulumi.justfile:276 → scripts/pulumi/check-tools.sh | Binaries on PATH + versions | Version parsing fragility (awk on unstable output) | `internal/preflight/toolchain.CheckTools` → `cluster-ops preflight tools` |
| `check-storageclass` | pulumi.justfile:417 | StorageClass existence + provisioner match | kubectl error conflation (432) | `internal/preflight/backend.CheckStorageClass` → `cluster-ops preflight storageclass` |

Contract test: `scripts/test-backend-prereqs.sh` — mocks kubectl, verifies check runs before deploy. Behavior moves to Go (fake clientset); order assertion stays (mock `go` on PATH).

### Deployer Access Checks

| Recipe | File:Line | Probes | Brittleness | Go Target |
|--------|-----------|--------|-------------|-----------|
| `verify-deployer-access` | cluster.justfile:2076 | Key matches registry project, kops export succeeds, kubectl auth can-i, KMS encrypt/decrypt round-trip | Hardcoded us-central1 (2132, 2136, 2143), error conflation on kops/kubectl (2121, 2125) | `internal/preflight/cluster.VerifyDeployerAccess` → `cluster-ops preflight deployer-access` |
| `check-deploy-credentials` | ci.justfile:17 | Calls verify-deployer-access, then checks deployer-roles.txt grants via gcloud IAM policy grep | First-miss exit (line 63 grep -qx), misses group/inherited grants, doesn't use SA creds for testIamPermissions | `internal/preflight/cluster.CheckDeployCredentials` → `cluster-ops preflight deploy-credentials` |

Recommendation: Replace gcloud IAM policy grep with `projects.testIamPermissions` called WITH the SA key credentials, against permissions expanded from `deployer-roles.txt` via `iam.roles.get` (`includedPermissions[]`) — catches group grants AND lists every missing permission (current shell exits on first).

Contract test: `scripts/test-ci-recipes.sh` — verifies check-deploy-credentials runs before set-deploy-secret. Order stays; behavior moves to Go.

### Datastore Pool/Verify Checks

| Recipe | File:Line | Probes | Brittleness | Go Target |
|--------|-----------|--------|-------------|-----------|
| `arangodb check-pool` | arangodb.justfile:3059 | Node count, arch=amd64, taints, zone spread, Ready status | kubectl JSON piped to jq (complex, fragile selectors 3092-3094) | `internal/preflight/datastore.CheckPool` (generic for all datastores) |
| `arangodb verify` | arangodb.justfile:3132 | Pool nodes, operator, StorageClass, deployment, pods ready, PVCs, Service port 8529, setup Job, backup CronJob | jq node/PVC/pod selectors (3184-3199), kubectl error conflation | `internal/preflight/datastore.VerifyArangoDB` |
| `arangodb verify-app-credentials` | arangodb.justfile:1286 | Secret keys exist, arangosh connects to each database with app creds | Runs ephemeral pod via kubectl run (1327) — works but opaque when it fails | `internal/preflight/datastore.VerifyArangoAppCredentials` (pod still via client-go Pod API) |
| `postgres check-pool` | postgres.justfile:97 | Same as arangodb check-pool | Same jq brittleness | Reuses `CheckPool` |
| `postgres verify` | postgres.justfile:1203 | Operator, cluster CR, instances ready, backup config, Service, PVCs | jq selectors, kubectl error conflation | `internal/preflight/datastore.VerifyPostgres` |
| `redis check-pool` | redis.justfile:33 | Same as arangodb check-pool | Same jq brittleness | Reuses `CheckPool` |
| `redis verify` | redis.justfile:134 | StatefulSet, pods ready, Service, PVCs | jq selectors, kubectl error conflation | `internal/preflight/datastore.VerifyRedis` |
| `minio check-pool` | minio.justfile:33 | Same as arangodb check-pool | Same jq brittleness | Reuses `CheckPool` |
| `minio verify` | minio.justfile:233 | Deployment, pods ready, Service, PVCs, root Secret | jq selectors, kubectl error conflation | `internal/preflight/datastore.VerifyMinio` |
| `nats verify` | nats.justfile:100 | StatefulSet, pods ready, Service, config Secret | jq selectors, kubectl error conflation | `internal/preflight/datastore.VerifyNats` |

Common pattern: all check-pool recipes are identical (node count, arch, taints, zones, Ready). Migrate once as `CheckPool(clients, pool, wantCount, requireAmd64 bool)`.

Contract test: `scripts/test-arangodb-finalization.sh` covers verify. Behavior → Go unit tests with fake clientset; shell test keeps order assertions.

### Cluster Validation Checks

| Recipe | File:Line | Probes | Brittleness | Go Target |
|--------|-----------|--------|-------------|-----------|
| `validate-kops-ha` | cluster.justfile:216 | Calls bin/cluster-ops validate ha (already Go) | No brittleness — delegates to custodian | Keep as thin wrapper |
| `validate-hardening` | cluster.justfile:239 | Cluster Autoscaler, Node Problem Detector, Metrics Server, cert-manager, node-local DNS | kubectl get piped to jq, pods_running shell function (248-254), kubectl top timeout not distinguished from failure (280) | `internal/preflight/cluster.ValidateHardening` (reuses custodian checks where possible) |
| `validate-cluster` | cluster.justfile:319 | kops validate cluster (waits up to --waittime) | Shells out to kops (no stable API) — keep exec, wrap in Go | `internal/preflight/cluster.ValidateCluster` (exec wrapper with timeout + structured result) |
| `verify-cluster` | cluster.justfile:1486 | Composite: validate-cluster + validate-kops-ha + validate-hardening + kubeconfig export | Shell orchestration of 3 checks | `internal/preflight/cluster.VerifyCluster` (calls 3 checks, returns combined results) |
| `preflight-create` | cluster.justfile:1689 | Env vars, tools on PATH + pinned versions, GOOGLE_APPLICATION_CREDENTIALS, SA email match, bucket/keyring existence, SSH key, cluster/bucket non-existence | Reuses check-helpers.sh (1699), asdf current parsing (1756), gcloud bucket/keyring probes, error conflation on all GCP calls | `internal/preflight/cluster.PreflightCreate` → `cluster-ops preflight create` |

Notes:
- `validate-kops-ha` already calls Go (`bin/cluster-ops validate ha` → `internal/custodian.ValidateHA`) — keep thin wrapper, no migration needed.
- `validate-hardening` duplicates checks in custodian (cluster-autoscaler, node-problem-detector, metrics-server, cert-manager, node-local DNS) — refactor custodian to export reusable check functions, avoid duplication.
- `preflight-create` is the longest check (100+ lines) — gate for `create-cluster`, validates everything before the first mutating GCP call.

Contract test: `scripts/test-bootstrap-bundle.sh` asserts preflight-create runs before create-cluster. Order stays in shell; behavior → Go.

### Recipes Staying in Shell

| Recipe | File:Line | Why Not Migrating | Notes |
|--------|-----------|-------------------|-------|
| `cluster-status` | cluster.justfile:342 | Trivial kubectl wrapper (3 lines: version, cluster-info, get nodes) | No brittleness, not worth Go wrapper |
| `drift-manifests` | cluster.justfile:888 | Already uses Go (bin/cluster-ops kops normalize, lines 923-927) | Shell orchestrates, Go does heavy lifting — works well |
| `registry-show` | cluster.justfile:1996 | Pure yq data validation, no external calls | No brittleness (yq is stable), no SDK benefit |
| `logto verify` | logto.justfile:154 | Single-use recipe (one deployment), simple kubectl get checks | Low value, wait for patterns to stabilize |

## Brittleness catalog

Ranked by impact (highest → lowest):

1. **Error conflation** (all kubectl/gcloud recipes) — not-found/RBAC/unreachable all exit 1, no distinction. Operator sees "check failed", wastes time distinguishing "create the namespace" from "fix your kubeconfig". **Fix**: client-go `apierrors.IsNotFound/IsForbidden/IsUnauthorized` + net error wrapping → `internal/kube.Classify(err) Reason`.

2. **First-miss exit** (check-deploy-credentials:63, check-backend-prereqs exits on namespace miss before checking Secret) — operator gets one failure at a time, reruns check after each fix. **Fix**: collect all results, print all failures, exit after the summary (never short-circuit).

3. **Credentials in argv** (check-backend-prereqs:725 `curl -u "${dbuser}:${dbpass}"`) — password visible in `ps`, process accounting, shell history if variable unquoted. **Fix**: pass creds via HTTP client's auth field (net/http BasicAuth or arangodb/go-driver auth option), never argv.

4. **Port-forward false pass** (check-backend-prereqs:716 `nc -z localhost 18529` after kubectl port-forward backgrounded) — nc succeeds if the local port binds before the tunnel establishes; curl then hits a closed connection and the check blames the database. **Fix**: terratest `k8s.NewTunnel` with readiness probe (HTTP GET on a known endpoint, not just port bind).

5. **Hardcoded region** (verify-deployer-access, cluster.justfile:2132 `us-central1`; `scripts/pulumi/check-backend.sh` already parses the URI with a regex and is not affected) — breaks when registry KMS key is in a different region. **Fix**: parse region from `kms_secrets_provider` gcpkms URI with `internal/registry.ParseKMSKey` (regex on `projects/.../locations/([^/]+)/...`).

6. **Fixed port** (check-backend-prereqs:708 local_port=18529) — conflicts if two checks run concurrently or another process holds the port. **Fix**: terratest tunnel with `0` → free port from OS, return actual port in result.

7. **jq selector fragility** (all datastore check-pool/verify) — 3-line jq pipelines like `jq '[.items[] | select([.spec.taints[]? | select(.key == "dedicated" and .value == "'"$POOL"'" and .effect == "NoSchedule")] | length > 0)] | length'` (arangodb.justfile:3092-3094) break on minor API changes, hard to test. **Fix**: client-go strongly-typed field access (`node.Spec.Taints[i].Key == "dedicated"`).

8. **Version parsing** (check-tools.sh:20-26, preflight-create:1756 asdf current) — awk on human-readable output (`awk '/^Google Cloud SDK/ {print $NF}'`) breaks when format changes. **Fix**: use --json/--format=json flags where available; for tools without JSON, accept the fragility and document the expected format in a comment.

## Design

### Package layout

```
internal/
  preflight/          # Check orchestration + types
    preflight.go      # Status, Result types; Run([]IO[Result]) []Result
    print.go          # Print(w, []Result) — formats as check-helpers.sh lines
    backend/          # Backend/Pulumi checks
      backend.go      # CheckBackend, CheckBackendPrereqs, CheckStorageClass
    datastore/        # Datastore checks
      pool.go         # CheckPool (shared by all datastores)
      arangodb.go     # VerifyArangoDB, VerifyArangoAppCredentials
      postgres.go     # VerifyPostgres
      redis.go        # VerifyRedis
      minio.go        # VerifyMinio
      nats.go         # VerifyNats
    cluster/          # Cluster validation
      cluster.go      # ValidateCluster, ValidateHardening, VerifyCluster
      access.go       # VerifyDeployerAccess, CheckDeployCredentials
      preflight.go    # PreflightCreate
    toolchain/        # Tool checks
      toolchain.go    # CheckTools
  kube/               # Kubernetes client helpers
    client.go         # NewClients, Classify
    tunnel.go         # OpenServiceTunnel (terratest wrapper)
  registry/           # Cluster registry (config/clusters/<name>.yaml)
    registry.go       # Entry, LoadByName, LoadByStack, ParseKMSKey
  gcp/                # GCP SDK wrappers (already exists, extend as needed)
    kms.go            # (exists) KMS encrypt/decrypt
    storage.go        # GCS bucket describe + versioning check
    iam.go            # testIamPermissions + roles.get for permission expansion
```

### Shared foundation

Defined by `docs/plans/go-migration/checks-phase1-implementation.md` (the executable plan for Phase 1):

**internal/preflight/preflight.go**
```go
type Status string
const (
    StatusPass Status = "pass"
    StatusFail Status = "fail"
    StatusWarn Status = "warn"
    StatusInfo Status = "info"
)

type Result struct {
    Name   string // "namespace dev"
    Detail string // "present" or "missing — run namespace-bootstrap"
    Fix    string // "run namespace-bootstrap" (empty for pass/info)
    Status Status
}

// Run executes all checks, returns ALL results (never short-circuits)
func Run(checks []IO.IO[Result]) []Result

// Print emits check-helpers.sh format lines (PASS/FAIL/WARN/INFO)
func Print(w io.Writer, rs []Result)

// Failed returns true if any result is StatusFail
func Failed(rs []Result) bool
```

Checks are `IO.IO[Result]` — errors fold into fail Results via `E.Fold` (mirrors `internal/custodian/validate.go` HACheck pattern). Dependent checks compose inside one check (e.g. "check ArangoDB database" includes "port-forward succeeded" as a sub-result — either both pass or the parent fails with the sub-failure detail).

**internal/kube/client.go**
```go
type Clients struct {
    Kube kubernetes.Interface  // k8s.io/client-go/kubernetes
    Rest *rest.Config
}

func NewClients(kubeconfig, context string) E.Either[error, Clients]

type Reason string
const (
    ReasonNotFound     Reason = "not-found"
    ReasonForbidden    Reason = "forbidden"
    ReasonUnauthorized Reason = "unauthorized"
    ReasonUnreachable  Reason = "unreachable"  // net errors
    ReasonOther        Reason = "other"
)

func Classify(err error) Reason
```

`Clients.Kube` is `kubernetes.Interface` (not `*kubernetes.Clientset`) so tests use `k8s.io/client-go/kubernetes/fake.NewSimpleClientset()`.

**internal/kube/tunnel.go**
```go
type Tunnel struct {
    LocalPort  int
    RemotePort int
    close      func()  // idempotent closer
}

// OpenServiceTunnel wraps terratest k8s.NewTunnelWithLogger
// Returns free local port (remotePort passed by caller)
func OpenServiceTunnel(clients Clients, ns, svc string, remotePort int) IOE.IOEither[error, *Tunnel]
```

Terratest v1.0.1 facts: `ForwardPortE` internally calls non-E `GetService` (→ t.Fatal on missing service), no context param, no reconnect. Wrapper adapts `testing.T` — Fatal/FailNow panic with sentinel `fatalErr`, recovered by `runT` into an error; non-sentinel panics re-throw.

Always used through `IOE.WithResource` so `tunnel.Close()` runs exactly once (double-close panics — terratest closes a channel).

**internal/registry/registry.go**
```go
type Entry struct {
    Cluster             string `yaml:"cluster"`
    Stack               string `yaml:"stack"`
    KopsState           string `yaml:"kops_state"`
    GCPProject          string `yaml:"gcp_project"`
    KMSSecretsProvider  string `yaml:"kms_secrets_provider"`
    PulumiState         string `yaml:"pulumi_state"`
    Namespace           string `yaml:"namespace"`
    CIEnv               string `yaml:"ci_env"`
    KopsVersion         string `yaml:"kops_version"`
    KubectlVersion      string `yaml:"kubectl_version"`
    PulumiVersion       string `yaml:"pulumi_version"`
}

func LoadByName(dir, name string) E.Either[error, Entry]
func LoadByStack(dir, stack string) E.Either[error, Entry]

// KMSKey parses gcpkms://projects/.../locations/.../keyRings/.../cryptoKeys/...
type KMSKey struct {
    Project  string
    Location string
    KeyRing  string
    Key      string
}
func ParseKMSKey(uri string) E.Either[error, KMSKey]  // regex parse of Entry.KMSSecretsProvider
```

Fixes hardcoded `us-central1` — all KMS calls use `ParseKMSKey(entry.KMSSecretsProvider)` → `.Location`.

**CLI**

New `preflight` command tree in `cmd/cluster-ops/main.go` (urfave/cli v2, existing pattern):
```go
preflightCommand := &cli.Command{
    Name: "preflight",
    Subcommands: []*cli.Command{
        {Name: "backend-prereqs", Action: runBackendPrereqs},
        {Name: "backend", Action: runBackend},
        {Name: "tools", Action: runTools},
        {Name: "storageclass", Action: runStorageClass},
        {Name: "deployer-access", Action: runDeployerAccess},
        {Name: "deploy-credentials", Action: runDeployCredentials},
        {Name: "pool", Action: runPool},
        {Name: "arangodb", Action: runArangoDBVerify},
        // ... postgres, redis, minio, nats, cluster, create ...
    },
}
```

Each action: parse flags → build check list → `results := preflight.Run(checks)` → `preflight.Print(os.Stdout, results)` → exit 1 if `preflight.Failed(results)`.

Justfile recipes become thin wrappers:
```justfile
check-backend-prereqs folder stack="" arango_service="arangodb":
    go run ./cmd/cluster-ops preflight backend-prereqs \
        --folder {{ folder }} \
        --stack {{ if stack != "" { stack } else { "${PULUMI_STACK}" } }} \
        --arango-service {{ arango_service }}
```

Keep `Next: <recipe>` echoes in the justfile wrapper (after the go run line).

### Output contract

Visual format matches `scripts/lib/check-helpers.sh` exactly — Go `Print()` emits:
```
PASS  namespace dev  present
FAIL  secret backend  missing — create with ArangoDB credentials
INFO  project: my-gcp-project
WARN  no backup CronJob — expected only if backup stack applied
```

Two-arg form pads label to `CHECK_LABEL_WIDTH` env var (default 10, cluster recipes set 24):
```go
if len(r.Detail) > 0 {
    fmt.Fprintf(w, "%s%-*s  %s\n", prefix(r.Status), labelWidth, r.Name, r.Detail)
} else {
    fmt.Fprintf(w, "%s%s\n", prefix(r.Status), r.Name)
}
```

`CHECK_COLOR` env var: `1` (default) → ANSI color, `0` → plain `PASS`/`FAIL`/`WARN`/`INFO`.

Exit code: 0 if all pass/warn/info, 1 if any fail. Summary printed by wrapper recipe (not by Go — recipe knows which doc to reference):
```bash
results=$(go run ./cmd/cluster-ops preflight pulumi-backend 2>&1)
code=$?
echo "$results"
if [ $code -ne 0 ]; then
    echo "Backend check failed. See docs/reference/pulumi/backend-bootstrap.md"
fi
exit $code
```

### Integration points

Gate recipes (`bootstrap-service`, `deploy-backend-services`, all restore recipes) already have explicit check call sites:
```justfile
deploy-backend-services:
    just gcp-pulumi check-backend-prereqs --folder modware-annotation
    just gcp-pulumi deploy modware-annotation
    # ... repeat for each service ...
```

After migration:
```justfile
deploy-backend-services:
    go run ./cmd/cluster-ops preflight backend-prereqs --folder modware-annotation
    just gcp-pulumi deploy modware-annotation
    # ... no other changes ...
```

Zero behavioral change — check still runs, still fails loudly, still blocks deploy on failure.

### Secrets handling

Never log secret values. Rules:
1. Passwords/tokens read from env/Secret/file never appear in Result.Detail or Fix.
2. When a credential check fails, Detail says "credentials rejected (401)" or "encrypt failed", never the plaintext credential.
3. Client-go Secret reads → decode base64 in-process, pass to HTTP client/SDK via auth struct field (never argv).
4. GCP SA keys read from file → parse JSON for project/email (logged), pass file path to `option.WithCredentialsFile()` (SDK reads it, never logged).
5. Tunnel credentials (ArangoDB user/pass from Secret) → passed to arangosh via env vars (kubectl run --overrides `env: [{name: APP_USER, valueFrom: secretKeyRef}]`), never inline in `args:`.

Tests with hardcoded fake credentials document them as `// TEST CREDENTIAL, not real`.

## Phasing

Phase 1 is **executed by `docs/plans/go-migration/checks-phase1-implementation.md`** (the TDD implementation plan for the backend deploy gate subset + shared foundation). Phase 2 and Phase 3 get their own implementation plans later (written after Phase 1 lands).

### Phase 1: Foundation + Backend Deploy Gate

**Scope**: Shared foundation (preflight, kube, registry packages) + checks blocking `deploy-backend-services` (the most frequently run gate).

**Components**:
- `internal/preflight` — Status, Result, Run, Print, Failed
- `internal/kube` — NewClients, Classify, OpenServiceTunnel (terratest wrapper)
- `internal/registry` — Entry, LoadByName, LoadByStack, ParseKMSKey
- `internal/gcp/storage` — bucket describe + versioning check (new)
- `internal/gcp/iam` — testIamPermissions + roles.get (new; kms.go already exists)
- `internal/preflight/backend` — CheckBackendPrereqs, CheckBackend, CheckStorageClass
- `internal/preflight/cluster` — VerifyDeployerAccess, CheckDeployCredentials
- `internal/preflight/toolchain` — CheckTools
- CLI: `cluster-ops preflight backend-prereqs|backend|tools|storageclass|deployer-access|deploy-credentials`

**Migration order** (TDD, recipe by recipe):
1. CheckTools (simplest, no kube/GCP deps) — test with fake exec.Command
2. CheckBackend (GCP SDKs, no kube) — test with mocked GCP clients
3. CheckStorageClass (kube, no tunnel) — test with fake clientset
4. VerifyDeployerAccess (kube + GCP + kops exec) — test with fake clientset + mocked GCP + exec wrapper
5. CheckDeployCredentials (builds on VerifyDeployerAccess) — test with mocked IAM client
6. CheckBackendPrereqs (kube + tunnel, most complex) — test with fake clientset + mocked tunnel

Each recipe: write failing test → implement → update justfile wrapper → run contract test (shell, mocks `go`) → update reference doc flag table → commit.

**Acceptance**:
- `just check` zero failures (all contract tests green)
- `go run ./cmd/cluster-ops preflight pulumi-backend` on real cluster → same output as old `just gcp-pulumi check-backend`
- `go run ./cmd/cluster-ops preflight backend-prereqs --folder modware-annotation` on experiments cluster → lists all prereq failures (namespace, Secret, database), not just first miss
- Unit test covers Classify: not-found/forbidden/unauthorized/unreachable all return distinct Reason
- Unit test covers never-short-circuit: Run([fail1, fail2, fail3]) returns 3 results
- Updated docs: `docs/reference/pulumi/backend-bootstrap.md` flag tables reference `cluster-ops preflight <name> --help`

**Contract test changes**:
- `scripts/test-backend-prereqs.sh` — replace kubectl mock with `go` mock (records `go run ./cmd/cluster-ops preflight backend-prereqs` call), assert it runs before `pulumi up`
- `scripts/test-ci-recipes.sh` — replace gcloud mock with `go` mock, assert `preflight deploy-credentials` runs before `gh secret set`

**Risks**:
- Terratest tunnel panic recovery — if sentinel fatalErr leaks or a non-sentinel panic isn't re-thrown, tests hang or pass incorrectly. **Mitigation**: add TestTunnelPanicHandling that verifies both cases.
- KMS region regex brittle — gcpkms URI format undocumented. **Mitigation**: test against prod/experiments actual URIs, document expected format in ParseKMSKey godoc.

**Rollback**: Revert the commit that updated justfile wrappers; old recipes still present as `_check-backend-prereqs-shell` (renamed, not deleted).

### Phase 2: Datastore Checks

**Scope**: All datastore check-pool + verify recipes (10 total).

**Components**:
- `internal/preflight/datastore/pool.go` — CheckPool(clients, pool, wantCount, requireAmd64 bool) — shared by all datastores
- `internal/preflight/datastore/arangodb.go` — VerifyArangoDB, VerifyArangoAppCredentials
- `internal/preflight/datastore/postgres.go` — VerifyPostgres
- `internal/preflight/datastore/redis.go` — VerifyRedis
- `internal/preflight/datastore/minio.go` — VerifyMinio
- `internal/preflight/datastore/nats.go` — VerifyNats
- CLI: `cluster-ops preflight pool|arangodb|postgres|redis|minio|nats`

**Migration order** (most-used → least-used):
1. CheckPool (shared) — test with fake nodes (various taint/arch/zone combos)
2. VerifyArangoDB (most complex) — test with fake deployment, pods, PVCs, services
3. VerifyArangoAppCredentials (runs ephemeral pod) — test with fake pod API, verify overrides JSON structure
4. VerifyPostgres — test with fake postgres.CrunchydataPostgresCluster CRD
5. VerifyRedis, VerifyMinio, VerifyNats (similar patterns) — test with fake StatefulSets/Deployments

**Acceptance**:
- `just arangodb verify` on prod cluster → same output as current recipe, all checks listed
- CheckPool unit test: nodes in 3 zones, 1 zone → both pass (zone spread is info, not fail)
- CheckPool unit test: 2/3 nodes Ready → fail with "2/3 nodes Ready"
- VerifyArangoDB unit test: 8/9 pods ready → fail with "8/9 member pods Running and ready"
- Updated docs: `docs/reference/arangodb/*.md`, `docs/reference/postgres/*.md` flag tables

**Contract test changes**:
- `scripts/test-arangodb-finalization.sh` — mock `go run ./cmd/cluster-ops preflight arangodb`, assert runs before finalization

**Risks**:
- CRD types (PostgresCluster) not in fake clientset — may need manual fake construction. **Mitigation**: use unstructured.Unstructured if typed fake unavailable, document workaround.

**Rollback**: Revert justfile wrappers, old recipes renamed `_verify-shell`.

### Phase 3: Cluster Validation

**Scope**: Cluster-level validation (5 recipes: validate-kops-ha, validate-hardening, validate-cluster, verify-cluster, preflight-create).

**Components**:
- `internal/preflight/cluster/cluster.go` — ValidateCluster (kops exec wrapper), ValidateHardening, VerifyCluster (composite)
- `internal/preflight/cluster/preflight.go` — PreflightCreate (longest check, 100+ lines)
- Refactor `internal/custodian` — export check functions for reuse by ValidateHardening (cluster-autoscaler, node-problem-detector, metrics-server, cert-manager, node-local-dns)
- CLI: `cluster-ops preflight cluster|hardening|create`

**Migration order**:
1. ValidateHardening — reuse custodian checks, test with fake deployments/daemonsets
2. ValidateCluster — kops exec wrapper with timeout, test with mocked exec
3. VerifyCluster — composite of 3 checks, test with mocked sub-checks
4. PreflightCreate — longest, test with mocked GCP bucket/keyring calls + tool version parsing

**Acceptance**:
- `just gcp-cluster preflight-create` on new cluster env → lists all missing prereqs (bucket, keyring, SSH key), not first miss
- `just gcp-cluster validate-hardening` → same output as current recipe
- `just gcp-cluster verify-cluster --waittime 5` → runs 3 checks, prints combined results, exits 1 if any fail
- Unit test: PreflightCreate with missing bucket + keyring → 2 failures listed
- Updated docs: `docs/reference/cluster/*.md` flag tables

**Contract test changes**:
- `scripts/test-bootstrap-bundle.sh` — mock `go run ./cmd/cluster-ops preflight create`, assert runs before `kops create cluster`

**Risks**:
- kops validate cluster no stable API — must exec, parse human-readable output. **Mitigation**: document expected output format, test against real kops 1.29.2 + 1.35.x output.
- asdf version parsing fragile — `asdf current` output format not guaranteed. **Mitigation**: accept fragility, document expected format, add test fixture with real asdf output.

**Rollback**: Revert justfile wrappers, old recipes renamed `_validate-*-shell`.

## Testing strategy

**Unit tests** (Go):
- Fake kubernetes.Interface (k8s.io/client-go/kubernetes/fake.NewSimpleClientset) for all kube checks
- Mocked GCP clients (interfaces in internal/gcp) for bucket/KMS/IAM calls
- Table-driven tests for Classify: each k8s error type → expected Reason
- Never-short-circuit contract: test that Run([fail, fail, pass]) returns 3 results, not 1

**Integration tests** (`//go:build integration`):
- Run against local k3d cluster (`just test-preflight-integration` — writes `k3d kubeconfig get` output to a mktemp file and sets `KUBECONFIG` to that path)
- Create test namespace, Secret, Service → run CheckBackendPrereqs → assert pass
- Delete Secret → rerun → assert fail with "Secret missing"
- Never run in CI (require local k3d running)

**Contract tests** (shell, `scripts/test-*.sh`, wired into `just check`):
- Mock `go` on PATH (records calls to `go run ./cmd/cluster-ops preflight ...`)
- Assert order: preflight before mutation (existing contract)
- Behavior assertions REMOVED (client-go cannot be intercepted by PATH mocks) — moved to Go unit tests
- Example: `scripts/test-backend-prereqs.sh` mocks `go`, asserts `preflight backend-prereqs` called before `pulumi up`, no longer mocks kubectl

**Manual verification** (on real clusters):
- Run each migrated check on experiments cluster (k8s 1.28.8, kops 1.29.2)
- Compare output to current shell recipe — must match line-for-line (modulo timing)
- Introduce a known failure (delete a Secret) → verify new check lists it with correct Fix message

## Acceptance

Phase 1:
- [ ] `just check` zero failures
- [ ] `go run ./cmd/cluster-ops preflight pulumi-backend` matches output of `just gcp-pulumi check-backend` on experiments cluster
- [ ] Unit test: Classify distinguishes not-found/forbidden/unauthorized/unreachable
- [ ] Unit test: Run never short-circuits (3 failures → 3 results)
- [ ] Contract test: `scripts/test-backend-prereqs.sh` green (order assertion via mocked `go`)

Phase 2:
- [ ] `just arangodb verify` on prod cluster matches current output
- [ ] Unit test: CheckPool with 2/3 nodes Ready → fail "2/3 nodes Ready"
- [ ] Contract test: `scripts/test-arangodb-finalization.sh` green

Phase 3:
- [ ] `just gcp-cluster preflight-create` lists all missing prereqs (not first miss)
- [ ] `just gcp-cluster verify-cluster` runs 3 checks, combined results
- [ ] Contract test: `scripts/test-bootstrap-bundle.sh` green

All phases:
- [ ] Every migrated recipe has updated `docs/reference/<area>/*.md` flag table
- [ ] Zero hardcoded us-central1 (`rg 'us-central1' internal/` returns only test fixtures)
- [ ] Zero passwords in argv (`rg 'curl.*-u.*\$' internal/` returns nothing)
- [ ] All checks read-only (no create/update/delete calls in check functions)

## Risks

1. **Terratest API instability** — v1.0.1 tunnel API has known issues (no context, GetService calls non-E API). **Mitigation**: wrapper isolates terratest to `internal/kube/tunnel.go`; future migration to pure client-go port-forward is localized.

2. **kops has no stable Go API** — must exec for kops validate, kops export kubeconfig. **Mitigation**: wrap in `internal/kops/exec.go` (already exists), accept the exec brittleness, test against both kops 1.29.2 and 1.35.x output.

3. **Contract tests cannot intercept client-go** — existing shell tests mock kubectl on PATH; client-go bypasses PATH. **Mitigation**: keep ORDER assertions in shell (mock `go run`), move BEHAVIOR assertions to Go unit tests (fake clientset).

4. **GCP SDK API churn** — storage/KMS/IAM APIs may change. **Mitigation**: pin SDK versions in go.mod (already done), document version compatibility in each internal/gcp/*.go file.

5. **Result.Fix message drift** — if a Fix message references a renamed recipe, operator gets stale advice. **Mitigation**: contract test asserts Fix messages reference only existing recipes (`scripts/test-fix-messages.sh` — new, greps Result.Fix for "run X", asserts `just -n X` succeeds).

6. **Rollback leaves renamed recipes** — old recipes stay as `_name-shell`, clutter justfile. **Mitigation**: delete old recipes 1 release after migration (gives time for rollback), document deletion in the PR that removes them.

## Related plans

- **[Phase 1 Implementation](checks-phase1-implementation.md)** — Executable TDD plan for Phase 1 (backend deploy gate subset + foundation)
- **[Port-forward Flows](port-forward.md)** — Migrate ~24 port-forward call sites to terratest tunnel
- **[Wait/Poll Loops](wait-poll.md)** — Migrate ~12 wait/poll loops to client-go watch + timeout
- **[GCP Describe-Then-Create](gcp-converge.md)** — Migrate ~15 GCP describe-then-create patterns to idempotent converge
- **[Pulumi Automation API](pulumi-automation.md)** (optional) — Migrate Pulumi CLI exec to Automation API for structured errors
