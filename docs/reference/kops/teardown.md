# Teardown & Disposable Lifecycle

Back to: [kOps Cluster Setup](../../kops-setup.md)

## Mindset & Durable Blueprint

The blueprint — the canonical manifest bundle at `config/kops/<cluster>/*.yaml` — is durable in Git. SA keys and the per-cluster env file stay on the operator machine (gitignored). GCE compute instances are transient. Recreation is fully declarative and reproducible.

> **Note on single-file `instancegroups.yaml` replace:** the manifest schema (`v1alpha2`) is verified cross-version compatible — `kops replace -f` accepts both files on kops v1.29.2 and v1.36.1. Full end-to-end re-creation from Git is still pending a first live-target run.

## What Survives Teardown

| Artifact | Survives? | Why |
|----------|-----------|-----|
| Canonical manifest bundle (`config/kops/<cluster>/*.yaml`) | ✅ Yes | Checked into Git — single source of truth |
| GCS state bucket (`gs://kops-state-<cluster>`) | ✅ Yes | `kops delete` does not delete the bucket |
| SSH keypair (`credentials/<project>/k8sVM*`) | ✅ Yes | Local files reused across cycles |
| Service account keys (`credentials/<project>/*.json`) | ✅ Yes | GCP service accounts and JSON keys persist |
| Per-cluster env file (`.env.<env>.<cluster>`) | ✅ Yes | Gitignored local credentials/paths; recreate with `just create-cluster-env` if missing |
| **GCE compute instances** (control-plane + workers) | ❌ Destroyed | Transient VMs |
| **Boot disks & etcd volumes** | ❌ Destroyed | Deleted with the instances |

## Destroy the Cluster

```bash
# Dry-run preview:
just gcp-cluster delete-cluster

# Full destroy:
just gcp-cluster delete-cluster --confirm yes
```

Without `--confirm yes` the recipe only previews. Nothing in the state bucket is touched.

## Optional: Delete the State Bucket

For complete deletion — bucket, all objects, all versions:

```bash
# Dry-run preview (lists objects, nothing deleted):
just gcp-cluster delete-state-bucket --cluster <cluster-name>

# Permanent deletion:
just gcp-cluster delete-state-bucket --cluster <cluster-name> --confirm yes
```

Bucket name defaults to `kops-state-<cluster-name>` derived from `--cluster`. Override with `--bucket-name <bucket|gs://bucket>` or the `BUCKET_NAME` env var.

> Deleting the bucket discards every manifest version kops stored. The Git bundle still lets you rebuild, but the state history is gone permanently.

## Caveats

- **kOps version pinning** — pins live in `.tool-versions`. Upgrade kOps through dedicated review PRs ([tool versions](tool-versions.md#upgrading-kops)).
- **GCP quotas** — frequent create/delete cycles can trigger API rate limits.

## Bringing It Back

See [recreation](recreation.md) for the full declarative rebuild sequence.
