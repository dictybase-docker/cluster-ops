# Filestash Verify Details

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## just filestash verify

### What It Does

Read-only checks. Each failed group prints `FAIL`, and the recipe exits non-zero at the end.

### Behavior

| Group | Check |
|---|---|
| Rollout | `deployment/filestash` is ready. |
| PVC | `pvc/filestash-state` is `Bound`, and its use is 80% or less. |
| HTTP (port-forward) | `/healthz` answers. `/about` lists each plugin in the lock file plus `plg_search_stateless` and `plg_image_c`. `/admin/tty/` returns 404. |
| Bucket policies | The anonymous policy on `downloads` equals `filestash/policies/downloads-anonymous.json`. `uploads` is private. |
| Backup age | `backup.maxAgeHours` is set. The newest completed state copy and the newest Velero backup of the namespace are younger than it. |
| Velero scope | The PVC has no `velero.io/exclude-from-backup=true` label, the pod excludes no `state` volume, and no Velero Schedule excludes the namespace. |

### Flags

| Flag | Required | Default |
|---|---|---|
| `--stack` | No | `$PULUMI_STACK` |
| `--namespace` | No | `properties.namespace` |
