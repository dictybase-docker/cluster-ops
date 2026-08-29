# ArangoDB Logical Databases Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

> **Not a required step in the normal flow.** The first load in [deploy guide §4](../../arangodb-deploy.md#4-import-data) creates the databases itself (`arangorestore --create-database true --all-databases`) along with the source's collections and users, so nothing needs to pre-create them.
>
> This recipe is here for when something is missing anyway — a database the dump did not contain, an application user or Secret `backend` you need to (re)create after `--include-system-collections` restored the source's `_users`, or the loader path in [deploy guide §4.3](../../arangodb-deploy.md#43-alternative-loaders), where the databases must exist before the loaders write into them. It is safe to run against databases that already exist.

## What It Does

`create-arangodb-databases/Pulumi.prod.yaml` creates:
- Secret `backend` (keys `user`, `password`)
- One-shot Job `backend-create-databases` (label `app=arangodb-create-databases`)

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
2. Single `pulumi config set-all --path` writes `properties.arangodbSecret.user` and `.pass` as encrypted secrets
3. Runs `preview` → `create-resource`
4. Waits for Job, prints Secret `backend`

## Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--app-user` | Yes | Application username |
| `--app-password` | Yes | Application password |

Both required — `Pulumi.prod.yaml` ships `name`/`userkey`/`passkey` but no `user`/`pass`. Applying without them creates Secret `backend` with empty credentials.

## Job Lifecycle

- Name derived from `properties.arangodbSecret.name` (`backend` in prod), matching `main.go`'s `"<name>-create-databases"`
- `ttlSecondsAfterFinished: 900` — vanishes 15 minutes after finishing
- Recipe treats a Job it observed as done if it disappears mid-wait
- Default budget: `--retries 60 --interval 10`

## Scope

Does not touch the root password or the Cluster CR.
