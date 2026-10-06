# Backend Service Troubleshooting

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## Failure Table

| Failure | Cause | Fix |
|---------|-------|-----|
| `no registry entry for cluster <name>` | `config/clusters/<name>.yaml` missing | Add the file — see [registry details](cluster-registry.md) |
| Scaffold: `stack file already exists` | Describe-then-create guard | Edit the existing `Pulumi.<stack>.yaml` by hand, or delete it only if you own it |
| Prereq: `namespace <ns> missing` | namespace-bootstrap stack not applied | `just gcp-pulumi update --folder namespace-bootstrap --stack <stack>` |
| Prereq: `secret <name> missing keys user/password` | ArangoDB credentials Secret absent or key names differ | Recreate via [ArangoDB deploy](../../arangodb-deploy.md); key names must match the stack config's `arangodbSecret` |
| Prereq: `database <name> not found (404)` | Database not created | Apply the `create-arangodb-databases` stack, then re-run the gate |
| Prereq: `credentials rejected (401)` | Secret does not match the ArangoDB user | Recreate via `create-arangodb-databases`; key names must match the stack config |
| Prereq: `port-forward never came up` | `arangodb` Service missing or pods not ready | `kubectl -n <ns> get svc arangodb`, check pod readiness, re-run |
| `verify-deployer-access`: `cannot update deployments in <ns>` | Deployer SA lacks the role | Add the missing role from `deployer-roles.txt` to the SA |
| `verify-deployer-access`: KMS round-trip fails | SA lacks `roles/cloudkms.cryptoKeyEncrypterDecrypter` | Grant the role on the registry's KMS key |
| `check-deploy-credentials`: `project_id mismatch` | Dev key against a prod registry (or reverse) | Use the key minted for that cluster's project |
| First deploy: `ERROR: stack '<name>' does not exist … no Pulumi.<stack>.yaml` | Stack file not committed to the repo | Commit the scaffolded file first ([scaffold details](scaffold-backend-stack.md)) |
| First deploy: image check fails | Pod runs an old/`bootstrap` tag | Pass `--image-tag <published-tag>`; never leave `bootstrap` in prod |
| Tag deploy authenticates with the dev key | `dictyBase/workflows` key-selection change not merged | Merge it, then re-run the workflow |
| Tag deploy does not trigger | `tag-build.yaml` not merged into the service repo | Merge the rendered file ([render details](render-deploy-workflows.md)) |
| Tag deploy: workflow queued, never starts | `concurrency` group serialized behind another run | Wait; or cancel the queued run and re-push |
| Pulumi: `the stack is already locked` | Another `pulumi up` holds the lock (crashed runner, parallel deploy) | `pulumi -C <folder> -s <stack> cancel` **only after confirming** the holder is dead; never cancel a live run |
| Deploy green but clients fail | Service answers only inside the cluster (ClusterIP) | Probe from an in-cluster pod: `kubectl run --rm -i grpcurl-probe --image=fullstorydev/grpcurl -- <service>.<ns>.svc.cluster.local:<port> list` |
| App CrashLoops on ArangoDB connect | App connects through `svc/arangodb` (`ARANGODB_SERVICE_HOST`), not pod exec | The prereq gate probes the same Service — a green gate but failing app means an app-side flag (`--is-secure`, database name), not the database |
| `gh secret set` in `set-deploy-secret` fails | Token lacks org Actions secrets write | Re-auth `gh` with org-admin scope — see [credentials details](ci-credentials.md) |
| `sync-deploy-vars`: `PROD_DEPLOY_SA_KEY not visible to <repo>` | Repo not in the secret's selected-repos list | Re-run `just ci set-deploy-secret` with `--repo <owner/name>` |
| Render fails on input-drift guard | `composite-deploy.yaml` renamed an input | Update the template, or pin `--workflow-ref` to the last compatible ref |

## Rollback

Re-run the previous tag's workflow — same ref, same image, same stack:

```bash
gh run rerun <run-id> --repo dictyBase/modware-order
```

A failed deploy does not roll back by itself: Pulumi stops at the failure and
the previous ReplicaSet keeps serving. Rerun only after the failure is
understood.
