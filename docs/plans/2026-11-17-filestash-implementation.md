# Filestash for MinIO Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy Filestash on `dcr-kube1` so that collaborators upload into a private `uploads` bucket and the public browses and downloads a `downloads` bucket.

**Architecture:** A new Pulumi Go project `filestash/` runs one Filestash pod with a state PVC. The pod reaches MinIO through the in-cluster Service. A new Gateway route exposes Filestash and blocks the admin paths. A new `just filestash` module creates the buckets, the four MinIO users, the bucket policy, the publication copy, the state backup, and the restore. Direct file URLs use the existing `storage.dictycr.org` MinIO route.

**Tech Stack:** Go 1.26, Pulumi Go SDK and `pulumi-kubernetes` v4, `github.com/IBM/fp-go/v2`, Gateway API on Cilium, Velero, `just`, `mc` (MinIO client), `yq`, `jq`, `curl`, `bash` contract tests.

**Spec:** [`docs/plans/2026-11-17-filestash-design.md`](2026-11-17-filestash-design.md). Read it before each task.

## Authorization

- Each `git commit` block in this plan is a future action. Run it only after the user approves commits.
- Task 1 starts local containers. Task 10 changes the live cluster. Each needs explicit user authorization before its first step.
- No task pushes to a remote.

## Global Constraints

- Bucket names: `uploads` (private) and `downloads` (anonymous `s3:GetObject` on `arn:aws:s3:::downloads/*` only). No anonymous `s3:ListBucket` on any bucket.
- MinIO users and policies. No Filestash path uses the MinIO root credential. Do not add a permission without explicit user approval (spec Section 7).

| User | Allowed actions |
|---|---|
| `filestash-intake` | `s3:PutObject` on `uploads/*`. `s3:ListBucket` on `uploads`. |
| `filestash-public` | `s3:GetObject` on `downloads/*`. `s3:ListBucket` on `downloads`. |
| `filestash-operator` | `s3:GetObject`, `s3:DeleteObject` on `uploads/*`. `s3:GetObject` on `downloads/*`. `s3:ListBucket` on both. |
| `filestash-publisher` | `s3:GetObject` on `uploads/*`. `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject` on `downloads/*`. `s3:ListBucket` on both. Recipes only. |

- One replica, strategy `Recreate`, PVC `ReadWriteOnce` on `dictycr-balanced` at `/app/data/state`.
- Image: pinned tag and digest. A config with `:latest` or no digest fails validation. The same rule applies to the plugin bundle image.
- Plugins: only the ZIP files in `filestash/plugins/plugins.lock.yaml`, each pinned by `sha256`. The browser shell (`features.server.console_enable`) stays `false`.
- Deployment uses this repository Pulumi project, not the vendor Helm chart (user decision).
- Container port `8334`. Health path `/healthz`. Share path `/s/<id>`. Admin paths `/admin` and `/admin/api/*`.
- Kubernetes Secret `filestash` (label `app.kubernetes.io/name=filestash`) holds `ADMIN_PASSWORD` and `CONFIG_SECRET`. Secret `filestash-users` holds the four MinIO key pairs. Secret names and key names stay plain text in `Pulumi.<stack>.yaml`.
- Recipe arguments default to `""` and resolve from flag, then cluster env or stack config, then fail with the flag name (`AGENTS.md`).
- Go code obeys the fp-go house style in `~/.pi/agent/AGENTS.md`. Run `golangci-lint fmt`, `golangci-lint run ./...`, `go test ./...`, and the gopls gate before each commit.
- `just check` passes with zero failures before each commit.
- Values not decided by the user stay placeholders (`<filestash-host>`, SMTP values). Validation rejects `<...>` placeholders in an enabled route or a deployed stack.
- **Deployment blockers.** Task 10 cannot start until the user supplies: the host name, the SMTP values, the retention period for `uploads`, the recovery point and recovery time targets, the capacity pass values, and the owner and frequency of the manual `just filestash verify` check.
- **No automated alerts in this plan.** The cluster has no alert system. Alerts are out of scope until the user selects one.
- **Proposed security refinement.** `filestash-publisher` and the removal of `downloads` write access from `filestash-operator` are design proposals, not user decisions. The user can reject them during plan review.

## Review Focus

1. **A file name with spaces, `#`, `%`, or non-ASCII letters in `publish`.** The copy must keep the exact key, and the printed URL must encode each path segment. Owned by Task 6 (`test_publish_quotes_special_keys`, `test_publish_encodes_urls`).
2. **Variants of the admin path** (`/Admin`, `//admin`, `/./admin`, `/%61dmin`, `/admin%2Fapi`). The route must not reach the admin console through a variant that Filestash accepts. Owned by Task 1 (image routing table), Task 4 (route rule test), Task 8 (`probe-admin`), and Task 10 (live probe).
3. **Loss or change of `CONFIG_SECRET` or the Filestash secret key.** Every share holds an encrypted credential, so a restore without the same values breaks all links. Owned by Task 7 (`test_restore_state_requires_secret`).
4. **A full state PVC.** Share creation and email codes write to SQLite. Owned by Task 7 (prune completed copies to `keep`) and Task 5 (`verify` fails above 80% use).
5. **A second `create-users` run.** It must reuse the stored keys, because rotation breaks every share. Owned by Task 5 (`test_create_users_is_idempotent`).

---

## File Structure

