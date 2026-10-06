# Deploy Credential Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

The deployer key is a GCP service-account key JSON used by tag deploys to
authenticate to the production cluster. Two supported sources, operator's
choice: **reuse** an existing key from cluster management, or **mint** a
dedicated deployer service account. `check-deploy-credentials` is the
read-only preflight either path must pass before `set-deploy-secret`
publishes the key to the org secret.

## Key Source

**Mint a dedicated deployer** — one recipe, standard path, idempotent:

```bash
just ci create-deploy-key --cluster dcr-kube1
```

Behavior: SA `deployer` created with the `deployer-roles.txt` role set when
missing (reused otherwise); key minted at `credentials/<cluster>/deployer.json`
— the standard per-cluster credentials folder — only when the file is absent
(SA keys cap at 10; an existing key file is always reused, never re-minted).
Then the preflight below.

`deployer-roles.txt` holds the exact roles deploys need — kOps state bucket
read (kubeconfig export), Pulumi state bucket read/write, and
`roles/cloudkms.cryptoKeyEncrypterDecrypter` on the registry's KMS key. A
dedicated SA keeps a separate audit trail and can be revoked without touching
cluster management.

**Reuse an existing key** — pass `--sa-key credentials/<cluster>/deployer.json` to the recipes below. Valid
only when that SA's roles are exactly the deployer set and nothing broader;
`check-deploy-credentials` catches a mismatch.

## check-deploy-credentials

Read-only, zero mutations, fails closed listing every failure:

1. Key file parses as GCP SA JSON; its `project_id` equals the registry's
   `gcp_project` — a dev key against a prod registry fails here.
2. The SA carries every role in `deployer-roles.txt` (IAM policy describe).
3. Kubeconfig export from the registry's `kops_state` bucket works — the same
   export path `composite-deploy` uses at deploy time.
4. KMS encrypt/decrypt round-trip on the registry's `kms_secrets_provider`
   key (stack operations fail cryptically without the KMS role).
5. Never prints key material or secret values.

## set-deploy-secret

Runs `gh secret set PROD_DEPLOY_SA_KEY --org dictyBase --visibility selected
--repos <repo-ids>` — one idempotent upsert that stores the key and sets repo
visibility. Refuses a missing or unreadable key file; never echoes the key;
prints the `rm` reminder for the on-disk file. Needs an org-admin `gh` token.

## Flags

| Flag | Applies to | Required | Default | Notes |
|------|-----------|----------|---------|-------|
| `--cluster` / `-c` | both | Yes* | `$CLUSTER_NAME` | Registry cluster name |
| `--sa-key` / `-k` | both | Yes | — | Deployer SA key JSON path |
| `--repos` / `-r` | set | Yes | — | Target repositories, `owner/name` comma-separated |

*Or `$CLUSTER_NAME` set inside `cluster-env`.

## Warnings

- The key JSON is secret material. Keep it under the gitignored
  `credentials/<cluster>/` folder, and never commit it.
- Re-running `create-sa-key` accumulates keys (GCP allows 10 per SA) — audit
  with `gcloud iam service-accounts keys list`.
- `deployer-roles.txt` caps what a leaked key can do; keep it minimal.
