# Logto Deployment Details

Back to: [Logto Deploy Guide](../../logto-deploy.md)

Reference for the `logto` recipes (`just_modules/logto.justfile`), the production deployment boundary, PostgreSQL wiring, version pin, and verification checks.

## Table of Contents

- [Table of Contents](#table-of-contents)
- [What It Does](#what-it-does)
- [Recipes](#recipes)
  - [just logto check](#just-logto-check)
  - [just logto install](#just-logto-install)
  - [just logto verify](#just-logto-verify)
- [Readiness and Rollout](#readiness-and-rollout)
- [Version and Image](#version-and-image)
- [Prerequisites](#prerequisites)
- [PostgreSQL Connection](#postgresql-connection)
- [Production Configuration](#production-configuration)
- [Admin Console Access](#admin-console-access)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)
- [Official References](#official-references)

## What It Does

The `log-to/` Pulumi program creates, in this order (`log-to/main.go`):

1. PVC `logto-claim` — `ReadWriteOnce`, `storageClass` and `diskSize` from config; mounted at `/etc/logto/packages/core/connectors`
2. Deployment `logto` — pod label `app: logto`, one container `logto-container` running `npm run cli db seed -- --swe && npm run cli db alteration deploy <tag> && npm run cli connector link && npm start`, with a readiness probe on `/api/status` ([readiness and rollout](#readiness-and-rollout))
3. Service `logto-api` (port 3001) and Service `logto-admin` (port 3002) — both default type (ClusterIP), selector `app: logto`
4. Ingress `logto-ingress` — `ingressClassName: nginx`, TLS from `ingress.tlsSecret`, one rule per `ingress.backendHosts`, path `/` → **`logto-api` only**

Every resource name derives from `properties.name`. The recipes hardcode `logto`, `logto-claim`, `logto-api`, `logto-admin`, `logto-ingress`, and the `app=logto` selector, so `name: logto` is mandatory — `check` reads `.config."log-to:properties".name` and rejects any other value with `must set name logto, got '<value>'` before Pulumi is invoked. That check is what keeps a renamed deployment from reaching the rollout wait, where it would otherwise time out against a Deployment that does not exist.

The startup command is valid against official Logto **v1.43.0**: the image's root `cli` npm script maps to the `logto` CLI, which accepts the `db` and `alteration` aliases, so `npm run cli db alteration deploy <tag>` is the supported way to run migrations to a named version. The three CLI steps run in sequence and `npm start` only executes if all of them exit zero — a failed alteration leaves the container dead rather than serving an unmigrated database.

The program creates **no namespace** and **no Secret**: namespaces come from `namespace-bootstrap` ([shared namespaces](../pulumi/namespaces.md)), and the database credentials come from Secret `logto-app`, created by the PostgreSQL stack ([cluster details](../postgres/cluster.md)). Secret `logto-app` is consumed twice: as `secretKeyRef` env sources, and as a pod-level `db-secret` volume that no container mounts. The volume is redundant, but it is still part of the pod spec, so the Secret must exist in the namespace before the pod can start.

The repository ships `log-to/Pulumi.dev.yaml` and `log-to/Pulumi.experiments.yaml` only — no production stack file. Treat production installation as **configuration work first, deployment second**. Do not apply a lab stack against production.

## Readiness and Rollout

The Deployment readiness probe sends an HTTP GET to `/api/status` on port 3001. Official Logto v1.43.0 returns HTTP 204 when its core service is healthy.

The container runs database seed, alteration deployment, connector linking, and `npm start` in sequence. Kubernetes does not mark the pod Ready until the process is listening and `/api/status` succeeds. `just logto install` then waits with `kubectl rollout status`; it does not treat a merely running container as installed.

The probe uses a 10-second initial delay, 10-second period, 5-second timeout, and 12-failure threshold. `just logto verify` prints the last 100 log lines so migration failures remain visible after a successful rollout.

## Recipes

Three recipes, all `[no-cd]` and all resolving the stack through the private `_require-stack` helper: `--stack`, else `$PULUMI_STACK`, else `Error: no stack name`. There is **no `dev` fallback** — unlike `just gcp-pulumi preview/create-resource`, which default to `dev` when `PULUMI_STACK` is unset. Stack names outside `[A-Za-z0-9._-]` are rejected before they reach a shell.

### just logto check

Read-only preflight. Creates and applies nothing; safe to run at any time.

```bash
just logto check
```

Behavior:

1. Requires `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, and `PULUMI_SECRET_PROVIDER` to be non-empty, and the credentials file to exist. An empty secrets provider is the failure that leaves `ensure-stack`'s `pulumi stack init --secrets-provider ""` with an unusable stack.
2. Requires `log-to/Pulumi.<stack>.yaml` to exist, and `yq` on `PATH`.
3. Asserts on that file, in this order: `name` is exactly `logto` (every recipe and resource name derives from it); `namespace` equals `--namespace`; `databaseSecret` is exactly `logto-app`; `image.name` and `image.tag` are set and the tag is not `latest`; `endpoint`, `ingress.backendHosts[0]`, and `ingress.tlsSecret` are non-empty and contain no `<`/`>` (unreplaced template placeholders).
4. Reads `pulumi -C namespace-bootstrap stack output appNamespace` for the same stack and requires it to equal `--namespace` — the bootstrap stack is the only writer of that namespace.
5. Runs `just postgres verify --namespace <ns>` (operator, Cluster `logto`, pods, PVC, Services).
6. Requires Secret `logto-app` to carry non-empty `username` and `password` keys.
7. Requires Service `logto-rw` to expose port 5432.

A missing Secret or Service aborts on `kubectl`'s own `Error from server (NotFound)` — those two reads are not guarded.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | No `dev` fallback; charset-validated |
| `--namespace` / `-n` | No | `prod` | Applies to Logto **and** PostgreSQL; must match the `namespace-bootstrap` `appNamespace` export |

### just logto install

The one operator-facing installation command. **Applies unattended.**

```bash
just logto install
```

Behavior:

1. Rejects non-positive-integer `--retries` / `--interval`
2. `just logto check --stack <stack> --namespace <ns>`
3. `just gcp-pulumi ensure-stack --folder log-to` — selects the stack, or initializes it from `Pulumi.<stack>.yaml` with `$PULUMI_SECRET_PROVIDER`; never `pulumi stack init` on a config-less stack
4. `just gcp-pulumi preview --folder log-to`
5. `just gcp-pulumi create-resource --folder log-to` — `pulumi up -s <stack> -f -y`
6. `kubectl rollout status deployment/logto -n <ns> --timeout=<retries × interval>s`
7. `just logto verify --stack <stack> --namespace <ns>`

Step 4 prints the plan and step 5 applies it in the same run with **no prompt in between**. The preview inside `install` is a record, not a gate: for a first production apply or after a config change, run `just gcp-pulumi preview --folder log-to` on its own first and read it.

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Passed through to `check`, `ensure-stack`, `preview`, `create-resource`, `verify` |
| `--namespace` / `-n` | No | `prod` | Passed through to `check`, the rollout wait, and `verify` |
| `--retries` / `-r` | No | `60` | Positive integer |
| `--interval` / `-i` | No | `10` | Positive integer; with the default retries this is a 600s rollout budget |

The recipe is re-runnable: Pulumi reconciles, the rollout wait returns immediately when the Deployment is already current, and `verify` re-reports. Upgrades use the same command after changing `image.tag`.

### just logto verify

Post-install and standalone health check. Exits non-zero with a failure count; `PASS`/`FAIL` lines are printed without color (`CHECK_COLOR=0`) so the output pipes cleanly.

```bash
just logto verify
```

Checks:

| Check | Pass condition |
|-------|----------------|
| Deployment `logto` | `status.availableReplicas >= 1` |
| Pods `app=logto` | At least one pod `Running` with **every** container `ready`, re-read after the rollout wait |
| PVC `logto-claim` | Phase `Bound` |
| Service `logto-api` | First port is 3001 |
| Service `logto-admin` | First port is 3002 |
| Ingress `logto-ingress` | A rule host equals `ingress.backendHosts[0]` from `Pulumi.<stack>.yaml` |
| Ingress TLS | A `spec.tls[].secretName` equals `ingress.tlsSecret` from `Pulumi.<stack>.yaml` |

It then prints `kubectl logs deployment/logto --tail=100`, because a healthy rollout does not prove the seed/alteration steps succeeded — read those lines after every version change.

The two Ingress checks compare live cluster state against the **config file**, so they fail if the stack config was edited without applying, or if the host/TLS values are Pulumi-encrypted (see [production configuration](#production-configuration)). Flags are `--stack` and `--namespace`, same defaults as `check`.

## Version and Image

The latest release shown by the official Logto repository during this update is **v1.43.0**:

| Setting | Production value | Reason |
|---|---|---|
| Image | `svhd/logto` | Image name used by this repository's Pulumi program |
| Tag | `1.43.0` | Immutable pin for the current official latest release |
| API port | `3001` | Logto core API |
| Admin port | `3002` | Logto admin service |

Do not use `svhd/logto:latest`; `just logto check` rejects it. The tag is not only the image: the container runs `npm run cli db alteration deploy <tag>` with the same value, so the tag is the migration target too, and `latest` makes both unreproducible.

Upgrades: change `image.tag`, run `just gcp-pulumi preview --folder log-to`, then `just logto install`. Take a database backup first — alterations are not rolled back by a pod restart — and confirm migration completion in the pod logs that `verify` prints.

## Prerequisites

| Requirement | Check | Notes |
|---|---|---|
| Production cluster shell | `echo "$PULUMI_STACK"` | Run inside `just cluster-env --env prod --cluster <prod-cluster>`; also exports `PULUMI_SECRET_PROVIDER`, which `check` requires |
| Kubernetes access | `kubectl config current-context` | Must target the intended production cluster |
| Shared namespaces | `pulumi -C namespace-bootstrap stack output appNamespace` | Must equal `prod`; the bootstrap stack is the only writer ([details](../pulumi/namespaces.md)) |
| PostgreSQL operator and cluster | `just postgres verify` | Cluster name `logto`, namespace `prod`. Installed by [`postgres-deploy.md`](../../postgres-deploy.md), never from the Logto guide |
| PostgreSQL application Secret | `kubectl -n prod get secret logto-app` | Must carry `username` and `password` keys |
| PostgreSQL write Service | `kubectl -n prod get service logto-rw` | Must expose 5432; Logto resolves it through Kubernetes service environment variables |
| Production Logto config | `test -f log-to/Pulumi.<production-stack>.yaml` | Not present in the repository today; add and review before apply |
| `yq`, `jq`, `kubectl` | `just check-tools` | `check` and `verify` parse config and cluster state with them |

`just logto check` asserts all of the above in one run.

## PostgreSQL Connection

The Pulumi program sets these container variables (`log-to/container.go`):

| Variable | Source | Value |
|---|---|---|
| `DBUSER` | Secret `logto-app`, key `username` | PostgreSQL application role, normally `logto` |
| `PGPASSWORD` | Secret `logto-app`, key `password` | PostgreSQL application password |
| `DB_URL` | Kubernetes service discovery | `postgresql://$(DBUSER)@$(LOGTO_RW_SERVICE_HOST):$(LOGTO_RW_SERVICE_PORT)/logto?sslmode=no-verify` |
| `ENDPOINT` | Pulumi config `endpoint` | Public Logto endpoint |
| `TRUST_PROXY_HEADER` | Hardcoded in the program | `1`, for the nginx ingress in front of it |

`LOGTO_RW_SERVICE_HOST` and `LOGTO_RW_SERVICE_PORT` are the environment variables Kubernetes injects for Service `logto-rw` — they only exist for Services in the **same namespace**, and only for Services that existed before the pod started. That is why `check` pins Logto and PostgreSQL to one namespace and why a PostgreSQL-first ordering matters: a pod started before `logto-rw` exists gets an empty `DB_URL` host and must be restarted.

`sslmode=no-verify` matches the current in-cluster implementation. Do not reuse this connection string outside the cluster without reviewing TLS requirements.

## Production Configuration

Create `log-to/Pulumi.<production-stack>.yaml` in this shape. Replace every `<...>` placeholder with the reviewed production value — `check` rejects any remaining angle bracket.

```yaml
config:
  log-to:properties:
    name: logto
    namespace: prod
    databaseSecret: logto-app
    storageClass: dictycr-balanced
    diskSize: 50Gi
    endpoint: https://<logto-auth-domain>
    image:
      name: svhd/logto
      tag: 1.43.0
    apiPort: 3001
    adminPort: 3002
    ingress:
      tlsSecret: <logto-tls-secret>
      backendHosts:
        - <logto-auth-domain>
      label:
        name: kcert.dev/ingress
        value: managed
```

**Keep these values plaintext.** `check` and `verify` read the file with `yq`, not `pulumi config get`, so a Pulumi-encrypted value is a `{secure: ...}` map on disk:

| Key | Encrypted result |
|-----|------------------|
| `databaseSecret` | `check` fails: `must use databaseSecret logto-app, got '{ "secure": ... }'` |
| `endpoint`, `ingress.backendHosts[0]`, `ingress.tlsSecret` | `check` passes (the map is non-empty and bracket-free), then `verify` compares the literal `{ "secure": ... }` text against the live Ingress and fails |

The lab `experiments` stack encrypts `databaseSecret`, `backendHosts[0]`, and `tlsSecret` — do not copy that shape into production. Nothing here is a credential: the database password lives in Secret `logto-app`, written by the PostgreSQL stack. Treating a hostname as a secret costs both preflight checks and buys nothing that DNS does not already publish.

Other config rules, traced from the program and recipes:

- `name` must be `logto` — every recipe and every derived resource name assumes it, and `check` fails with `must set name logto, got '<value>'` on anything else. It is also the first config assertion, so a wrong `name` masks later config errors until it is fixed.
- `namespace` must equal the `namespace-bootstrap` `appNamespace` export (`prod` on `dcr-kube1`). The Logto stack creates no namespace.
- Do not run `pulumi stack init` by hand. `ensure-stack` initializes from this file with the cluster's KMS provider and writes `secretsprovider:` + `encryptedkey:` into it on first apply; commit those two lines, as every other production stack in this repo carries them.
- The program routes the Ingress to `logto-api` only. `logto-admin` stays an internal Service with no Ingress path. Confirm intended admin-console access (port-forward, or a separately reviewed Ingress) before production apply.

Before applying, run `just logto check`, then `just gcp-pulumi preview --folder log-to`, and read every resource change.

## Admin Console Access

The program creates `logto-admin` as an internal ClusterIP Service on port 3002. It creates no Ingress for that Service and does not set `ADMIN_ENDPOINT`.

Keep admin access internal until a public admin hostname and TLS path are reviewed. Use port-forwarding for temporary access, or add an approved Ingress and configure `ADMIN_ENDPOINT` in the application deployment before exposing the console.

## Verification

```bash
just logto verify
```

Success criteria — the recipe asserts the first five, the operator confirms the last two:

- Deployment `logto` has an available replica, and an `app=logto` pod is Running with every container ready.
- PVC `logto-claim` is `Bound`.
- `logto-api` exposes 3001 and `logto-admin` exposes 3002.
- Ingress `logto-ingress` carries the configured host and TLS Secret.
- Pod logs show `db seed`, `db alteration deploy <tag>`, and `connector link` completing before Logto starts serving.
- The production hostname answers over HTTPS with the expected certificate.

## Troubleshooting

| Symptom | Cause | Action |
|---|---|---|
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>`, or pass `--stack` |
| `Error: PULUMI_SECRET_PROVIDER is empty` | Not in the sub-shell, or the env file predates the KMS wiring | Re-enter the sub-shell; see [cluster-env](../pulumi/cluster-env.md) |
| `Error: stack config missing: log-to/Pulumi.<stack>.yaml` | Production config not written yet | Create it from [production configuration](#production-configuration) |
| `must set name logto, got '...'` | `name` renamed, missing, or stored as a Pulumi secret | Set `name: logto` in plaintext; the recipes and all resource names are hardcoded to it |
| `must use databaseSecret logto-app, got '{ "secure": ... }'` | Value stored as a Pulumi secret | Store it plaintext; the checks read the YAML with `yq` |
| `must pin a non-latest Logto image tag` | `image.tag` missing or `latest` | Pin an immutable tag, e.g. `1.43.0` |
| `contains an empty or placeholder production value` | `<...>` left in `endpoint`, `backendHosts[0]`, or `tlsSecret` | Replace with reviewed production values |
| `namespace-bootstrap exports app namespace 'x', expected 'prod'` | Wrong stack selected, or `--namespace` does not match the bootstrap | Confirm `$PULUMI_STACK`; see [shared namespaces](../pulumi/namespaces.md) |
| `Error from server (NotFound): secrets "logto-app"` | PostgreSQL not deployed in this namespace | Run `just postgres verify`; deploy via [`postgres-deploy.md`](../../postgres-deploy.md) |
| `Service logto-rw exposes '...', expected 5432` | Wrong Service, or a non-CloudNativePG object of the same name | Inspect `kubectl -n prod get svc logto-rw -o yaml` |
| `LOGTO_RW_SERVICE_HOST` empty in the pod | Pod started before Service `logto-rw` existed, or they are in different namespaces | Keep both in `prod`; `kubectl -n prod rollout restart deployment/logto` |
| Rollout wait times out after 600s | Image pull, migration failure, or unschedulable pod | `kubectl -n prod describe pod -l app=logto` and read the logs `verify` prints |
| Pod fails during database alteration | Migration failed or the database is unreachable | Read pod logs, verify `logto-app`, check PostgreSQL status before retrying |
| `Ingress host <host> missing` in `verify` | Config edited but not applied, or the value is encrypted | Re-run `just logto install`; keep ingress values plaintext |
| API works but admin console is unreachable | The program exposes an Ingress for the API Service only | Port-forward `logto-admin`, or add an approved admin exposure design; do not point the API Ingress at the admin port |
| Preview targets lab resources | Wrong Pulumi stack, or production config absent | Stop; return to the production `cluster-env` shell and inspect `pulumi stack` |

## Official References

- [Logto OSS deployment documentation](https://docs.logto.io/logto-oss/deploy)
- [Logto OSS upgrade guide](https://docs.logto.io/logto-oss/upgrading-oss-version)
- [Logto v1.43.0 release](https://github.com/logto-io/logto/releases/tag/v1.43.0)
- [Logto source repository](https://github.com/logto-io/logto)
- [PostgreSQL deployment guide in this repository](../../postgres-deploy.md)
