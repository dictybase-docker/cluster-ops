# Event Messenger Deploy

The `event-messenger/` Pulumi project deploys **event-messenger** (`dictybase/event-messenger`) into one namespace. The project creates two Kubernetes Secrets and two NATS subscriber Deployments from the same container image: `event-messenger-email` (`send-email` via Mailgun and NCBI eUtils) and `event-messenger-issue` (`gh-issue` for GitHub issue creation). Both subscribers listen on NATS subject `OrderService.Create` and query `stock-api` and `annotation-api` over gRPC.

**Both Deployments and both Secrets are always created.** Neither subscriber is optional, and the project creates no Kubernetes Service and no Ingress.

**Status**: Production procedure. Run `just gcp-pulumi scaffold-event-messenger-stack` to create a new cluster stack file from the registry and template, or `just gcp-pulumi ensure-stack --folder event-messenger` when the stack file already exists.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Verify the Upstream Services](#1-verify-the-upstream-services)
- [2. Scaffold or Select the Stack](#2-scaffold-or-select-the-stack)
- [3. Populate Email Subscriber Credentials](#3-populate-email-subscriber-credentials)
- [4. Populate GitHub Issue Subscriber Credentials](#4-populate-github-issue-subscriber-credentials)
- [5. Pin the Image Tag](#5-pin-the-image-tag)
- [6. Preview and Deploy](#6-preview-and-deploy)
- [7. Push the Stack File](#7-push-the-stack-file)
- [8. Verify the Rollout](#8-verify-the-rollout)
- [9. Enable Production Tag Deploys](#9-enable-production-tag-deploys)
- [10. Troubleshooting](#10-troubleshooting)
- [11. Related Documents](#11-related-documents)

---

## Quick Reference

For experienced users. Full details in the sections below.

```bash
# Enter the cluster environment first
just cluster-env --env <env> --cluster <cluster>

# 1. Verify NATS. Services stock-api and annotation-api must also exist.
just nats verify

# 2. Scaffold the stack file on a new cluster, or select an existing stack
just gcp-pulumi scaffold-event-messenger-stack
just gcp-pulumi ensure-stack --folder event-messenger

# 3. Populate the email subscriber credentials (omit --value for a hidden prompt)
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.domain
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.mailgunApiKey
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.sender
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.senderName
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.cc
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.ncbiApiKey

# 4. Populate the GitHub issue subscriber credentials (omit --value for a hidden prompt)
just gcp-pulumi set-secret --folder event-messenger --key properties.issueDeployment.secrets.values.owner
just gcp-pulumi set-secret --folder event-messenger --key properties.issueDeployment.secrets.values.repository
just gcp-pulumi set-secret --folder event-messenger --key properties.issueDeployment.secrets.values.token

# 5. Pin a release or develop-<sha> image tag (replace the scaffold 'bootstrap' placeholder)
just gcp-pulumi set-config --folder event-messenger --key properties.image.tag --value <immutable-tag>

# 6. Read the plan, then apply it
just gcp-pulumi preview --folder event-messenger
just gcp-pulumi create-resource --folder event-messenger

# 7. Push the stack file before each event-messenger merge or tag
git add "event-messenger/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "event-messenger: stack config" && git push

# 8. Verify both rollouts. The sub-shell sets no default namespace.
kubectl -n <namespace> rollout status deployment/event-messenger-email
kubectl -n <namespace> rollout status deployment/event-messenger-issue

# 9. Optional, one time per cluster. Let release tags deploy.
just ci set-deploy-secret --repos dictyBase/event-messenger
just ci sync-deploy-vars --repos dictyBase/event-messenger
just ci render-tag-deploy --app event-messenger --project event-messenger --out bin/ci-render/event-messenger-tag-build.yaml
```

---

**All of the commands below run in the cluster-env sub-shell.**

- Enter the sub-shell with `just cluster-env --env <env> --cluster <cluster>`. The sub-shell reads `.env.<env>.<cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Each recipe defaults `--stack` to `$PULUMI_STACK`. No command below needs the flag.
- Outside the sub-shell the recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Verify the Upstream Services

NATS and the two modware gRPC Services (`stock-api` and `annotation-api`) must exist in the namespace before the subscriber pods start. Kubernetes injects `NATS_SERVICE_HOST`, `STOCK_API_SERVICE_HOST`, and `ANNOTATION_API_SERVICE_HOST` only into pods that start after those Services exist, and the container exits with code `2` when one variable is absent.
→ [Prerequisites](reference/event-messenger/deployment.md#prerequisites) · [Service discovery](reference/event-messenger/deployment.md#in-cluster-service-discovery)

```bash
just nats verify
```

---

## 2. Scaffold or Select the Stack

On a new cluster, run `scaffold-event-messenger-stack` to initialize the stack with the cluster KMS key and render `event-messenger/Pulumi.${PULUMI_STACK}.yaml` from `config/templates/event-messenger-stack.yaml.tmpl`. When the stack file already exists, run `ensure-stack` to select the stack.
→ [Create the stack](reference/event-messenger/deployment.md#create-the-stack)

```bash
# New cluster (creates the stack and writes event-messenger/Pulumi.${PULUMI_STACK}.yaml)
just gcp-pulumi scaffold-event-messenger-stack

# Existing stack file
just gcp-pulumi ensure-stack --folder event-messenger
```

---

## 3. Populate Email Subscriber Credentials

The program creates the email Secret from `properties.emailDeployment.secrets.values.*`. The five Mailgun and sender values (`domain`, `mailgunApiKey`, `sender`, `senderName`, `cc`) are required at container startup. The three NCBI eUtils values (`ncbiApiKey`, `ncbiEmail`, `ncbiTool`) are optional, and the scaffold template pre-populates `ncbiEmail` and `ncbiTool` in plain text. Keep `secrets.name` and `secrets.keys.*` in plain text.
→ [Email subscriber configuration](reference/event-messenger/deployment.md#email-subscriber-configuration)

```bash
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.domain
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.mailgunApiKey
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.sender
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.senderName
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.cc
just gcp-pulumi set-secret --folder event-messenger --key properties.emailDeployment.secrets.values.ncbiApiKey
```

---

## 4. Populate GitHub Issue Subscriber Credentials

The program creates the issue Secret from `properties.issueDeployment.secrets.values.*`. All three GitHub values (`owner`, `repository`, `token`) are required at container startup. If you forget to set `values.*`, the Kubernetes Secret still applies with empty strings, and the pod exits at startup because `--token`, `--repository`, and `--owner` receive empty values. Keep `secrets.name` and `secrets.keys.*` in plain text.
→ [GitHub issue subscriber configuration](reference/event-messenger/deployment.md#github-issue-subscriber-configuration)

```bash
just gcp-pulumi set-secret --folder event-messenger --key properties.issueDeployment.secrets.values.owner
just gcp-pulumi set-secret --folder event-messenger --key properties.issueDeployment.secrets.values.repository
just gcp-pulumi set-secret --folder event-messenger --key properties.issueDeployment.secrets.values.token
```

---

## 5. Pin the Image Tag

The scaffold template sets `properties.image.tag` to the placeholder `bootstrap`. Pin an immutable release tag or a `develop-<sha>` tag from `dictybase/event-messenger` before you preview or deploy.
→ [Image and command wiring](reference/event-messenger/deployment.md#image-and-command-wiring)

```bash
just gcp-pulumi set-config --folder event-messenger --key properties.image.tag --value <immutable-tag>
```

---

## 6. Preview and Deploy

Read the plan, then apply it. The apply is not interactive, and it stops by itself when the plan changes a protected resource. For an upgrade, change `properties.image.tag` and run both commands again.
→ [Deploy behavior](reference/event-messenger/deployment.md#deploy-behavior)

```bash
just gcp-pulumi preview --folder event-messenger
just gcp-pulumi create-resource --folder event-messenger
```

---

## 7. Push the Stack File

**CAUTION: Push cluster-ops `develop` before you merge or tag `dictyBase/event-messenger`.** CI clones cluster-ops at `develop`, so a stack file that you did not push deploys nothing.
→ [CI deployment](reference/event-messenger/deployment.md#ci-deployment)

```bash
git add "event-messenger/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "event-messenger: stack config" && git push
```

---

## 8. Verify the Rollout

No recipe covers this project, so use `kubectl`. The sub-shell sets no default namespace, so give `-n <namespace>` with the value of `properties.namespace`.
→ [Verification](reference/event-messenger/deployment.md#verification)

```bash
kubectl -n <namespace> rollout status deployment/event-messenger-email
kubectl -n <namespace> rollout status deployment/event-messenger-issue
```

---

## 9. Enable Production Tag Deploys

**Optional. Do this one time for each cluster. Skip it while production deploys stay manual.**

These three recipes give `dictyBase/event-messenger` access to `PROD_DEPLOY_SA_KEY`, publish the `<CI_ENV>_*` repository variables, and render a tag workflow file. The rendered file does nothing until you add it to the service repository with a pull request.
→ [Tag deploy setup](reference/event-messenger/deployment.md#tag-deploy-setup)

```bash
just ci set-deploy-secret --repos dictyBase/event-messenger
just ci sync-deploy-vars --repos dictyBase/event-messenger
just ci render-tag-deploy --app event-messenger --project event-messenger --out bin/ci-render/event-messenger-tag-build.yaml
```

---

## 10. Troubleshooting

→ [Troubleshooting table](reference/event-messenger/deployment.md#troubleshooting)

---

## 11. Related Documents

**Reference details for this guide:**
- [Event Messenger deployment details](reference/event-messenger/deployment.md)

**Other documents:**
- NATS server: [`nats-deploy.md`](nats-deploy.md)
- gRPC backends (`stock-api`, `annotation-api`, `order-api`): [`backend-service-deploy.md`](backend-service-deploy.md)
- Shared namespaces: [`reference/pulumi/namespaces.md`](reference/pulumi/namespaces.md)
