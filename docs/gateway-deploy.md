# Gateway Deploy

The `gateway/` Pulumi project deploys the shared **Cilium Gateway** (`dictycr-gateway`), the **cert-manager** ACME `Issuer` (`letsencrypt-issuer`), and the `HTTPRoute` resources for `frontend_application`, `graphql_server`, `minio`, and `log-to` into one namespace.

**Each service route has its own `enabled` toggle.** By default, the scaffold template enables `frontend`, `graphql`, and `minio`, and disables `logto` ([Section 7](#7-configure-the-logto-route-optional)). Legacy `nginx` Ingress resources remain on `dcr-experiments` only.

**Status**: Production procedure. Run `just gcp-pulumi scaffold-gateway-stack` to create a new cluster stack file from the registry and template, or `just gcp-pulumi ensure-stack --folder gateway` when the stack file already exists.

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Install Gateway API CRDs](#1-install-gateway-api-crds)
- [2. Scaffold or Select the Stack](#2-scaffold-or-select-the-stack)
- [3. Configure the ACME Issuer](#3-configure-the-acme-issuer)
- [4. Configure the Frontend Route](#4-configure-the-frontend-route)
- [5. Configure the GraphQL Route](#5-configure-the-graphql-route)
- [6. Configure the MinIO Route](#6-configure-the-minio-route)
- [7. Configure the Logto Route (Optional)](#7-configure-the-logto-route-optional)
- [8. Preview and Deploy](#8-preview-and-deploy)
- [9. Push the Stack File](#9-push-the-stack-file)
- [10. Verify the Gateway](#10-verify-the-gateway)
- [11. Troubleshooting](#11-troubleshooting)
- [12. Related Documents](#12-related-documents)

---

## Quick Reference

For experienced users. Full details in the sections below.

```bash
# Enter the cluster environment first
just cluster-env --env <env> --cluster <cluster>

# 1. Install Gateway API standard CRDs and enable Gateway API on cert-manager
just gateway install-crds

# 2. Scaffold the stack file on a new cluster, or select an existing stack
just gcp-pulumi scaffold-gateway-stack
just gcp-pulumi ensure-stack --folder gateway

# 3. Set the Let's Encrypt ACME contact email
just gcp-pulumi set-config --folder gateway --key properties.issuer.email --value <acme-email>

# 4. Configure the frontend route (default: enabled)
just gcp-pulumi set-config --folder gateway --key properties.routes.frontend.hosts[0] --value <frontend-host>

# 5. Configure the GraphQL route (default: enabled)
just gcp-pulumi set-config --folder gateway --key properties.routes.graphql.hosts[0] --value <graphql-host>

# 6. Configure the MinIO route (default: enabled)
just gcp-pulumi set-config --folder gateway --key properties.routes.minio.hosts[0] --value <minio-host>

# 7. Optional. Enable and configure the Logto route (default: false)
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.hosts[0] --value <logto-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.enabled --value true

# 8. Check prerequisites, preview, deploy, and verify
just gateway deploy

# 9. Commit and push the stack file
git add "gateway/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "gateway: stack config" && git push

# 10. Re-verify at any time
just gateway verify
```

---

**All of the commands below run in the cluster-env sub-shell.**

- Enter the sub-shell with `just cluster-env --env <env> --cluster <cluster>`. The sub-shell reads `.env.<env>.<cluster>` and exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PULUMI_SECRET_PROVIDER`, `PROJECT_ID`, and `KUBECONFIG`.
- Each recipe defaults `--stack` to `$PULUMI_STACK` and `--namespace` to `prod`. No command below needs those flags.
- Outside the sub-shell the recipes fail with `Error: no stack name`.
- Leave the sub-shell with `exit` or Ctrl-D.

## 1. Install Gateway API CRDs

Apply the Kubernetes Gateway API standard CRDs and enable `--enable-gateway-api` on `cert-manager`. If you just added `spec.networking.cilium.gatewayAPI.enabled: true` to `config/kops/<cluster>/cluster.yaml`, run this command before you run `just gcp-cluster apply-cluster` so Cilium sees the CRDs at startup.
→ [Prerequisites](reference/gateway/deployment.md#prerequisites) · [`just gateway install-crds`](reference/gateway/deployment.md#just-gateway-install-crds)

```bash
just gateway install-crds
```

---

## 2. Scaffold or Select the Stack

On a new cluster, run `scaffold-gateway-stack` to initialize the stack with the cluster KMS key and render `gateway/Pulumi.${PULUMI_STACK}.yaml` from `config/templates/gateway-stack.yaml.tmpl`. When the stack file already exists, run `ensure-stack` to select the stack.
→ [Create the stack](reference/gateway/deployment.md#create-the-stack)

```bash
# New cluster (creates the stack and writes gateway/Pulumi.${PULUMI_STACK}.yaml)
just gcp-pulumi scaffold-gateway-stack

# Existing stack file
just gcp-pulumi ensure-stack --folder gateway
```

---

## 3. Configure the ACME Issuer

Set the contact email for the Let's Encrypt ACME `Issuer` (`letsencrypt-issuer`). `just gateway check` rejects an empty value or the `<acme-email>` placeholder.
→ [Configure the ACME Issuer](reference/gateway/deployment.md#configure-the-acme-issuer)

```bash
just gcp-pulumi set-config --folder gateway --key properties.issuer.email --value <acme-email>
```

---

## 4. Configure the Frontend Route

The key `properties.routes.frontend.enabled` defaults to `true`. Set the public hostname for the four frontend Services (`frontpage-api`, `stockcenter-api`, `publication-api`, `genomepage-api`), or set `enabled` to `false` if `frontend_application` is not deployed yet.
→ [Frontend route](reference/gateway/deployment.md#frontend-route)

```bash
# Set the public hostname (route is enabled by default)
just gcp-pulumi set-config --folder gateway --key properties.routes.frontend.hosts[0] --value <frontend-host>

# Or disable the route until frontend_application is deployed
just gcp-pulumi set-config --folder gateway --key properties.routes.frontend.enabled --value false
```

---

## 5. Configure the GraphQL Route

The key `properties.routes.graphql.enabled` defaults to `true`. Set the public hostname for Service `graphql-api` (`:8080`), or set `enabled` to `false` if `graphql_server` is not deployed yet.
→ [GraphQL route](reference/gateway/deployment.md#graphql-route)

```bash
# Set the public hostname (route is enabled by default)
just gcp-pulumi set-config --folder gateway --key properties.routes.graphql.hosts[0] --value <graphql-host>

# Or disable the route until graphql_server is deployed
just gcp-pulumi set-config --folder gateway --key properties.routes.graphql.enabled --value false
```

---

## 6. Configure the MinIO Route

The key `properties.routes.minio.enabled` defaults to `true`. Set the public S3 hostname for Service `minio` (`:9000`), or set `enabled` to `false` to keep MinIO internal only.
→ [MinIO route](reference/gateway/deployment.md#minio-route)

```bash
# Set the public hostname (route is enabled by default)
just gcp-pulumi set-config --folder gateway --key properties.routes.minio.hosts[0] --value <minio-host>

# Or disable the route to keep MinIO cluster-internal
just gcp-pulumi set-config --folder gateway --key properties.routes.minio.enabled --value false
```

---

## 7. Configure the Logto Route (Optional)

**Optional. The default is off. Skip this section while Logto is not deployed.**

The key `properties.routes.logto.enabled` defaults to `false`. To expose `logto-api` (`:3001`) through the Gateway after you deploy Logto, set `hosts[0]` first and then set `enabled` to `true`.
→ [Logto route](reference/gateway/deployment.md#logto-route)

```bash
# Keep the Logto route off (default)
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.enabled --value false

# Turn the Logto route on. Set the hostname first.
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.hosts[0] --value <logto-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.logto.enabled --value true
```

---

## 8. Preview and Deploy

Run `just gateway check` and `just gcp-pulumi preview --folder gateway` to review the plan first, then run `just gateway deploy` to execute `check` → `ensure-stack` → `preview` → `create-resource` → `verify`. Point your DNS A records at the Gateway external IP address so `cert-manager` can complete the HTTP-01 challenges.
→ [`just gateway deploy`](reference/gateway/deployment.md#just-gateway-deploy) · [TLS certificate issuance](reference/gateway/deployment.md#tls-certificate-issuance)

```bash
just gcp-pulumi preview --folder gateway
just gateway deploy
```

---

## 9. Push the Stack File

Commit and push `gateway/Pulumi.${PULUMI_STACK}.yaml` so Git keeps the canonical routing and hostname configuration for the cluster.
→ [Create the stack](reference/gateway/deployment.md#create-the-stack)

```bash
git add "gateway/Pulumi.${PULUMI_STACK}.yaml" && git commit -m "gateway: stack config" && git push
```

---

## 10. Verify the Gateway

Run `just gateway verify` to confirm that `Issuer` `letsencrypt-issuer` is `Ready`, `Gateway` `dictycr-gateway` is `Programmed` with an external IP address, and every enabled route has an `Accepted` `HTTPRoute` and a `kubernetes.io/tls` Secret.
→ [Verification](reference/gateway/deployment.md#verification)

```bash
just gateway verify
```

---

## 11. Troubleshooting

→ [Troubleshooting table](reference/gateway/deployment.md#troubleshooting)

---

## 12. Related Documents

**Reference details for this guide:**
- [Gateway deployment details](reference/gateway/deployment.md)

**Other documentation:**
- Cluster setup and day-2 operations: [`kops-setup.md`](kops-setup.md)
- Pulumi backend and namespaces: [`pulumi-setup.md`](pulumi-setup.md)
- GraphQL server deployment: [`graphql-deploy.md`](graphql-deploy.md)
- MinIO deployment: [`minio-deploy.md`](minio-deploy.md)
- Logto deployment: [`logto-deploy.md`](logto-deploy.md)
