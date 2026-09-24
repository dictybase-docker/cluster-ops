# ArangoDB Logical Databases Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## When to Run This

**Not a required step in the normal flow.** The first load in [deploy guide §4](../../arangodb-deploy.md#4-import-data) creates the databases itself (`arangorestore --create-database true --all-databases`) along with the source's collections and users, so nothing needs to pre-create them.

Use `create-databases` only when a logical database is missing or for the loader path in [deploy guide §4.3](../../arangodb-deploy.md#43-alternative-loaders). After a successful import, use [Finalize Import](bootstrap.md#finalize-import) to rotate the imported app user's password and update Secret `backend`; it reapplies grants without creating databases. `create-databases` remains safe against databases that already exist.

## What It Does

`create-arangodb-databases/Pulumi.dcr-kube1.yaml` creates:
- Secret `backend` (keys `user`, `password`)
- One-shot Job `backend-create-databases-<run-id>` (label `app=arangodb-create-databases`)

`configure-app-credentials` uses the same Pulumi project and owns the same Secret `backend`; its `backend-configure-app-credentials-<run-id>` Job rotates the existing app user's password and reapplies grants without creating databases. It sets `createDatabases=false`; the `create-databases` recipe explicitly enables database creation again.

### Job Structure

| Phase | Containers | Action |
|-------|------------|--------|
| Init | `ensure-user`, `ensure-database` (×7) | Create user and databases |
| Main | `ensure-grant` (×7) | Grant `rw` on each database |

**Databases created:** `annotation`, `order`, `stock`, `content`, `cgm_ddb`, `chado`, `annofeature`

The `arangoadmin` containers use the in-namespace `arangodb` Service from the [cluster step](cluster.md).

## Command

```bash
just arangodb create-databases --app-user '<app-user>' --app-password '<app-password>'
```

## Behavior

1. Runs `ensure-stack`
2. `pulumi config set-all --path` writes `properties.arangodbSecret.user` and `.pass` as encrypted secrets, enables database creation, and sets a unique run ID
3. Runs `preview` → `create-resource`
4. Waits for the unique Job, prints Secret `backend`

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--app-user` | Yes | Application username |
| `--app-password` | Yes | Application password |

Both required — `Pulumi.dcr-kube1.yaml` ships `name`/`userkey`/`passkey` but no `user`/`pass`. Applying without them creates Secret `backend` with empty credentials.

## Job Lifecycle

- Each run uses a unique Job name with run ID; previous Jobs remain available for logs until next run
- No TTL — Job remains available for logs and Pulumi state reconciliation
- Before the next run, recipe removes prior Jobs with no running containers and refreshes Pulumi state (including older TTL-deleted Jobs)
- Refuses to replace a matching Job while a container is running
- Default budget: `--retries 60 --interval 10`

## Scope

Does not touch the root password or the Cluster CR.