| Path | Responsibility |
|---|---|
| `filestash/Pulumi.yaml` | Project metadata. |
| `filestash/main.go` | `Run`, `NewFilestash`, `Install`. |
| `filestash/config.go` | `FilestashConfig`, `ReadConfig`, `ApplyDefaults`, `Validate`. |
| `filestash/config_test.go` | Table tests for defaults and validation. |
| `filestash/storage.go` | State PVC. |
| `filestash/seed.go` | ConfigMap `filestash-seed` with the seed `config.json`. |
| `filestash/plugins/` | Plugin lock files and the bundle image `Dockerfile`. |
| `filestash/deployment.go` | Deployment with init container and Secret env. |
| `filestash/service.go` | Service on port `8334`. |
| `filestash/statebackup.go` | CronJob for the SQLite-consistent state copy. |
| `filestash/install_test.go` | `pulumi.RunWithMocks` tests of the resources. |
| `filestash/policies/{intake,public,operator,publisher}.json` | MinIO user policies. |
| `filestash/policies/downloads-anonymous.json` | Anonymous bucket policy for `downloads`. |
| `filestash/manifests/publish-lease.yaml` | Lease template for the publication lock. |
| `filestash/manifests/restore-deny-egress.yaml` | NetworkPolicy for the restore test namespace. |
| `filestash/manifests/backup-rbac.yaml` | ServiceAccount, Role, and RoleBinding for the backup CronJob to scale the Deployment. |
| `config/templates/filestash-stack.yaml.tmpl` | Stack template. |
| `just_modules/pulumi.justfile` | New `scaffold-filestash-stack` recipe. |
| `just_modules/filestash.justfile` | All `just filestash` recipes. |
| `justfile` | `mod filestash`, `test-filestash`, add to `check`. |
| `scripts/test-filestash-recipes.sh` | Contract tests with mocked `kubectl`, `mc`, `velero`, `curl`, and `go run ./cmd/cluster-ops forward run`. |
| `gateway/config.go`, `gateway/routes.go`, `gateway/gateway.go`, `gateway/main_test.go` | Route key `filestash` and blocked admin paths. |
| `gateway/Pulumi.dcr-kube1.yaml` | Route entry, `enabled: false`. |
| `docs/filestash-deploy.md` | Guide. |
| `docs/reference/filestash/*.md` | Reference documents. |
| `docs/reference/filestash/filestash-deploy-workflow.png` | Workflow diagram. |

---

### Task 1: Verify the selected image (throwaway spike)

**Requires user authorization to run local containers.** The spec checked source revision `7509e60`. This task proves each fact against a released image. No production code. Use a local `docker` network with a throwaway MinIO container at the cluster version `bitnamilegacy/minio:2025.7.23-debian-12-r3`.

**Files:**
- Create: `docs/reference/filestash/image-facts.md`

**Interfaces:**
- Produces: image tag and digest, `ADMIN_PASSWORD` format, seed `config.json` shape, share URL form, upload result without `s3:GetObject`, admin routing table, conditional write support, sqlite3 image tag and digest.

- [ ] **Step 1:** Select the newest Filestash release tag. Record the digest from `docker buildx imagetools inspect machines/filestash:<tag>`.
- [ ] **Step 2:** Start the image with `ADMIN_PASSWORD` set to a bcrypt hash (`htpasswd -bnBC 10 "" <pw> | tr -d ':\n'`). Record if login works. Repeat with a plaintext value. Record the accepted format.
- [ ] **Step 3:** Start the image with an empty state volume and no `ADMIN_PASSWORD`. Record what `/admin` shows.
- [ ] **Step 4:** Configure one S3 connection with pinned `type`, `endpoint`, `region`. Copy the resulting `config.json` connection block. This is the seed shape for Task 2.
- [ ] **Step 5:** Create the four MinIO users with the Global Constraints policies. Log in as `filestash-intake`. Create an upload-only share on `/uploads/alice/`. Upload a file through the share. If it fails, stop and report the exact error to the user. Do not widen the policy.
- [ ] **Step 6:** Record the share URL form and the WebDAV behavior under `/s/<id>/`.
- [ ] **Step 7:** Record whether `Range: bytes=0-99` through the public share returns HTTP 206.
- [ ] **Step 8: Admin routing table.** Send `GET`, `POST`, `PUT`, `DELETE`, `OPTIONS` to each of `/admin`, `/admin/`, `/Admin`, `//admin`, `/./admin`, `/x/../admin`, `/%61dmin`, `/admin%2Fapi/config`, `/admin/api/session`, `/admin/api/config` directly to the container. Record which variants reach admin content. Task 4 must block every variant that does.
- [ ] **Step 9: Conditional write.** Send a `PutObject` with `If-None-Match: *` to an existing key in the MinIO container. Record whether MinIO returns HTTP 412. Record whether `mc cp` can send that header.
- [ ] **Step 9a:** Load the selected plugin ZIPs. Record each `manifest.json` module type, the `/about` plugin list, and the config keys for search timeout and the shell. Record if a public link visitor can open the F4 web console. Open one sample file for each viewer: a `.psd`, a `.bam`, a `.pdb`, a `.nef`, and a `.docx`. Record if `.fasta`, `.gff3`, and `.vcf` files open as text.
- [ ] **Step 9b: State size inputs.** Record the size of each plugin ZIP (the `office` ZIP holds LibreOffice WASM). Record the size of `state/db/*.sql` after 100 shares. Record if `state/log/` rotates and its growth for 1,000 requests. Record what Filestash writes to `/app/data/cache` during a large ZIP download. Task 2 sizes come from these values. If they do not fit `5Gi` and `2Gi`, change the scaffold values.
- [ ] **Step 10:** Select a sqlite3 image (proposal: `keinos/sqlite3`). Record tag and digest.
- [ ] **Step 11:** Write each answer in `image-facts.md` as a table: fact, result, command.
- [ ] **Step 12: Commit** (after approval)

```bash
git add docs/reference/filestash/image-facts.md
git commit -m "docs(filestash): record verified image behavior"
```

---

### Task 2: Pulumi project `filestash/`

**Files:**
- Create: every `filestash/*.go` file and `filestash/Pulumi.yaml` in File Structure, except `statebackup.go`.
- Create: `filestash/ingress.go` (nginx Ingress mode, copied from the `dcr-mcp/ingress.go` pattern)
- Create: `filestash/Pulumi.dcr-kube1.yaml` and `filestash/Pulumi.experiments.yaml` with `just gcp-pulumi scaffold-filestash-stack` (Task 5). Commit both.
- Test: `filestash/config_test.go`, `filestash/install_test.go`

