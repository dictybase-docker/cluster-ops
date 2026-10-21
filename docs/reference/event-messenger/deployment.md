# Event Messenger Deployment Details

Back to: [Event Messenger Deploy Guide](../../event-messenger-deploy.md)

This document gives the details for the `event-messenger/` Pulumi project: the resources, the prerequisites, the in-cluster service discovery, the configuration keys, the email and GitHub issue subscriber credentials, the CI path, and the error messages.

## Table of Contents

- [What the Project Creates](#what-the-project-creates)
- [Prerequisites](#prerequisites)
- [In-Cluster Service Discovery](#in-cluster-service-discovery)
- [Create the Stack](#create-the-stack)
- [Email Subscriber Configuration](#email-subscriber-configuration)
- [GitHub Issue Subscriber Configuration](#github-issue-subscriber-configuration)
- [Image and Command Wiring](#image-and-command-wiring)
- [Deploy Behavior](#deploy-behavior)
- [CI Deployment](#ci-deployment)
- [Tag Deploy Setup](#tag-deploy-setup)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

---

## What the Project Creates

The program creates four resources in the namespace in a fixed order: the email Secret, the email Deployment, the issue Secret, and the issue Deployment. Both Deployments run the same `dictybase/event-messenger` container image with different subcommands.

| Order | Resource | Default Name | Details |
|-------|----------|--------------|---------|
| 1 | Secret | `email-event` (`emailevent` on `experiments`) | Holds the Mailgun and NCBI eUtils values from `properties.emailDeployment.secrets.values.*`. |
| 2 | Deployment | `event-messenger-email` | Pod label `app: event-messenger-email`. One container `event-messenger-email` that runs subcommand `send-email`. Depends on the email Secret (`3m` create timeout). |
| 3 | Secret | `issue-event` (`issueevent` on `experiments`) | Holds the GitHub owner, repository, and token values from `properties.issueDeployment.secrets.values.*`. |
| 4 | Deployment | `event-messenger-issue` | Pod label `app: event-messenger-issue`. One container `event-messenger-issue` that runs subcommand `gh-issue`. Depends on the issue Secret (`3m` create timeout). |

The program creates **no Namespace**, **no Service**, and **no Ingress**. Both containers act as NATS subscribers and outbound clients only. The `namespace-bootstrap` project creates the namespace ([shared namespaces](../pulumi/namespaces.md)). Unlike `graphql_server` and the `modware-*` projects, which read pre-existing Kubernetes Secrets created by datastore stacks, `event-messenger` creates its own two Kubernetes Secrets directly from the stack configuration.

---

## Prerequisites

Each upstream Service must exist in the target namespace before the two subscriber pods start.

| Component | Resource in the namespace | Purpose | Guide |
|-----------|---------------------------|---------|-------|
| Namespace | `dev` or `prod` | Target namespace | [`pulumi-setup.md`](../../pulumi-setup.md) |
| NATS | Service `nats` (`:4222`) | Publishes `OrderService.Create` events from `modware-order` | [`nats-deploy.md`](../../nats-deploy.md) |
| modware gRPC | Services `stock-api` (`:9345`) and `annotation-api` (`:9250`) | Stock, strain, plasmid, and annotation metadata lookup for order emails and GitHub issues | [`backend-service-deploy.md`](../../backend-service-deploy.md) |
| Outbound HTTPS | External network access | Reaches Mailgun (`api.mailgun.net`), NCBI eUtils (`eutils.ncbi.nlm.nih.gov`), and the GitHub REST API (`api.github.com`) | — |

`event-messenger` does not dial `order-api` directly, but `modware-order` (`order-api-server`) is the publisher of the `OrderService.Create` messages on NATS.

### Check the Prerequisites

NATS has a verify recipe. The two gRPC Services have none, so query them with `kubectl`.

```bash
just nats verify
kubectl -n <namespace> get svc stock-api annotation-api
```

`just nats verify` defaults to `--namespace prod`. On the lab `experiments` cluster, pass `--namespace dev`. Deploy a missing gRPC Service with `just ci deploy-backend-services --services stock,annotation` ([`backend-service-deploy.md`](../../backend-service-deploy.md)).

---

## In-Cluster Service Discovery

Pulumi passes no NATS or gRPC host and port arguments to the containers. Both `send-email` and `gh-issue` read the standard Kubernetes service-discovery environment variables instead.

| Service | Environment variables the binary reads | Flag that gets the value |
|---------|---------------------------------------|--------------------------|
| `nats` | `NATS_SERVICE_HOST` / `NATS_SERVICE_PORT` | `--nats-host` / `--nats-port` |
| `stock-api` | `STOCK_API_SERVICE_HOST` / `STOCK_API_SERVICE_PORT` | `--stock-grpc-host` / `--stock-grpc-port` |
| `annotation-api` | `ANNOTATION_API_SERVICE_HOST` / `ANNOTATION_API_SERVICE_PORT` | `--annotation-grpc-host` / `--annotation-grpc-port` |

Kubernetes writes these variables into a pod only when the Service exists in the same namespace before the pod starts. If `nats`, `stock-api`, or `annotation-api` starts after `event-messenger`, the pod exits with code `2` and logs `Required flags "..." not set`. Restart both Deployments after the missing Service exists.

```bash
kubectl -n <namespace> rollout restart deployment/event-messenger-email deployment/event-messenger-issue
```

---

## Create the Stack

Run `scaffold-event-messenger-stack` to create a new cluster stack file from `config/templates/event-messenger-stack.yaml.tmpl` and the cluster registry entry (`config/clusters/<cluster>.yaml`).

```bash
just gcp-pulumi scaffold-event-messenger-stack
```

The recipe performs these actions:

1. Reads `namespace` and `kms_secrets_provider` from the registry entry matching `--stack` (`$PULUMI_STACK`).
2. Refuses to overwrite an existing `event-messenger/Pulumi.<stack>.yaml`.
3. Initializes the stack with `pulumi stack init --secrets-provider` and appends the rendered `config:` block (`email-event`, `issue-event`, `OrderService.Create`, `logLevel: error`).

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | No | `$PULUMI_STACK` | Must match a `stack:` key in `config/clusters/*.yaml` |
| `--nats-subject` | No | `OrderService.Create` | NATS subject passed to `--subject` on both Deployments |
| `--ncbi-email` | No | `siddhartha-basu@northwestern.edu` | Plaintext default written to `emailDeployment.secrets.values.ncbiEmail` |
| `--ncbi-tool` | No | `eventmessenger` | Plaintext default written to `emailDeployment.secrets.values.ncbiTool` |

When `event-messenger/Pulumi.<stack>.yaml` already exists, select the stack with:

```bash
just gcp-pulumi ensure-stack --folder event-messenger
```

### Baseline Stack File

The scaffold template sets the Kubernetes Secret names, key names, NATS subject, and the two non-secret NCBI values (`ncbiEmail` and `ncbiTool`). Populate the secret `values.*` keys with `just gcp-pulumi set-secret` and replace `image.tag: bootstrap` before you deploy.

```yaml
config:
  event-messenger:properties:
    emailDeployment:
      name: event-messenger-email
      secrets:
        keys:
          cc: eventMessenger.email.cc
          domain: eventMessenger.email.domain
          mailgunApiKey: eventMessenger.email.apiKey
          ncbiApiKey: eventMessenger.email.ncbiApiKey
          ncbiEmail: eventMessenger.email.ncbiEmail
          ncbiTool: eventMessenger.email.ncbiTool
          sender: eventMessenger.email.sender
          senderName: eventMessenger.email.senderName
        name: email-event
        values:
          ncbiEmail: siddhartha-basu@northwestern.edu
          ncbiTool: eventmessenger
    image:
      name: dictybase/event-messenger
      tag: <immutable-tag>
    issueDeployment:
      name: event-messenger-issue
      secrets:
        keys:
          owner: eventMessenger.github.owner
          repository: eventMessenger.github.repository
          token: eventMessenger.github.token
        name: issue-event
    logLevel: error
    namespace: prod
    nats:
      subject: OrderService.Create
```

For the full shape with encrypted `values.*` blocks, read `event-messenger/Pulumi.experiments.yaml`.

### Keys That Change per Cluster

| Key | Lab (`experiments`) | Lab (`dev` template) | Production |
|-----|---------------------|----------------------|------------|
| `namespace` | `dev` | `dev` | `prod` |
| `logLevel` | `debug` | `debug` | `error` |
| `emailDeployment.secrets.name` | `emailevent` (no hyphen) | `email-event` | `email-event` |
| `issueDeployment.secrets.name` | `issueevent` (no hyphen) | `issue-event` | `issue-event` |
| `emailDeployment.secrets.values.*` | Sandbox Mailgun domain and test recipients | Sandbox values | Production Mailgun domain, sender, CC address, and NCBI API key |
| `issueDeployment.secrets.values.*` | `dictybase-playground` / `learn-github-action` + PAT | **Absent** (incomplete) | Production GitHub owner, repository, and PAT |
| `image.tag` | the last tag from CI (`develop-<sha>`) | `develop-0217a41` | an immutable `develop-<sha>` tag or a release tag |

**Warning on `Pulumi.dev.yaml` vs `Pulumi.experiments.yaml`**:
- The two lab files name the Kubernetes Secrets differently: `experiments` uses `emailevent` and `issueevent` (no hyphen), while `dev` and the scaffold template use `email-event` and `issue-event`. When you inspect a Secret with `kubectl`, check `secrets.name` in `Pulumi.<stack>.yaml` first.
- `Pulumi.dev.yaml` omits `issueDeployment.secrets.values` completely and omits `ncbiApiKey`, `ncbiEmail`, and `ncbiTool` under `emailDeployment.secrets.values`. **Never copy `Pulumi.dev.yaml` with `fork-stack` or `new-stack-from` without populating `issueDeployment.secrets.values.*`.** Use `just gcp-pulumi scaffold-event-messenger-stack` instead.

---

## Email Subscriber Configuration

Deployment `event-messenger-email` runs subcommand `send-email`. The program creates the Kubernetes Secret named in `properties.emailDeployment.secrets.name` and injects eight environment variables into the container via `secretKeyRef`.

| Container env var | Config key for the Secret key (`keys.*`) | Config key for the Secret value (`values.*`) | Required at startup | CLI flag fed by env / argv |
|-------------------|------------------------------------------|----------------------------------------------|---------------------|----------------------------|
| `EMAIL_DOMAIN` | `emailDeployment.secrets.keys.domain` | `emailDeployment.secrets.values.domain` | Yes (`optional: false`) | `--domain $(EMAIL_DOMAIN)` |
| `EMAIL_SENDER_NAME` | `emailDeployment.secrets.keys.senderName` | `emailDeployment.secrets.values.senderName` | Yes (`optional: false`) | `--name $(EMAIL_SENDER_NAME)` |
| `EMAIL_SENDER` | `emailDeployment.secrets.keys.sender` | `emailDeployment.secrets.values.sender` | Yes (`optional: false`) | `--sender $(EMAIL_SENDER)` |
| `EMAIL_CC` | `emailDeployment.secrets.keys.cc` | `emailDeployment.secrets.values.cc` | Yes (`optional: false`) | `--cc $(EMAIL_CC)` |
| `MAILGUN_API_KEY` | `emailDeployment.secrets.keys.mailgunApiKey` | `emailDeployment.secrets.values.mailgunApiKey` | Yes (`optional: false`) | `--apiKey $(MAILGUN_API_KEY)` |
| `NCBI_API_KEY` | `emailDeployment.secrets.keys.ncbiApiKey` | `emailDeployment.secrets.values.ncbiApiKey` | No (`optional: true`) | `NCBI_API_KEY` (`--ncbi-api-key`) |
| `NCBI_TOOL` | `emailDeployment.secrets.keys.ncbiTool` | `emailDeployment.secrets.values.ncbiTool` | No (`optional: true`) | `NCBI_TOOL` (`--ncbi-tool`) |
| `NCBI_EMAIL` | `emailDeployment.secrets.keys.ncbiEmail` | `emailDeployment.secrets.values.ncbiEmail` | No (`optional: true`) | `NCBI_EMAIL` (`--ncbi-email`) |

### Secret Pointers vs Secret Values

- **Keep `secrets.name` and `secrets.keys.*` in plain text.** They are the Kubernetes Secret name and data-key names referenced by `secretKeyRef`.
- **Encrypt `secrets.values.*` with `just gcp-pulumi set-secret`** for `domain`, `mailgunApiKey`, `sender`, `senderName`, `cc`, and `ncbiApiKey`. Omit `--value` to enter the secret at a hidden prompt so it stays out of shell history.
- **`ncbiEmail` and `ncbiTool` can stay in plain text** (`set-config`), because they are standard NCBI eUtils client identification headers, not credentials.
- **Notice the NCBI delivery difference:** The program passes the five Mailgun and sender variables on the command line via Kubernetes `$(VAR)` expansion (`--domain $(EMAIL_DOMAIN)`, and so on), but it does **not** pass `--ncbi-api-key`, `--ncbi-tool`, or `--ncbi-email` in `args`. The `event-messenger` binary reads those three values directly from the container environment variables `NCBI_API_KEY`, `NCBI_TOOL`, and `NCBI_EMAIL`.

---

## GitHub Issue Subscriber Configuration

Deployment `event-messenger-issue` runs subcommand `gh-issue`. The program creates the Kubernetes Secret named in `properties.issueDeployment.secrets.name` and injects three environment variables into the container via `secretKeyRef` (all with `optional` unset, so required).

| Container env var | Config key for the Secret key (`keys.*`) | Config key for the Secret value (`values.*`) | Required at startup | CLI flag fed by argv |
|-------------------|------------------------------------------|----------------------------------------------|---------------------|----------------------|
| `GITHUB_OWNER` | `issueDeployment.secrets.keys.owner` | `issueDeployment.secrets.values.owner` | Yes | `--owner $(GITHUB_OWNER)` |
| `GITHUB_REPOSITORY` | `issueDeployment.secrets.keys.repository` | `issueDeployment.secrets.values.repository` | Yes | `--repository $(GITHUB_REPOSITORY)` |
| `GITHUB_TOKEN` | `issueDeployment.secrets.keys.token` | `issueDeployment.secrets.values.token` | Yes | `--token $(GITHUB_TOKEN)` |

### Empty-Values Pitfall and Token Rotation

The Pulumi program does **not** validate that `secrets.values.*` are non-empty before creating the Kubernetes Secret. It writes `StringData` with whatever strings are in the stack config:

- If `issueDeployment.secrets.values` is omitted (as in `Pulumi.dev.yaml`), Kubernetes still creates the Secret with three keys mapped to `""` (empty string).
- `SecretKeyRef` succeeds because the keys exist in the Secret, and Kubernetes expands `$(GITHUB_TOKEN)`, `$(GITHUB_REPOSITORY)`, and `$(GITHUB_OWNER)` to `""` in the container arguments.
- At startup, `urfave/cli` v3 rejects the empty required flags with `Required flags "token, repository, owner" not set` and exits with code `2`, putting the pod into `CrashLoopBackOff`.

When you rotate the GitHub personal access token (`properties.issueDeployment.secrets.values.token`) or Mailgun API key (`properties.emailDeployment.secrets.values.mailgunApiKey`):

1. Update the value with `just gcp-pulumi set-secret --folder event-messenger --key <path>`.
2. Run `just gcp-pulumi preview --folder event-messenger` and `just gcp-pulumi create-resource --folder event-messenger` to update the Kubernetes Secret in place.
3. **Restart the Deployment.** Kubernetes updates the Secret object, but the running pod reads environment variables only at container start. Run `kubectl -n <namespace> rollout restart deployment/event-messenger-issue` (or `deployment/event-messenger-email`) so a new pod starts with the new Secret value.

---

## Image and Command Wiring

Both Deployments pull `<image.name>:<image.tag>` (`dictybase/event-messenger:<tag>`). The `event-messenger` Dockerfile installs `wkhtmltopdf` alongside the `/usr/local/bin/app` binary to render order PDF attachments, and links `/etc/localtime` to `America/Chicago`.

### Global Flag Order (`--log-level` before the Subcommand)

The binary uses `urfave/cli` v3, where `--log-level` is a root flag and `send-email` / `gh-issue` are subcommands. The Pulumi program passes `--log-level <level>` **before** the subcommand name:

- Email container arguments:
  ```text
  --log-level <logLevel> send-email --subject <nats.subject> --domain $(EMAIL_DOMAIN) --apiKey $(MAILGUN_API_KEY) --name $(EMAIL_SENDER_NAME) --sender $(EMAIL_SENDER) --cc $(EMAIL_CC)
  ```
- Issue container arguments:
  ```text
  --log-level <logLevel> gh-issue --subject <nats.subject> --token $(GITHUB_TOKEN) --repository $(GITHUB_REPOSITORY) --owner $(GITHUB_OWNER)
  ```

### Asymmetry in `image.pullPolicy`

- `event-messenger-issue` sets `imagePullPolicy` from `properties.image.pullPolicy`. When `image.pullPolicy` is omitted in `Pulumi.<stack>.yaml`, Kubernetes applies its default (`IfNotPresent` for non-`:latest` tags).
- `event-messenger-email` does **not** wire `image.pullPolicy` in `email.go` — Kubernetes always uses the default pull policy for that container regardless of `properties.image.pullPolicy`.

### Hardcoded Pricing Flags

Both `send-email` and `gh-issue` accept `--strain-price` (default `30`) and `--plasmid-price` (default `15`). The Pulumi program passes neither flag, so both subscribers always use the binary defaults unless the Go program is modified.

---

## Deploy Behavior

`create-resource` drives the Pulumi Automation API, not the interactive CLI. It asks for no confirmation, and it hides secret values in the logs and in the error text. The recipe previews the change first, and then it does one of three things:

- It stops and lists the URNs when the plan changes a protected resource.
- It makes no update when the plan has no change.
- It applies the saved plan from that preview, so the applied plan is the plan that you read.

Each Deployment resource sets a Pulumi custom create timeout of `3 minutes`, and depends on its corresponding Secret (`pulumi.DependsOn`). No recipe waits for the rollout from the shell. Run the commands in [Verification](#verification) yourself.

### Recipe Flags

| Recipe | Flag | Required | Default | Notes |
|--------|------|----------|---------|-------|
| all | `--folder` / `-f` | Yes | — | Pass `event-messenger` |
| all | `--stack` / `-s` | No | `$PULUMI_STACK` | Every recipe except `ensure-stack` falls back to the literal `dev` outside a cluster shell. `ensure-stack` fails instead. |
| `ensure-stack` | `--stack` / `-s` | No | `$PULUMI_STACK` | Creates the stack when `Pulumi.<stack>.yaml` exists. Otherwise it only selects a stack that exists, and it fails when there is none. |
| `set-config` / `set-secret` | `--key` / `-k` | Yes | — | Path under `event-messenger:`, for example `properties.image.tag` |
| `set-config` / `set-secret` | `--value` / `-v` | Yes for `set-config`, no for `set-secret` | hidden prompt on `set-secret` | Omit it on `set-secret` to keep the value out of the shell history |
| `set-config` | `--plaintext` | No | unset | Accepted but unused. `set-config` always stores the value plain. |

---

## CI Deployment

A push to `develop` in `dictyBase/event-messenger` starts `.github/workflows/ci.yml`. That workflow runs the unit tests and then calls the composite deploy workflow with `app: event-messenger`, `project: event-messenger`, `stack: experiments`, and `docker_image: event-messenger`. The composite workflow builds `dictybase/event-messenger:<ref>-<sha>`, clones cluster-ops at branch **`develop`**, sets `properties.image.tag`, and runs `pulumi up`.

**Push order: send each change to `event-messenger/*.go` or `event-messenger/Pulumi.<stack>.yaml` to cluster-ops `develop` before you merge or tag `dictyBase/event-messenger`.** Otherwise the CI run clones the old tree and deploys stale config.

---

## Tag Deploy Setup

The CI path above deploys the lab `experiments` stack only. Production rollouts stay manual until the service repository carries a tag workflow. `event-messenger` has no entry in `config/services.yaml`, so `just ci deploy-backend-services` and `just ci pr-deploy-workflows` exit with `no services matched` for this repository. Use the three single-repository recipes from the production cluster-env sub-shell.

```bash
just ci set-deploy-secret --repos dictyBase/event-messenger
just ci sync-deploy-vars --repos dictyBase/event-messenger
just ci render-tag-deploy --app event-messenger --project event-messenger --out bin/ci-render/event-messenger-tag-build.yaml
```

| Recipe | What it changes | Risk | Details |
|--------|-----------------|------|---------|
| `set-deploy-secret` | Adds `event-messenger` to the repository list of the org secret `PROD_DEPLOY_SA_KEY` and uploads `credentials/<cluster>/deployer.json` | The recipe reads the current list and sends it again, so one run never removes the modware repositories | [CI variables](../backend/ci-variables.md#set-deploy-secret) |
| `sync-deploy-vars` | Sets the five `PROD_*` repository variables from the registry entry | One upsert per variable. It warns, and it does not fail, when the repository cannot see `PROD_DEPLOY_SA_KEY`. | [CI variables](../backend/ci-variables.md#sync-deploy-vars) |
| `render-tag-deploy` | Writes `bin/ci-render/event-messenger-tag-build.yaml` in cluster-ops only | None until you merge the pull request below | [workflow render](../backend/render-deploy-workflows.md) |

To finish the setup, open a pull request against `dictyBase/event-messenger`:

1. Copy `bin/ci-render/event-messenger-tag-build.yaml` to `.github/workflows/tag-build.yaml`.
2. Delete `.github/workflows/staging-build.yaml` in the same pull request. There is no staging environment.
3. Do not change `.github/workflows/ci.yml`. It keeps the develop path on the `experiments` stack.

Two conditions must hold before a tag deploys, and these recipes check neither one. First, the composite workflow must select `secrets.PROD_DEPLOY_SA_KEY` and `vars.PROD_PULUMI_STATE_STORAGE` for `inputs.environment == 'production'`. Second, `event-messenger/Pulumi.<prod-stack>.yaml` must exist on cluster-ops `develop`.

---

## Verification

The cluster-env sub-shell exports `KUBECONFIG`, but it sets no default namespace. Give `-n <namespace>` to each command: `prod` on production, `dev` on the lab `experiments` stack.

```bash
# 1. Rollouts and images. Both tags must match properties.image.tag.
kubectl -n <namespace> rollout status deployment/event-messenger-email
kubectl -n <namespace> rollout status deployment/event-messenger-issue
kubectl -n <namespace> get deploy event-messenger-email event-messenger-issue \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.template.spec.containers[0].image}{"\n"}{end}'

# 2. Startup logs. Each subscriber logs its NATS subscription readiness.
kubectl -n <namespace> logs deployment/event-messenger-email --tail=20
kubectl -n <namespace> logs deployment/event-messenger-issue --tail=20

# 3. Secrets. Confirm the expected keys exist (never print secret values).
kubectl -n <namespace> get secret <email-secret-name> -o jsonpath='{.data}' | jq 'keys'
kubectl -n <namespace> get secret <issue-secret-name> -o jsonpath='{.data}' | jq 'keys'
```

- The email subscriber logs `starting the email sending subscriber backend`.
- The issue subscriber logs `starting the Github issue creation subscriber messaging backend`.
- On stack `experiments`, the Secret names are `emailevent` and `issueevent`. On stacks created by `scaffold-event-messenger-stack`, the Secret names are `email-event` and `issue-event`.

---

## Troubleshooting

| Symptom | Cause | Action |
|---------|-------|--------|
| `ERROR: stack '<stack>' does not exist in event-messenger, and there is no Pulumi.<stack>.yaml to initialize it from` | First deploy on this cluster. `ensure-stack` creates a stack only from a stack file that exists. | Run `just gcp-pulumi scaffold-event-messenger-stack` ([create the stack](#create-the-stack)). |
| Pod `CrashLoopBackOff` with `Required flags "nats-host, nats-port" not set` or `Required flags "stock-grpc-host, ..." not set` | Service `nats`, `stock-api`, or `annotation-api` did not exist in the namespace when the pod started. | Verify NATS (`just nats verify`) and the gRPC Services, then run `kubectl -n <namespace> rollout restart deployment/event-messenger-email deployment/event-messenger-issue`. |
| Pod `CrashLoopBackOff` with `Required flags "token, repository, owner" not set` | `issueDeployment.secrets.values.*` are missing or empty in `Pulumi.<stack>.yaml` (for example, copied from `Pulumi.dev.yaml`). | Set `owner`, `repository`, and `token` with `just gcp-pulumi set-secret`, apply the stack, and restart `deployment/event-messenger-issue` ([GitHub issue subscriber configuration](#github-issue-subscriber-configuration)). |
| Pod `CrashLoopBackOff` with `Required flags "domain, apiKey, name, sender, cc" not set` | One or more `emailDeployment.secrets.values.*` keys are missing or empty in `Pulumi.<stack>.yaml`. | Set the five Mailgun and sender values with `just gcp-pulumi set-secret`, apply the stack, and restart `deployment/event-messenger-email` ([email subscriber configuration](#email-subscriber-configuration)). |
| Pod `CreateContainerConfigError` on Secret `email-event` (`emailevent`) or `issue-event` (`issueevent`) | A `secrets.keys.*` entry in `Pulumi.<stack>.yaml` is empty or does not match the key written to the Secret. | Restore the `secrets.keys.*` map from the [baseline stack file](#baseline-stack-file) in plain text and re-apply. |
| Issue pod logs `401 Bad credentials` from GitHub when an order arrives | The GitHub PAT in `issueDeployment.secrets.values.token` is expired or revoked, or the pod is still running with an old Secret value after rotation. | Verify whether the pod environment matches the Secret (`kubectl -n <namespace> get secret <issue-secret-name> -o jsonpath='{.data}' \| jq 'keys'`), update `values.token` with `just gcp-pulumi set-secret`, run `create-resource`, and run `kubectl -n <namespace> rollout restart deployment/event-messenger-issue`. |
| Email pod logs a Mailgun `401` or domain error when an order arrives | `mailgunApiKey` or `domain` is wrong for the target Mailgun region/domain, or the pod was not restarted after rotating the Secret. | Update `emailDeployment.secrets.values.mailgunApiKey` and `domain` with `just gcp-pulumi set-secret`, run `create-resource`, and restart `deployment/event-messenger-email`. |
| Both pods are `Running` with no errors, but new orders trigger no email or GitHub issue | `properties.nats.subject` does not match the subject published by `modware-order` (`OrderService.Create`), or `order-api-server` is not publishing to NATS. | Check `properties.nats.subject` in `Pulumi.<stack>.yaml` and check the logs of `deployment/order-api-server`. |
| A CI deploy ignores a recent cluster-ops change | The cluster-ops change is local, or it is on an unmerged branch. CI always clones `cluster-ops@develop`. | Merge and push cluster-ops `develop`, then run the workflow again. |
