# Backend Stack Scaffold Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

`just gcp-pulumi scaffold-backend-stack` creates a service's stack config —
`<folder>/Pulumi.<stack>.yaml` — from the repo template and registers the
stack in the Pulumi backend. Describe-then-create: an existing stack file is
never overwritten; operator-tuned config stays intact.

## Behavior

1. Resolves the cluster through the [registry](cluster-registry.md) from
   `--stack`; namespace and KMS secrets provider come from the registry file —
   no stack→namespace table hides in recipe code.
2. Derives from `--folder`: `appName` (folder suffix after `modware-`),
   image name (`dictybase/<folder>`), config key prefix (`<folder>:`), and the
   default `arangodbSecret.name` (= appName — one Secret per service, not a
   shared credential).
3. `pulumi stack init --secrets-provider <kms>` in the folder — registers the
   stack and writes the `secretsprovider` + `encryptedkey` lines.
4. Renders the config block from `config/templates/backend-stack.yaml.tmpl`
   and appends it below those lines — the template holds only the `config:`
   block, never the provider header (hand-copying loses the encrypted key).
5. Fails when `Pulumi.<stack>.yaml` already exists.
6. Prints the next step (`check-backend-prereqs`).

`image.tag: bootstrap` is a placeholder. CI deploys always override the tag at
deploy time (`pulumi config set --path properties.image.tag <ref>` runs before
`pulumi up` in the dagger pipeline); the committed value is used only by the
manual first deploy, where you pass the real published tag.

## Production Fields

The template leaves optional `internal/backend` fields unset. Set them on the
prod stack for production posture (defaults preserve lab behavior):

| Field | Lab default | Suggested prod value |
|-------|-------------|----------------------|
| `replicas` | 1 | 2 |
| `resources` | none | `requests: {cpu: 100m, memory: 128Mi}, limits: {cpu: 500m, memory: 512Mi}` |
| `grpcHealthProbe` | off | on (readiness + liveness via gRPC health protocol) |

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--folder` / `-f` | Yes | — | Service project folder (for example `modware-order`) |
| `--stack` / `-s` | Yes* | `$PULUMI_STACK` | Must match a registry entry's `stack` |
| `--port` / `-p` | No | `9250` | gRPC server port |
| `--secret-name` | No | appName | Override when a service genuinely needs an existing Secret |

*Or `$PULUMI_STACK` set inside `cluster-env`.

## Warnings

- **Never edit `secretsprovider`/`encryptedkey` by hand** — the encrypted key
  is bound to the KMS key; a stale value breaks all later secret sets.
- **No plaintext secrets in stack files.** Use `just gcp-pulumi set-secret` —
  it stores the value encrypted through the KMS provider.
- The rendered file is inert until committed and pushed to cluster-ops
  `develop` — CI deploys clone `develop`, so an unpushed stack file deploys
  nothing.