**Two clusters, two exposure modes.** The clusters expose services in different ways. The stack file selects the mode.

| Value | `dcr-kube1` (`Pulumi.dcr-kube1.yaml`) | `experiments` (`Pulumi.experiments.yaml`) |
|---|---|---|
| `namespace` (from `config/clusters/*.yaml`) | `prod` | `dev` |
| Exposure | Gateway route in `gateway/` (Task 4). `ingress.enabled: false`. | nginx Ingress from this project. `ingress.enabled: true`. The cluster has no Gateway stack. |
| Filestash host | `<filestash-host>` (proposal `files.dictycr.org`) | `<filestash-host>` (proposal `files.dictybase.dev`) |
| Direct file URL host (first MinIO host) | `storage.dictycr.org` | `storage.dictybase.dev` |
| TLS | Gateway listener, Secret `filestash-tls` | `kcert.dev/ingress: managed` label, Secret `filestash-tls-secret` |
| `storage.size` | `5Gi` | `2Gi` |
| `storage.storageClass` | `dictycr-balanced` | `dictycr-balanced` (exists on the cluster) |

Ingress mode (experiments only):
- Ingress `filestash` on `/` to Service `filestash:8334` with annotations `nginx.ingress.kubernetes.io/proxy-body-size: "0"`, `proxy-request-buffering: "off"`, `proxy-read-timeout: "3600"`, `proxy-send-timeout: "3600"`. The Filestash Helm guide states that a 60-second proxy timeout breaks large uploads.
- A second Ingress `filestash-admin-block` on `Prefix /admin` with annotation `nginx.ingress.kubernetes.io/permanent-redirect: /`. This uses a standard annotation, not a snippet. Task 1 admin routing table and Task 8 `probe-admin` apply to both clusters.
- `just filestash publish` reads the direct file URL host from the `minio` ingress hosts in `minio/Pulumi.experiments.yaml` when the stack has no Gateway route.

**Interfaces:**
- Consumes: Task 1 image tag, digest, and seed shape.
- Produces: stack key `filestash:properties`:

```yaml
filestash:properties:
  name: filestash
  namespace: <from registry>
  applicationURL: https://<filestash-host>
  port: 8334
  replicas: 1                      # validation rejects any other value
  image: {name: machines/filestash, tag: <tag>, digest: sha256:<digest>}
  secretName: filestash            # keys ADMIN_PASSWORD, CONFIG_SECRET
  storage: {size: <from stack file>, storageClass: dictycr-balanced}   # size required, no code default
  cacheSizeLimit: 2Gi              # emptyDir for /app/data/cache
  ingress: {enabled: <bool>, className: nginx, hosts: [<host>], tlsSecret: <secret>, label: {name: kcert.dev/ingress, value: managed}}
  s3: {endpoint: http://minio.<namespace>.svc.cluster.local:9000, region: us-east-1, label: dictycr-minio}
  nodePool: <application pool label value>
```

- Produces: names `filestash` (Deployment, Service), `filestash-state` (PVC), `filestash-seed` (ConfigMap), label `app.kubernetes.io/name=filestash` on every resource.

- [ ] **Step 1: Write failing config tests** in `config_test.go`:
  - `TestApplyDefaults` asserts `port=8334`, `replicas=1`, `storage.storageClass=dictycr-balanced`, `cacheSizeLimit=2Gi`, `secretName=filestash`, `s3.region=us-east-1`. `storage.size` stays empty.
  - `TestValidateRejects` table: empty `storage.size`, `storage.size` that `resource.ParseQuantity` rejects, `storage.size` below `1Gi`, `ingress.enabled=true` with no host, empty namespace, `image.tag=latest`, empty `image.digest`, `replicas=2`, `applicationURL` with `<` or `>`, `applicationURL` without `https://`, empty `s3.endpoint`. Each error names the field.
- [ ] **Step 2:** Run `go test ./filestash/...`. Expected: FAIL, undefined `FilestashConfig`.
- [ ] **Step 3:** Implement `FilestashConfig`, `ReadConfig(ctx *pulumi.Context) (*FilestashConfig, error)`, `(*FilestashConfig).ApplyDefaults()`, `(*FilestashConfig).Validate() error`. Follow `dcr-mcp/config.go` checks in fp-go style with lenses.
- [ ] **Step 4:** Run `go test ./filestash/...`. Expected: PASS.
- [ ] **Step 5: Write failing resource tests** in `install_test.go` with `pulumi.RunWithMocks`:
  - `TestDeploymentShape`: `replicas=1`, `strategy.type=Recreate`, image `machines/filestash:<tag>@sha256:<digest>`, `runAsNonRoot=true`, port `8334`, readiness and liveness `httpGet /healthz`, mount `/app/data/state` from claim `filestash-state`.
  - `TestDeploymentEnv`: `ADMIN_PASSWORD` and `CONFIG_SECRET` from `secretKeyRef` on Secret `filestash`. `APPLICATION_URL` equals `applicationURL`. No env holds a literal secret.
  - `TestInitContainerSeedsOnlyWhenAbsent`: the init container copies `/seed/config.json` to `/app/data/state/config/config.json` only if the target does not exist.
  - `TestSeedHasNoCredentials`: the ConfigMap JSON has one connection with `type=s3`, the pinned `endpoint` and `region`, and no `access_key_id` or `secret_access_key`.
  - `TestServicePort`: Service `filestash` port `8334`.
  - `TestPVCSize`: PVC `filestash-state` requests `storage.size` from the stack file, class `dictycr-balanced`, mode `ReadWriteOnce`.
  - `TestCacheEmptyDir`: volume for `/app/data/cache` is an `emptyDir` with `sizeLimit` equal to `cacheSizeLimit`.
  - `TestIngressModeExperiments`: with `ingress.enabled=true`, Ingress `filestash` has the four nginx annotations and the kcert label, and Ingress `filestash-admin-block` has the redirect annotation on `Prefix /admin`.
  - `TestNoIngressWhenDisabled`: with `ingress.enabled=false`, no Ingress resource.
  - `TestStackFilesValidate`: load `Pulumi.dcr-kube1.yaml` and `Pulumi.experiments.yaml` from disk. Each passes `Validate` except for `<...>` placeholders in disabled modes.
