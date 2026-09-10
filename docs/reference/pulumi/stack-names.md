# Pulumi Stack Names

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## The Rule

**One unique stack per cluster, named after the cluster** — mirroring the env file: `.env.<env>.<cluster>` carries `PULUMI_STACK=<cluster>`, so `dcr-kube1` gets the stack `dcr-kube1` in every project.

`prod`, `dev`, `experiments`, and `local` are **template** names, not stack names. They are the shipped `Pulumi.<name>.yaml` files a new cluster forks its config from.

Never share a stack name between clusters: stack state lives inside the project's state bucket, so two clusters in one GCP project would collide, and `pulumi stack ls` stops being unambiguous.

## New Cluster

Fork the base templates into per-cluster files, review the deltas, then point the env file at them:

```bash
# 1. Per-cluster config files (one per project that ships the base)
just gcp-pulumi fork-stack --to-stack <cluster-name>

# 2. Record the deltas for this cluster, if any
$EDITOR storage_class/Pulumi.<cluster-name>.yaml
git commit -am "feat(pulumi): <cluster-name> stack config"

# 3. Env file — PULUMI_STACK defaults to the cluster name, shown here explicitly
just create-cluster-env --env <env> --cluster <cluster-name> \
    --pulumi-stack <cluster-name> --force yes
```

`ensure-stack` then initializes each stack **from its own `Pulumi.<cluster-name>.yaml`**, so a new cluster starts from the base config with this cluster's deltas applied.

If a project has no base template left, fork from the closest existing cluster instead — `storage_class`'s `Pulumi.prod.yaml` was removed once `Pulumi.dcr-kube1.yaml` became the production config:

```bash
just gcp-pulumi fork-stack --to-stack <cluster-name> --from-stack dcr-kube1 --folder storage_class
```

## How Recipes Resolve It

[`create-cluster-env`](cluster-env.md) writes `PULUMI_STACK=<cluster-name>` into the env file. Every `just gcp-pulumi` recipe defaults `--stack` to `$PULUMI_STACK`, so day-to-day commands never spell out a stack name:

```bash
just cluster-env --env prod --cluster dcr-kube1   # exports PULUMI_STACK=dcr-kube1
just gcp-pulumi apply-storageclass                # operates on stack dcr-kube1
```

Override only for deliberately sharing one stack across near-identical clusters:

```bash
just create-cluster-env --env <env> --cluster <cluster-name> \
  --pulumi-stack <custom-name> --force yes
```

## Guard

`ensure-stack` refuses to initialize a stack with no `Pulumi.<stack>.yaml` template — that would create an empty stack and fail at preview with `missing required configuration variable`. It points at `fork-stack` (or `new-stack-from`, when the base stack already exists in the backend) instead.

## Per-Stack Files

Each project directory holds `Pulumi.yaml`, the shipped `Pulumi.<env>.yaml` templates, and one `Pulumi.<cluster>.yaml` per cluster it is deployed to. Secret values inside those files are encrypted with the KMS provider — `PULUMI_SECRET_PROVIDER` plus the `pulumi-manager` key ([backend bootstrap](backend-bootstrap.md)).
