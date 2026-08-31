# MinIO Install Details

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

## What It Does

Applies the `minio` stack (`Pulumi.prod.yaml`) which creates:

1. Secret `minio-root` — keys `rootUser` / `rootPassword`
2. Helm release `minio` (chart **17.0.23**, standalone mode) — StatefulSet pod, 150Gi PVC on `dictycr-balanced`, Service `minio` on 9000

## Command

```bash
just minio deploy --root-user '<user>' --root-password '<password>'
```

## Behavior

- Runs `ensure-stack` → sets the encrypted `properties.secret.userName` + `properties.secret.password` → `preview` → `create-resource`
- Waits for a ready `app.kubernetes.io/name=minio` pod
- Prints the `minio` Service

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--root-user` | Yes | — | Stored encrypted; never generated or defaulted |
| `--root-password` | Yes | — | Stored encrypted; never generated or defaulted |
| `--namespace` | No | `prod` | |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` / `--interval` | No | `60` / `10` | 10-minute wait budget |

## Chart 17 Changes (vs the lab stacks)

- **Image repository moved**: free Bitnami images live under `bitnamilegacy/minio` with dated tags (Bitnami Secure Images publishes `latest`-only). The stack pins `2025.7.23-debian-12-r3`. Lab configs predate the `repository` field — the program defaults it to `bitnamilegacy/minio`.
- **`disableWebUI` removed**: the Console is a separate deployment gated on `console.enabled`. Prod sets `webui: false` → Console off. The guide never exposes a UI; use `mc` from the operator machine.
- **Ingress**: chart 17 renames `apiIngress.*` to `ingress.*`. This program renders the Ingress itself and prod sets `apiIngress.enabled: false` — the S3 API stays cluster-internal. Expose it via the `ingress/` project when needed, not via this stack.

## Service and Credentials

| Item | Value |
|------|-------|
| S3 API | `http://minio.prod.svc.cluster.local:9000` |
| Credentials | Secret `minio-root`, keys `rootUser` / `rootPassword` |

Application config reads the same Secret — e.g. `graphql_server` maps `rootUser`/`rootPassword` into its `ACCESS_KEY`/`SECRET_KEY` env vars.

## No HA

Standalone mode is one pod + one PVC. There is no erasure coding across nodes and no failover — a pod/node failure is downtime until reschedule. See the guide [§2](../../minio-deploy.md#2-install-minio) for the mitigation (bucket-level copies elsewhere).
