# First Deploy Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

`just gcp-pulumi bootstrap-service` runs the first deploy of a scaffolded
backend stack end to end, preflight-gated and aborting before any mutation
when [prerequisites](prerequisites.md) fail. Named `bootstrap-service` —
`bootstrap-backend` already means the Pulumi state backend.

## Behavior

Fixed order; the contract test asserts the sequence:

1. `check-backend-prereqs` — aborts before any mutation.
2. `ensure-stack` — selects the stack, or initializes it from the stack file.
   Fails when `Pulumi.<stack>.yaml` is absent from the repo — CI must never
   invent stacks.
3. Sets `properties.image.tag` to `--image-tag` (`set-config --plaintext`),
   then `preview` — the operator sees the resources before anything changes.
4. `update` — applies (`create-resource` → `pulumi up`).
5. Rollout wait + **image check** — `kubectl rollout status` alone does not
   prove the right image: the recipe also asserts the running pod's image tag
   equals `--image-tag`.
6. Prints the next step: commit and push the stack file, then publish CI
   credentials.

**Manual on purpose** — the stack file commit and push stay human. CI deploys
clone cluster-ops `develop` at deploy time; an unpushed stack file deploys
nothing. Push cluster-ops `develop` **before** any tag that should deploy.

## Verification

After the first tag deploy:

```bash
kubectl -n prod get deploy order-api-server \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
# expect: dictybase/modware-order:<tag>
```

In-cluster gRPC reachability:

```bash
kubectl run --rm -i grpcurl-probe --image=fullstorydev/grpcurl -- \
  order-api-server.prod.svc.cluster.local:9250 list
```

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | Yes* | `$PULUMI_STACK` | Registry stack name |
| `--folder` / `-f` | Yes | — | Service project folder |
| `--image-tag` / `-t` | Yes | — | Published image tag for the manual first deploy (CI overwrites the tag on every later deploy) |
| `--arango-service` / `-a` | No | `arangodb` | Passed to the prereq gate |

*Or `$PULUMI_STACK` set inside `cluster-env`.

## Warnings

- A prerequisite failure leaves the cluster untouched — the contract test
  asserts no `pulumi up` ran.
- `--image-tag` must be an already-published Docker Hub tag; the first deploy
  builds nothing.
- On any failed step, fix and re-run the recipe — it is idempotent up to the
  `update` step.