- [ ] **Step 6:** Run `go test ./filestash/...`. Expected: FAIL.
- [ ] **Step 7:** Implement `storage.go`, `seed.go`, `deployment.go`, `service.go`, `(*Filestash).Install(ctx *pulumi.Context) error`.
- [ ] **Step 8:** Run `go test ./filestash/...`, `golangci-lint fmt`, `golangci-lint run ./...`, gopls gate. Expected: clean.
- [ ] **Step 9: Commit** (after approval)

```bash
git add filestash/
git commit -m "feat(filestash): add Pulumi project for Filestash"
```

---

### Task 3: Plugin bundle image

The user selected these plugins. Each one has a different delivery path.

| Plugin | Kind | Source | Delivery |
|---|---|---|---|
| `plg_theme_apple` | Runtime ZIP (CSS) | `https://downloads.filestash.app/upload/theme_apple.zip` | Bundle image |
| `plg_application_adobe` | Runtime ZIP (`xdg-open`) | `https://demo.filestash.app/assets/plugin/application_adobe.zip` | Bundle image |
| `plg_application_biomed` | Runtime ZIP (`xdg-open`) | `https://demo.filestash.app/assets/plugin/application_biomed.zip` | Bundle image |
| `plg_application_photography` | Runtime ZIP (`xdg-open`) | `https://demo.filestash.app/assets/plugin/application_photography.zip` | Bundle image |
| `plg_application_office` | Runtime ZIP (`middleware` WASM and `xdg-open`), no prebuilt ZIP | `server/plugin/plg_application_office` at the Filestash tag from Task 1, plus LibreOffice WASM files from `cdn.zetaoffice.net` | Built in the bundle image from source |
| `plg_search_stateless` | Compiled | Already in the official image | Configuration only |
| `plg_image_c` | Compiled | Already in the official image | Configuration only |

**Files:**
- Create: `filestash/plugins/plugins.lock.yaml` (name, URL, `sha256`, license, reviewer, review date)
- Create: `filestash/plugins/Dockerfile` (stage 1 builds `application_office.zip` with `clang --target=wasm32`, stage 2 downloads and checks the vendor ZIPs, final stage is `busybox` with `/plugins/*.zip`)
- Create: `filestash/plugins/office-deps.lock.yaml` (each zetaoffice file URL and `sha256`)
- Modify: `filestash/config.go` (`plugins: {image: {name, tag, digest}}`), `filestash/deployment.go`, `filestash/install_test.go`
- Modify: `just_modules/filestash.justfile` (`build-plugins`, `verify`), `scripts/test-filestash-recipes.sh`

**Interfaces:**
- Consumes: Task 1 Filestash tag. Task 2 Deployment.
- Produces: image `dictybase/filestash-plugins:<tag>` and init container `plugins` that runs before `seed`.

Rules:
- Each download checks its `sha256` from a lock file. A mismatch fails the build.
- **License gate.** Before Step 1, record the license of each vendor ZIP and of the zetaoffice files in `plugins.lock.yaml`. If a license does not permit redistribution in a public image, stop and report to the user. The alternative is a private registry for the bundle image.
- **Review gate.** Each ZIP runs JavaScript in the browser of every public visitor. A person reviews each ZIP `manifest.json` and lists its module types in `plugins.lock.yaml`. A ZIP with a `patch` or `middleware` module needs a note from the reviewer.
- The init container deletes every `*.zip` in `/app/data/state/plugins/` and then copies the bundle ZIPs. Plugins that staff add by hand do not survive a restart.

Configuration (set in the seed `config.json`, keys confirmed in Task 1):
- `features.search.explore_timeout`: proposed `5` seconds. `plg_search_stateless` lists objects recursively, so a search on the public link sends many list requests to MinIO.
- `features.server.console_enable`: `false`. This setting turns on the browser bash shell at `/admin/tty/`.
- The F4 web console in the file browser: record in Task 1 if a public link visitor can open it. If the visitor can, find the setting that turns it off, or stop and report to the user.

- [ ] **Step 1: Write failing tests:**
  - `TestPluginInitContainerOrder`: init containers are `plugins`, then `seed`. The `plugins` image has a digest.
  - `TestPluginInitReplacesZips`: the `plugins` command deletes `/app/data/state/plugins/*.zip` before the copy.
  - `TestSeedDisablesConsole`: the seed JSON has `features.server.console_enable=false`.
  - `test_plugins_lock_complete`: each `plugins.lock.yaml` entry has a 64-character `sha256`, a license, and a reviewer.
  - `test_build_plugins_fails_on_checksum`: a mock download with a wrong checksum gives a non-zero exit.
  - `test_verify_checks_plugins`: `verify` fails if `/about` through port-forward does not list each plugin from the lock file, or if `/admin/tty/` does not return HTTP 404.
- [ ] **Step 2:** Run tests. Expected: FAIL.
- [ ] **Step 3:** Implement. `just filestash build-plugins --tag <t>` builds and pushes the image and prints the digest for the stack config.
- [ ] **Step 4:** Run `go test ./filestash/...`, lint, gopls gate, `just check`. Expected: PASS.
- [ ] **Step 5: Commit** (after approval)

```bash
git add filestash/ just_modules/filestash.justfile scripts/test-filestash-recipes.sh
git commit -m "feat(filestash): add pinned plugin bundle image"
```

---

### Task 4: Gateway route with blocked admin paths

**Files:**
- Modify: `gateway/config.go` (route key, `Filestash ServiceRouteConfig`, `BlockedPathPrefixes []string` on `ServiceRouteConfig`, `Validate` with `F.Pipe10`)
- Modify: `gateway/routes.go`, `gateway/gateway.go` (route and listener lists)
- Modify: `gateway/Pulumi.dcr-kube1.yaml`, `config/templates/gateway-stack.yaml.tmpl`
- Test: `gateway/main_test.go`

