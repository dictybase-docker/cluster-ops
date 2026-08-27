# Creating and Configuring a Stack

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

Run from repo root with a cluster shell active. Every recipe defaults `--stack` to `$PULUMI_STACK` ([stack names](stack-names.md)).

## Create or Select

`ensure-stack` selects the stack named by `$PULUMI_STACK` if it exists, or initializes it if not. Use it instead of a raw `pulumi stack select || pulumi stack init`:

```bash
just gcp-pulumi ensure-stack --folder <project-folder>
```

## Seed From an Existing Stack

To copy configuration from another stack rather than starting empty:

```bash
just gcp-pulumi new-stack-from --folder <project-folder> --from-stack <base-stack>
```

## Set Configuration

Plain values:

```bash
just gcp-pulumi set-config --folder <project-folder> --key "<key>" --value "<value>"
```

Secret values — use `set-secret`, never a raw `pulumi config set --secret`:

```bash
just gcp-pulumi set-secret --folder <project-folder> --key "<key>" --value "<secret-value>"
```

Both write with `--path`, so dotted keys like `properties.secret.password` create nested structure rather than a literal flat key.

## Preview and Apply

```bash
just gcp-pulumi preview --folder <project-folder>
just gcp-pulumi create-resource --folder <project-folder>
```

Always preview before applying. `create-resource` runs `pulumi up -f -y` — it does not prompt.

## Overriding the Stack

Pass `--stack <name>` on any recipe to override `$PULUMI_STACK` for that one command:

```bash
just gcp-pulumi preview --folder <project-folder> --stack <other-stack>
```

## Do Not Use pulumi-init-and-deploy

> `just pulumi-init-and-deploy` deploys many projects in bulk from `pulumi-files/resources/*.txt`. It is the wrong tool for setup and for any targeted change. Run one project at a time with the recipes above.

## Full Recipe Reference

See [recipes](recipes.md) for every `gcp-pulumi` recipe and the environment variables it reads.
