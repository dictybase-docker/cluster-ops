# CI Variables and Credentials Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

The `just ci` recipes publish the deploy credentials and variables GitHub
Actions needs for tag deploys to a production cluster. Variables route by name
prefix (`<CI_ENV>_*`); the prod deployer key is a separate org secret
(`PROD_DEPLOY_SA_KEY`) selected by the `environment` input inside
`composite-deploy.yaml`.

## Recipes

| Recipe | Purpose |
|--------|---------|
| `just ci check-deploy-credentials` | Read-only preflight of the deployer SA key against the registry |
| `just ci set-deploy-secret` | Publish the key JSON to org secret `PROD_DEPLOY_SA_KEY` |
| `just ci sync-deploy-vars` | Set `<CI_ENV>_*` repo variables and verify secret visibility |

## check-deploy-credentials

Read-only, zero mutations, fails closed listing every failure:

1. Key file parses as GCP SA JSON; its `project_id` equals the registry's
   `gcp_project` — a dev key against a prod registry fails here.
2. The SA carries the deployer roles (kops state bucket read, Pulumi state
   read/write, `roles/cloudkms.cryptoKeyEncrypterDecrypter`).
3. Kubeconfig export from the kops state bucket works.
4. KMS encrypt/decrypt round-trip on the registry's secrets key.
5. Never prints key material or secret values.

## set-deploy-secret

Runs `gh secret set PROD_DEPLOY_SA_KEY --org dictyBase --visibility selected
--repos <repo-ids>` — one idempotent upsert that stores the key and sets repo
visibility. Refuses a missing or unreadable key file; never echoes the key;
prints the `rm` reminder for the on-disk file. Needs an org-admin `gh` token.

## sync-deploy-vars

1. Reads the registry entry → `ci_env` → variable names (`PROD_CLUSTER`,
   `PROD_KOPS_STATE_STORAGE`, `PROD_KOPS_VERSION`, `PROD_KUBECTL_VERSION`,
   `PROD_PULUMI_VERSION`).
2. `gh variable set` per `--repo` — idempotent upsert, prints before/after.
3. Verifies `PROD_DEPLOY_SA_KEY` is visible to each repo; prints the exact
   `set-deploy-secret` command to fix it when not.
4. Verifies org variable `PULUMI_STATE_STORAGE` is visible; warns when not.

## Variables Published

| Variable | Source key | Example (dcr-kube1) |
|----------|-----------|---------------------|
| `PROD_CLUSTER` | `cluster` | `dcr-kube1` |
| `PROD_KOPS_STATE_STORAGE` | `kops_state` | `gs://kops-state-dcr-kube1` |
| `PROD_KOPS_VERSION` | `kops_version` | `1.36.1` |
| `PROD_KUBECTL_VERSION` | `kubectl_version` | `1.35.8` |
| `PROD_PULUMI_VERSION` | `pulumi_version` | `3.255.0` |

Org secret `PULUMI_STATE_STORAGE` (the Pulumi backend bucket) already exists
and is only verified, never set.

## Secrets Inventory

| Secret | Used by | Notes |
|--------|---------|-------|
| `DEPLOY_SA_KEY` | develop deploys (dcr-experiments) | exists, unchanged |
| `PROD_DEPLOY_SA_KEY` | tag deploys (dcr-kube1) | new; minted or reused per [credentials details](ci-credentials.md), published by `set-deploy-secret` |
| `GH_DEPLOY_TOKEN`, `DOCKERHUB_USER`, `DOCKER_PASS` | all deploys | exist, unchanged |

`composite-deploy.yaml` selects the key with
`inputs.environment == 'production' && secrets.PROD_DEPLOY_SA_KEY || secrets.DEPLOY_SA_KEY`.
The develop path passes no `environment` input and is unchanged.

## Token Scopes

| Recipe | Minimum scope |
|--------|---------------|
| `set-deploy-secret` | org Actions secrets write (org admin) |
| `sync-deploy-vars` | Actions variables write on each target repo |

## Flags

| Flag | Applies to | Required | Default | Notes |
|------|-----------|----------|---------|-------|
| `--cluster` / `-c` | all three | Yes* | `$CLUSTER_NAME` | Registry cluster name |
| `--sa-key` / `-k` | check, set | Yes | — | Deployer SA key JSON path |
| `--repos` / `-r` | set, sync | Yes | — | Target repositories, `owner/name` comma-separated |

*Or the corresponding env var set inside `cluster-env`.

## Warnings

- The key file on disk is secret material. `set-deploy-secret` prints the `rm`
  reminder — delete the file after publishing unless it is a managed,
  gitignored credential.
- Do not widen `PROD_DEPLOY_SA_KEY` visibility beyond the deploying repos. Audit
  with `gh api orgs/dictyBase/actions/secrets/PROD_DEPLOY_SA_KEY/repositories`.