**Interfaces:**
- Consumes: Task 1 admin routing table.
- Produces: route key `filestash`, HTTPRoute `filestash-route`, TLS Secret `filestash-tls`.

- [ ] **Step 1: Write failing tests:**
  - `TestFilestashRouteBlocksAdmin`: with `blockedPathPrefixes: [/admin]`, the HTTPRoute has a rule with `PathPrefix /admin` and a `RequestRedirect` filter (`path.replaceFullPath: /`, `statusCode: 302`) and no `backendRefs`. The `/` rule forwards to `filestash:8334`.
  - `TestFilestashRouteBlocksEveryAdminVariant`: one blocked rule for each variant that Task 1 marked as reaching admin content. If a variant cannot be matched by a Gateway API path match, the test fails with the variant name.
  - `TestFilestashRouteDisabledByDefault`: `enabled=false` gives no `filestash-route` and no listener.
  - `TestValidateRejectsBlockedPathWithoutSlash`: `blockedPathPrefixes: [admin]` fails.
  - Extend `TestConfigValidateFailures` with a placeholder host case for an enabled `filestash` route.
- [ ] **Step 2:** Run `go test ./gateway/...`. Expected: FAIL.
- [ ] **Step 3:** Implement. Other routes keep an empty `BlockedPathPrefixes`. `TestInstallCreatesIssuerGatewayAndEnabledHTTPRoutes` passes with no edit.
- [ ] **Step 4:** Add the route to `gateway/Pulumi.dcr-kube1.yaml` with `enabled: false`, `hosts: [<filestash-host>]`, `tlsSecret: filestash-tls`, `serviceName: filestash`, `servicePort: 8334`, `path: /`, and the blocked prefixes. Add the same block to the template.
- [ ] **Step 5:** If Step 3 cannot block a variant, stop. Keep the route disabled. Report the variant to the user and request approval for an alternative control.
- [ ] **Step 6:** Run `go test ./gateway/...`, lint, gopls gate, `just test-gateway`. Expected: PASS.
- [ ] **Step 7: Commit** (after approval)

```bash
git add gateway/ config/templates/gateway-stack.yaml.tmpl
git commit -m "feat(gateway): add filestash route with blocked admin paths"
```

---

### Task 5: `just filestash` module (stack, secrets, buckets, users, deploy, verify)

**Files:**
- Create: `just_modules/filestash.justfile`, `config/templates/filestash-stack.yaml.tmpl`, `filestash/policies/*.json`, `scripts/test-filestash-recipes.sh`
- Modify: `just_modules/pulumi.justfile` (`scaffold-filestash-stack`), `justfile` (`mod filestash`, `test-filestash`, `check` list)

**Interfaces:**
- Consumes: Task 2 stack keys and names.
- Produces: Secret `filestash-users` with keys `intakeAccessKey`, `intakeSecretKey`, `publicAccessKey`, `publicSecretKey`, `operatorAccessKey`, `operatorSecretKey`, `publisherAccessKey`, `publisherSecretKey`.

All arguments default to `""`. The namespace comes from `properties.namespace` in `filestash/Pulumi.<stack>.yaml`. A flag mismatch fails before the first mutation.

| Recipe | Effect |
|---|---|
| `gcp-pulumi scaffold-filestash-stack --state-size <q> --host <h>` | Copy of `scaffold-dcr-mcp-stack`. Reads namespace and KMS provider from `config/clusters/*.yaml`. Writes `storage.size` from `--state-size` (required, no default). Sets `ingress.enabled: true` only if `gateway/Pulumi.<stack>.yaml` does not exist. Fails on `:latest`. |
| `filestash check` | Read-only preflight: namespace, Secret `filestash` with both keys, MinIO Service. |
| `filestash create-secret` | Hidden prompt for the admin password. Stores the format from Task 1 and a random 32-character `CONFIG_SECRET`. Adds the label. Describe-then-create. Never replaces an existing `CONFIG_SECRET`. |
| `filestash create-buckets` | `mc mb --ignore-existing` for `uploads` and `downloads`. `mc anonymous set none` on `uploads`. |
| `filestash create-users` | Reads or generates the eight values in `filestash-users`. Creates policies, users, and attachments with `mc admin`. Uses `minio-root` through `go run ./cmd/cluster-ops forward run`, env only. |
| `filestash publish-bucket` | `mc anonymous set-json policies/downloads-anonymous.json` on `downloads`. |
| `filestash unpublish-bucket` | `mc anonymous set none` on `downloads`. |
| `filestash deploy` | `check`, `pulumi up` on `filestash/`, `kubectl rollout status`. |
| `filestash admin` | Port-forward to Service `filestash` with `trap ... EXIT`. Prints the local admin URL. |
| `filestash verify` | Pod ready, PVC bound, PVC use below 80%, `/healthz` 200 through port-forward, `downloads` anonymous policy equals the policy file, `uploads` anonymous `none`. Task 7 adds backup checks. Non-zero exit on any failure. |
| `filestash resize-state --size <q>` | Grows the PVC. Checks that the storage class has `allowVolumeExpansion: true` and that the new size is larger. Sets `storage.size` with `set-config`, then runs `deploy`. A shrink is not possible and fails before any change. |
| `filestash teardown` | Destructive. Requires `--delete-pvc yes` to delete state. Prints the share count first. |

