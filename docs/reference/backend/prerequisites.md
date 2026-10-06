# Backend Prerequisite Details

Back to: [Backend Service Deploy Guide](../../backend-service-deploy.md)

## What It Does

The prerequisite recipes prove a cluster can accept a service before the first
deploy runs. `check-backend-prereqs` is the composite gate — read-only, zero
mutations, exits non-zero listing every missing prerequisite. Individual probes
exist for diagnostics.

## Behavior

`just gcp-pulumi check-backend-prereqs --stack <stack> --folder <folder>` checks,
in order:

1. **Registry entry** — the stack resolves to a cluster through the
   [registry](cluster-registry.md); namespace comes from there.
2. **Namespace** — exists and is Active (`kubectl get ns`).
3. **ArangoDB credentials Secret** — the Secret named by the stack config's
   `arangodbSecret` exists in the namespace and carries the configured
   `userkey`/`passkey`. Key names only — never Secret values.
4. **Application database** — the service's database exists in ArangoDB and
   the credentials work, **through the same Service the app connects to** (the
   `arangodb` Service behind the `ARANGODB_SERVICE_HOST`/`ARANGODB_SERVICE_PORT`
   env vars modware services read). The probe port-forwards the Service, then
   calls the ArangoDB REST API with the Secret credentials: 200 = green,
   401 = credentials, 404 = database missing (apply the
   `create-arangodb-databases` stack, [ArangoDB deploy](../../arangodb-deploy.md)).
5. **Port** — the stack config's port value is in range.

`just gcp-cluster verify-deployer-access --cluster <name> --sa-key <path>`
checks the deployer identity the CI pipeline will use:

1. Exports a kubeconfig from the registry's `kops_state` bucket — the same
   code path `composite-deploy` uses at deploy time, so a change there is
   caught by this probe, not by a failed deploy.
2. Probes `kubectl auth can-i update deployments -n <namespace>` — fails with
   the missing permission named.
3. KMS encrypt/decrypt round-trip on the registry's secrets key.

## Layers Checked

| Layer | Probe | Failure looks like |
|-------|-------|--------------------|
| Registry | `registry-show` | `no registry entry for cluster …` |
| Namespace | `check-backend-prereqs` | `namespace prod missing — run namespace-bootstrap` |
| K8s Secret | `check-backend-prereqs` | `secret order missing keys user/password` |
| ArangoDB database | `check-backend-prereqs` | `database order not found (404)` or `credentials rejected (401)` via `svc/arangodb` |
| Deployer identity | `verify-deployer-access` | `cannot update deployments in prod` |

## Flags

### check-backend-prereqs

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` / `-s` | Yes* | `$PULUMI_STACK` | Resolves the cluster through the registry |
| `--folder` / `-f` | Yes | — | Service project folder (stack config source) |
| `--arango-service` / `-a` | No | `arangodb` | Service the app connects to (`ARANGODB_SERVICE_HOST` source) |
| `--arango-port` / `-p` | No | `18529` | Local port for the port-forward probe |

### verify-deployer-access

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--cluster` / `-c` | Yes* | `$CLUSTER_NAME` | Registry cluster name |
| `--sa-key` / `-k` | Yes | — | Deployer SA key JSON path |

*Or the corresponding env var set inside `cluster-env`.

## Warnings

- Both probes are read-only. A failed gate fixes nothing by re-running — fix
  the named layer, then re-run.
- A green Secret check does not create the database. Database creation belongs
  to `create-arangodb-databases`; run it before the gate if the check names it.
