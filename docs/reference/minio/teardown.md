# MinIO Teardown Details

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

## Warning

**Destructive. Clone only.** Deletes the data PVC — every object in every bucket.

## Command

```bash
just minio teardown --namespace prod --delete-pvc yes
```

## Behavior

Order of operations:
1. `pulumi destroy` on the `minio` stack — removes the Helm release (pod, Service) and Secret `minio-root`
2. Deletes leftover `app.kubernetes.io/name=minio` PVCs in the namespace

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--namespace` | Yes | Target namespace |
| `--delete-pvc` | Yes | Must be `yes` — deletes the data PVC (all objects) |
| `--stack` | No | Defaults to `$PULUMI_STACK`; no dev fallback |

## What It Leaves Alone

- Imported data at the **source** — mirroring is one-way, the source is untouched
- `prod` namespace itself
- `stateful-db` pool — see [kops-setup.md §4](../../kops-setup.md#4-day-2-operations-git-first-workflow)