- [ ] **Step 1: Write failing contract tests** in `scripts/test-filestash-recipes.sh` (pattern of `scripts/test-dcr-mcp-recipes.sh`):
  - `test_policy_files`: `jq` asserts the exact `Action` and `Resource` sets from Global Constraints for each of the four files. Asserts `intake.json` has no `s3:GetObject` and no `s3:DeleteObject`. Asserts `operator.json` has no `s3:PutObject` on `downloads`. Asserts `downloads-anonymous.json` has only `s3:GetObject` on `arn:aws:s3:::downloads/*`.
  - `test_create_users_is_idempotent`: with `filestash-users` present, no `kubectl create secret` call, and the same keys go to `mc admin user add`.
  - `test_create_users_never_passes_keys_in_argv`: the call log has no secret value.
  - `test_namespace_mismatch_fails_before_mutation`: `--namespace other` against stack namespace `prod` exits non-zero. The log has no `mc` and no `pulumi up` line.
  - `test_missing_stack_names_flag`: no `PULUMI_STACK` and no `--stack` prints `--stack`.
  - `test_unpublish_bucket_sets_none`, `test_teardown_requires_flag`, `test_create_secret_keeps_config_secret`.
  - `test_verify_fails_on_full_pvc`: mock `df` output at 81% gives a non-zero exit.
  - `test_scaffold_requires_state_size`: no `--state-size` prints `--state-size` and writes no file.
  - `test_scaffold_selects_ingress_mode`: for stack `experiments` (no gateway stack file) the output has `ingress.enabled: true` and namespace `dev`. For `dcr-kube1` it has `ingress.enabled: false` and namespace `prod`.
  - `test_resize_rejects_shrink`: `just filestash resize-state --size 1Gi` against a `5Gi` PVC exits non-zero before any change.
- [ ] **Step 2:** Run `just test-filestash`. Expected: FAIL.
- [ ] **Step 3:** Implement. Quote each user value with `quote()`. Use `mktemp` templates with `XXXXXX`.
- [ ] **Step 4:** Run `just test-filestash` and `just check`. Expected: PASS.
- [ ] **Step 5: Commit** (after approval)

```bash
git add just_modules/filestash.justfile just_modules/pulumi.justfile justfile \
  config/templates/filestash-stack.yaml.tmpl filestash/policies scripts/test-filestash-recipes.sh
git commit -m "feat(filestash): add just module for users, buckets, and deploy"
```

---

### Task 6: Publication recipes

**Files:**
- Create: `filestash/manifests/publish-lease.yaml`
- Modify: `just_modules/filestash.justfile`, `scripts/test-filestash-recipes.sh`

**Interfaces:**
- Consumes: `publisherAccessKey` and `publisherSecretKey` (Task 5). Task 1 conditional write result.
- Produces: `just filestash publish --source <collaborator>/<path> --dataset <name>`, `just filestash unpublish-file --key <dataset>/<path>`, and `just filestash release-lock --holder <id>`.

`publish` behavior, in this order:
1. Validate `--dataset` against `^[a-z0-9][a-z0-9._-]*$`. Reject a `--source` with a `..` segment.
2. Take the single-writer lock. Render `filestash/manifests/publish-lease.yaml` with `yq`, with `spec.holderIdentity` set to `<user>@<host>:<pid>`. Run `kubectl create -f -`. Never use `kubectl apply` for the lock. If the Lease exists, exit and print its holder. In `trap ... EXIT`, delete the Lease only if its `holderIdentity` equals this process value. After a crash, the Lease stays. The operator removes it with `just filestash release-lock --holder <id>`, which deletes it only if the holder matches. `unpublish-file` takes the same lock.
3. List source keys with `mc ls --recursive --json`. Fail if empty.
4. `mc stat` each destination key. If any exists, print all existing keys and exit before the first copy.
5. Copy each key with `--attr "Content-Type=<type>"`. If Task 1 proved conditional write support, send `If-None-Match: *` and treat HTTP 412 as a failure. `<type>` comes from an extension table: `csv text/csv`, `tsv text/tab-separated-values`, `txt text/plain`, `json application/json`, `gz application/gzip`, `zip application/zip`, `fa`/`fasta`/`gff`/`gff3` `text/plain`, `pdf application/pdf`, `png image/png`, `jpg`/`jpeg image/jpeg`, `mp4 video/mp4`. Other extensions use `application/octet-stream`.
6. Print each direct URL `https://<minio host>/downloads/<dataset>/<key>`. Percent-encode each path segment with `jq -rn --arg s "$seg" '$s|@uri'`. The host is the first `minio` route host in `gateway/Pulumi.<stack>.yaml`.

`unpublish-file` requires `--confirm yes`, deletes one key, and prints the URL that now returns HTTP 404.

- [ ] **Step 1: Write failing tests:**
  - `test_publish_refuses_existing_key`: mock `mc stat` succeeds for one key. Exit non-zero. No `mc cp` in the log.
  - `test_publish_sets_content_type`: `table.csv` gives `text/csv`. `x.unknown` gives `application/octet-stream`.
  - `test_publish_rejects_dotdot`: `--source alice/../bob` exits before any `mc` call.
  - `test_publish_quotes_special_keys`: key `alice/my file #1 %ü.csv` reaches `mc stat` and `mc cp` as one argument, unchanged.
  - `test_publish_encodes_urls`: the same key prints `.../my%20file%20%231%20%25%C3%BC.csv`.
  - `test_publish_uses_publisher_keys`: the forward child env uses `publisherAccessKey`, not `minio-root` and not `operatorAccessKey`.
  - `test_publish_fails_when_lease_held`: mock `kubectl create -f -` fails with `AlreadyExists`. Exit non-zero. No `mc` call. No Lease delete.
  - `test_publish_never_applies_lease`: the log has no `kubectl apply`.
  - `test_release_lock_checks_holder`: `--holder other` with a different stored holder exits non-zero and deletes nothing.
  - `test_unpublish_file_requires_confirm`.
- [ ] **Step 2:** Run `just test-filestash`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `just test-filestash` and `just check`. Expected: PASS.
- [ ] **Step 5: Commit** (after approval)

```bash
git add just_modules/filestash.justfile scripts/test-filestash-recipes.sh
git commit -m "feat(filestash): add publish and unpublish-file recipes"
```

---

### Task 7: State backup and restore

**Files:**
- Create: `filestash/statebackup.go`
- Create: `filestash/manifests/backup-rbac.yaml`, `filestash/manifests/restore-deny-egress.yaml`
- Modify: `filestash/config.go` (`backup: {schedule, keep, maxAgeHours, image: {name, tag, digest}}`), `filestash/install_test.go`, `just_modules/filestash.justfile`, `scripts/test-filestash-recipes.sh`

