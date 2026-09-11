# Shared Namespaces — Bootstrap Stack

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## Why a Bootstrap Stack

The `operators` namespace (all operator Helm releases) and the app namespace (`prod` on production clusters, `dev` on lab stacks) are prerequisites for nearly every later stack. `namespace-bootstrap` is the **only writer** of both:

- Operator programs ([CloudNativePG](../../reference/postgres/operator.md), [ArangoDB](../../reference/arangodb/operator.md)) probe the stack via `StackReference` + a live `GetNamespace` read (`internal/nsprobe`) — a missing bootstrap fails their `preview` with a pointer to this page, not deep inside a Helm create.
- `backup_secrets` places Secret `dictycr` into the app namespace and creates no namespaces.
- No recipe or program may create these namespaces by any other path — two writers to one Kubernetes object means either destroy path can delete a namespace with live workloads inside.

Deploy once per cluster, right after [StorageClass](storage-class.md).

## Deploy

Applies the stack, then verifies exactly the namespaces `Pulumi.<stack>.yaml` declares:

```bash
just gcp-pulumi apply-namespaces
```

`$PULUMI_STACK` is the cluster's own name, matching its `Pulumi.<cluster>.yaml` ([stack names](stack-names.md)). The recipe folds `ensure-stack` + `preview` + `create-resource` + verification. Lab stacks pass `--stack dev` (or `experiments`).

## What Each Stack Declares

| Cluster stack | `operatorNamespace` | `appNamespace` |
|---------------|--------------------|----------------|
| `dcr-kube1` (production) | `operators` | `prod` |
| lab stacks (`dev` / `experiments`) | `operators` | `dev` |
| local k3d (`local`) | `operators` | `dev` |

Both keys are required in every `Pulumi.<stack>.yaml` — an empty value fails preview instead of creating a nameless namespace.

## Exports

The stack exports `operatorsNamespace` and `appNamespace`. Operator programs read them via the stack reference `organization/namespace-bootstrap/<stack>` (`internal/nsprobe`; the literal `organization` segment is required by the self-managed GCS backend) — the Helm release namespace comes from the export itself, so an operator stack cannot drift from the bootstrap, and a missing bootstrap fails the operator's `preview`.

A **new** production cluster gets its config via the standard fork:

```bash
just gcp-pulumi fork-stack --to-stack <new-cluster> --from-stack dcr-kube1
```

## Existing Stacks — Check State Before Applying

The refactor that made this stack the only namespace writer removed namespace resources from `backup_secrets`. On a cluster where `backup_secrets` was **already applied**, a plain `pulumi up` would plan to **delete** `prod`/`operators` with everything inside. Check first:

```bash
pulumi -C backup_secrets stack export --stack <stack-name> |
  jq -r '.deployment.resources[] | select(.type=="kubernetes:core/v1:Namespace") | .urn'
```

If any namespace URNs appear, detach them (object stays, state entry goes) before the next apply:

```bash
pulumi -C backup_secrets state delete 'urn:pulumi:<stack>::backup_secrets::kubernetes:core/v1:Namespace::<name>'
```

## Teardown

`just gcp-pulumi remove-resource --folder namespace-bootstrap --stack <stack-name>` deletes the namespaces **and everything inside them**. Only run it for a disposable cluster teardown, never to "recreate" a namespace.