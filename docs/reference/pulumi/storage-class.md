# StorageClass — First Apply

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## Why StorageClass First

ArangoDB, CNPG, Redis, and MinIO all request the `dictycr-balanced` and `dictycr-ssd` classes. A PVC referencing a class that does not exist stays Pending indefinitely, so apply the classes before any database stack.

Deploy once per cluster.

- Sizing and class choice: [`kops-gcp-architecture.md` §6](../../kops-gcp-architecture.md#-6-database-storage--retrieval)
- Consumed by production ArangoDB: [`arangodb-deploy.md` §3](../../arangodb-deploy.md#3-install-arangodb)

## Deploy — Real Cluster

Applies the stack, then verifies the classes and provisioner declared in `Pulumi.<stack>.yaml` — no flags:

```bash
just gcp-pulumi apply-storageclass
```

`$PULUMI_STACK` is the cluster's own name, matching its `Pulumi.<cluster>.yaml` ([stack names](stack-names.md)).

The recipe folds `ensure-stack` + `preview` + `create-resource` + verification, and retries the verification while the classes are not yet visible on the cluster. Pass `--stack <name>` to target a stack other than `$PULUMI_STACK`.

## Deploy — Lab Stacks

Lab clusters use the literal stack names `dev` or `experiments` ([stack names](stack-names.md)). Seed `dev` from `experiments` rather than starting empty:

```bash
just gcp-pulumi new-stack-from --folder storage_class --stack dev --from-stack experiments
just gcp-pulumi apply-storageclass --stack dev
```

## What Each Stack Declares

`apply-storageclass` reads these values from `storage_class/Pulumi.<stack>.yaml`, so verification expectations always match the stack being applied. Each cluster has its own stack ([stack names](stack-names.md)):

| Cluster stack | Classes declared | Provisioner |
|---------------|------------------|-------------|
| `dcr-kube1` (production) | `dictycr-balanced`, `dictycr-ssd` | `pd.csi.storage.gke.io` |
| lab clusters (forked from `Pulumi.dev.yaml` / `Pulumi.experiments.yaml`) | `dictycr-balanced` | `pd.csi.storage.gke.io` |
| local k3d (forked from `Pulumi.local.yaml`) | `dictycr-balanced` | `rancher.io/local-path` |

`Pulumi.prod.yaml` no longer exists for this project — the production config lives in `Pulumi.dcr-kube1.yaml`. A **new** production cluster forks from it:

```bash
just gcp-pulumi fork-stack --to-stack <new-cluster> --from-stack dcr-kube1 --folder storage_class
```

## Verify

`apply-storageclass` runs this check for you. Use `check-storageclass` standalone for day-2 drift checks — it defaults to requiring only `dictycr-balanced` with provisioner `pd.csi.storage.gke.io`.

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