**Interfaces:**
- Consumes: Task 1 sqlite image. Task 2 PVC name.
- Produces: CronJob `filestash-state-backup`. Completed copies in `/app/data/state/backups/<UTC yyyymmddThhmmssZ>/` with `manifest.json` (`timestamp`, file names, `sha256`, `integrity: ok`). Recipes `backup-now` and `restore-state`.

Defaults: `schedule: "30 */4 * * *"`, `keep: 7`. `maxAgeHours` has no default. It comes from the recovery point target, which is a deployment blocker.

**Maintenance interval.** Separate SQLite copies and a later `config.json` copy do not give one consistent application state. The CronJob therefore stops Filestash during the copy. Filestash is unavailable for the copy time (expected: less than one minute). Record the measured time in Task 10.

CronJob behavior:
1. Scale Deployment `filestash` to 0 and wait until no Filestash pod exists. Use the ServiceAccount from `backup-rbac.yaml`.
2. Write into `backups/.partial-<timestamp>/`.
3. Run `sqlite3 <file> ".backup <dest>"` for each `*.sql`. Run `PRAGMA integrity_check` on each copy. Fail unless the output is `ok`.
4. Copy `config/config.json`. Write `manifest.json` with the row count of each table and the completion timestamp, after all checks pass.
5. Rename `.partial-<timestamp>` to `<timestamp>` (same file system, atomic).
6. Scale Deployment `filestash` to 1 in a `trap ... EXIT`, so the pod comes back after a failure too.
7. Delete `.partial-*` directories older than one hour. Prune only directories that have a `manifest.json`, and keep the newest `keep`.

Recipes:

| Recipe | Effect |
|---|---|
| `filestash backup-now` | Runs a Job from the CronJob and waits. Then runs `velero backup create filestash-<timestamp> --include-namespaces <ns> --selector app.kubernetes.io/name=filestash` and waits for `Completed`. |
| `filestash restore-state --backup <name> --target-namespace <ns>` | Test restore into an isolated namespace. Refuses the live namespace. Validates before mutation: the Velero backup is `Completed`, it contains the PVC and Secret `filestash`, and the target namespace does not exist. Creates the namespace with `restore-deny-egress.yaml`, so restored credentials cannot reach MinIO or SMTP. Restores with `--namespace-mappings`. Runs a Job that replaces `*.sql` and `config.json` with the newest completed copy after its `sha256` check, and clears `email.*` in `config.json`. Starts one Filestash pod with no Service and no route. Passes when `/healthz` returns 200 through port-forward and each table row count equals the manifest. |
| `filestash restore-state --backup <name> --in-place yes` | Disaster recovery in the live namespace. Destructive. Validates the same preflight. Scales `filestash` to 0, replaces the state from the completed copy, keeps the live Secret `filestash` only if its `CONFIG_SECRET` equals the backup value, and scales to 1. |

`verify` adds: the newest completed copy and the newest Velero backup that holds the PVC are each younger than `maxAgeHours`. The Filestash pod has no `backup.velero.io/backup-volumes-excludes` that names the state volume. The Velero Schedule in `operators` does not exclude the namespace.

- [ ] **Step 1: Write failing tests:**
  - `TestStateBackupCronJob`: mounts claim `filestash-state`, uses the backup ServiceAccount, `concurrencyPolicy: Forbid`, image with digest.
  - `TestStateBackupCommandOrder`: the script writes to `.partial-`, runs the integrity check before the rename, and prunes only directories with `manifest.json`.
  - `TestValidateRequiresMaxAge`: empty `backup.maxAgeHours` fails validation.
  - `test_verify_fails_on_stale_backup`, `test_verify_fails_on_velero_exclusion`.
  - `test_restore_state_requires_secret`: a Velero backup without Secret `filestash` exits before any mutation.
  - `test_restore_state_refuses_live_namespace`: `--target-namespace prod` without `--in-place yes` gives a non-zero exit before any mutation.
  - `test_restore_state_applies_deny_egress_first`: the NetworkPolicy line precedes the `velero restore create` line.
  - `test_restore_state_clears_smtp`: the restore Job script clears `email.*`.
  - `TestStateBackupScalesDownFirst`: the CronJob script scales to 0 before the first `sqlite3` line and scales to 1 in a `trap`.
  - `test_restore_state_preflight_precedes_mutation`: in the call log, every `velero backup describe` and `kubectl get` line comes before the first `velero restore create` line.
