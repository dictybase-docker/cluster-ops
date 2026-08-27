# StorageClass — First Apply

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## Why StorageClass First

ArangoDB, CNPG, Redis, and MinIO all request the `dictycr-balanced` and `dictycr-ssd` classes. A PVC referencing a class that does not exist stays Pending indefinitely, so apply the classes before any database stack.

Deploy once per cluster.

- Sizing and class choice: [`kops-gcp-architecture.md` §6](../../kops-gcp-architecture.md#-6-database-storage--retrieval)
- Consumed by production ArangoDB: [`arangodb-deploy.md` §3](../../arangodb-deploy.md#3-install-arangodb)

## Deploy — Real Cluster

Uses `$PULUMI_STACK`, so no `--stack` flag is needed:

```bash
just gcp-pulumi ensure-stack --folder storage_class
just gcp-pulumi preview --folder storage_class
just gcp-pulumi create-resource --folder storage_class
```

## Deploy — Lab Stacks

Lab clusters use the literal stack names `dev` or `experiments` ([stack names](stack-names.md)). Seed `dev` from `experiments` rather than starting empty:

```bash
just gcp-pulumi new-stack-from --folder storage_class --stack dev --from-stack experiments
just gcp-pulumi preview --folder storage_class --stack dev
just gcp-pulumi create-resource --folder storage_class --stack dev
```

## What Each Stack Declares

The classes and provisioner differ per stack, so the verification command differs too:

| Stack | Classes declared | Provisioner |
|-------|------------------|-------------|
| `prod` | `dictycr-balanced`, `dictycr-ssd` | `pd.csi.storage.gke.io` |
| `dev`, `experiments` | `dictycr-balanced` | `pd.csi.storage.gke.io` |
| `local` | `dictycr-balanced` | `rancher.io/local-path` |

## Verify

`check-storageclass` confirms each named class exists and reports its provisioner. It defaults to requiring only `dictycr-balanced` with provisioner `pd.csi.storage.gke.io`.

Production — name both classes, otherwise a missing `dictycr-ssd` passes silently:

```bash
just gcp-pulumi check-storageclass --classes dictycr-balanced,dictycr-ssd
```

Lab (`dev` / `experiments`) — the default is correct:

```bash
just gcp-pulumi check-storageclass
```

Local (`local` stack) — override the expected provisioner:

```bash
just gcp-pulumi check-storageclass --provisioner rancher.io/local-path
```

The command also prints the cluster's default StorageClass, if one is set — a surprise default silently absorbs any PVC that does not name a class explicitly.

On a GCE cluster, a wrong or missing `pd.csi.storage.gke.io` provisioner almost always means the PD CSI driver is not enabled — see [prerequisites](prerequisites.md).

## Teardown

Destructive if PVCs still reference the class. Existing volumes are not deleted, but no new PVC can bind:

```bash
just gcp-pulumi remove-resource --folder storage_class --stack <stack-name>
```
