# Pulumi Automation Stack Details

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## What It Does

Manages Pulumi stacks through the Automation API. It opens stacks from a
registry entry or the `PULUMI_*` environment, writes config keys, previews
with a structured diff, and updates with secret-safe event logs. Raw Pulumi
engine output never reaches logs. Read-only except `up`, `destroy` and
`config set`.

## Behavior

1. `stack ensure` selects a stack. It creates the stack only when the
   committed `Pulumi.<stack>.yaml` exists (fail closed).
2. `stack config set` writes one key. `--secret` marks it for the secrets
   provider. Keys are path keys (`properties.x.y`).
3. `stack preview` prints `creates/updates/deletes/replaces` counts and
   blocks `delete`/`replace` on protected types (PVC, StatefulSet, Database;
   Deployments only in the `PROTECT_DEPLOY_NS` namespaces). `--allow-protected`
   accepts them. The preview writes a saved plan file; the update applies that
   exact plan.
4. `stack up` runs the same gate, then updates. `--dry-run` stops at the
   preview and reports its counts. Secret config values are masked in every
   log line and in update errors.
5. `stack destroy` destroys resources only with `--confirm yes`; without it
   the command fails closed and mutates nothing.
6. `stack outputs --key <k>` prints one output value.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--folder` | Yes | — | Folder of the Pulumi project |
| `--stack` | Yes | `$PULUMI_STACK` | Stack name |
| `--key` / `--value` | Yes | — | Config key and value (`config set`) |
| `--secret` | No | off | Mark the value secret |
| `--create` | No | off | Create the stack (`ensure`) |
| `--copy-config-from` | No | — | Copy config from this stack after create |
| `--allow-protected` | No | off | Accept protected delete/replace |
| `--dry-run` | No | off | Report only (`up`, `destroy`) |
| `--registry-dir` | No | `config/clusters` | Cluster registry directory |

## Notes

- The `gcp-pulumi` wrappers call these subcommands; their pre-migration bodies
  remain as `*-shell` for one release.
- `pulumi-gcs-setup`, `pulumi-local-setup`, `fork-stack` and `cleanup-resource`
  stay on the Pulumi CLI (one-time setup, file copy, or no subcommand pinned).