- [ ] **Step 2:** Run tests. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./filestash/...`, lint, gopls gate, `just check`. Expected: PASS.
- [ ] **Step 5: Commit** (after approval)

```bash
git add filestash/ just_modules/filestash.justfile scripts/test-filestash-recipes.sh
git commit -m "feat(filestash): add consistent state backup and restore"
```

---

### Task 8: Operations recipes

**Files:**
- Modify: `just_modules/filestash.justfile`, `filestash/config.go` (`previousImage`), `scripts/test-filestash-recipes.sh`

| Recipe | Effect |
|---|---|
| `filestash upgrade --tag <t> --digest <d>` | Runs `backup-now`. Saves the current image to `previousImage` in the stack config. Sets the new image. Runs `deploy`, then `probe-public` and `probe-admin`. |
| `filestash rollback` | Sets `image` to `previousImage` and runs `deploy`. Prints the `restore-state` command for the backup that `upgrade` made, for a state format change. |
| `filestash purge-uploads --older-than <days>` | Lists `uploads` objects older than the value with `filestash-operator` keys. Dry run by default. Deletes only with `--confirm yes`. No default for `--older-than`. |
| `filestash audit-shares` | Runs a read-only pod with the sqlite image on the state PVC. Lists each share with path, flags, user list, and expiry. Fails if an upload share has no expiry or a share path is outside `/uploads/` and `/downloads/`. |
| `filestash probe-public --host <h>` | `curl` checks of the spec public table: direct object read, list denial, write denial, `uploads` denial, Range HTTP 206. |
| `filestash probe-admin --host <h>` | `curl` with `--path-as-is` for every admin variant and method from Task 1. Fails if any response holds admin content or admin JSON. |

- [ ] **Step 1: Write failing tests:**
  - `test_upgrade_runs_backup_first`: `backup-now` lines precede the `pulumi up` line.
  - `test_rollback_requires_previous_image`: empty `previousImage` gives a non-zero exit.
  - `test_purge_uploads_dry_run_default`: no `mc rm` without `--confirm yes`.
  - `test_purge_uploads_requires_age`.
  - `test_audit_shares_flags_no_expiry`: a mock row with `can_upload` and no `expire` gives a non-zero exit.
  - `test_probe_admin_uses_path_as_is`: each `curl` line has `--path-as-is`.
- [ ] **Step 2:** Run `just test-filestash`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `just test-filestash`, `just check`, Go gates. Expected: PASS.
- [ ] **Step 5: Commit** (after approval)

```bash
git add filestash/ just_modules/filestash.justfile scripts/test-filestash-recipes.sh
git commit -m "feat(filestash): add upgrade, rollback, purge, audit, and probe recipes"
```

---

### Task 9: Guide, reference documents, and diagram

Read `docs/STYLE.md` first. Write in ASD-STE100. Each guide section shows one recipe. No raw `kubectl`, `mc`, or `velero` command.

**Files:**
- Create: `docs/filestash-deploy.md`
- Create in `docs/reference/filestash/`: `install.md`, `users-and-policies.md`, `shares.md`, `publish.md`, `admin-access.md`, `backup-restore.md`, `operations.md`, `verify.md`, `troubleshooting.md`, `filestash-deploy-workflow.png`
- Modify: `docs/minio-deploy.md` Related Documents (one link)

| Guide section | Recipe |
|---|---|
| 1. Scaffold stack | `just gcp-pulumi scaffold-filestash-stack` |
| 2. Admin secret | `just filestash create-secret` |
| 3. Buckets and users | `just filestash create-buckets`, then `create-users` (two lines, one block) |
| 4. Deploy (route off) | `just filestash deploy` |
| 4a. Build the plugin bundle (before deploy) | `just filestash build-plugins` |
| 5. Admin console, SMTP, shares | `just filestash admin` |
| 6. Public bucket policy | `just filestash publish-bucket` |
| 7. Publish files | `just filestash publish` |
| 8. Remove a published file | `just filestash unpublish-file` |
| 9. Route and DNS | `just gcp-pulumi set-config` for `filestash.enabled`, `just gateway deploy`, then `just filestash probe-admin` |
| 10. Backup and restore | `just filestash backup-now` |
| 11. Upgrade and rollback | `just filestash upgrade` |
| 12. Clean up uploads and audit links | `just filestash purge-uploads`, `just filestash audit-shares` |
| 13. Teardown | `just filestash teardown` |
| 14. Verify | `just filestash verify` |
| 15. Troubleshooting | link only |
| 16. Related Documents | links only |

- [ ] **Step 1:** Write the reference documents in the shape *What It Does* → *Behavior* → *Flags* → warnings. `backup-restore.md` states that the restore needs Secret `filestash` with the same `CONFIG_SECRET`. `shares.md` states that credential rotation stops every share made with that credential, and that revocation cannot recall downloaded files. `publish.md` states that the recipe guards only its own writes, not root writes.
- [ ] **Step 2:** Write the guide from the table.
- [ ] **Step 3:** Draw the workflow diagram with every guide step in order. Save the PNG.
- [ ] **Step 4:** Make sure that each recipe `Next:` line names the next recipe in guide order.
- [ ] **Step 5:** Run `just docs docs-lint` and `just check`. Expected: zero failures.
- [ ] **Step 6: Commit** (after approval)

```bash
git add docs/filestash-deploy.md docs/reference/filestash docs/minio-deploy.md
git commit -m "docs(filestash): add deploy guide and reference documents"
```

---

### Task 10: Live rollout and release-blocker tests

**Requires explicit user authorization and every deployment blocker value.** Record each result in `docs/reference/filestash/release-tests.md`. Any failed row stops the release.

- [ ] **Step 0:** Do Steps 1 to 9 on `experiments` first (namespace `dev`, nginx Ingress). Then do them again on `dcr-kube1`. A failure on `experiments` stops the `dcr-kube1` rollout.
- [ ] **Step 1:** Run guide sections 1 to 6 with the route off. Configure SMTP through `just filestash admin`.
- [ ] **Step 2: Isolation tests** through `just filestash admin` port-forward, with test users Alice and Bob. Every row of the spec Section 11 isolation table must pass. Add: an open verified session after share deletion, and the WebDAV endpoint `/s/<id>/`.
- [ ] **Step 3: Backup and restore test, before any public route.** Run `just filestash backup-now`. Run `just filestash restore-state --backup <new backup> --target-namespace filestash-restore-test`. Confirm the shares, `config.json`, and `PRAGMA integrity_check` results there. Restore one object from each bucket from the MinIO restic snapshot. Delete the test namespace.
- [ ] **Step 4:** Run `just filestash audit-shares`. Expected: exit 0.
- [ ] **Step 5:** Set `filestash.enabled: true` and the real host with `just gcp-pulumi set-config --folder gateway`. Run `just gateway deploy`. Add the DNS `A` record.
- [ ] **Step 6:** Run `just filestash probe-admin --host <filestash-host>` from outside the cluster. On failure, set `enabled: false`, run `just gateway deploy` again, and report to the user.
- [ ] **Step 7:** Run `just filestash probe-public --host storage.dictycr.org`. Expected: exit 0.
- [ ] **Step 8: Capacity tests** from spec Section 11 against the user pass values.
- [ ] **Step 9:** Run `just filestash verify`. Expected: exit 0.
- [ ] **Step 10: Commit** (after approval)

```bash
git add docs/reference/filestash/release-tests.md gateway/Pulumi.dcr-kube1.yaml
git commit -m "chore(filestash): enable route after release tests"
```
