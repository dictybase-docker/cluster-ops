# ArangoDB Operator Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

## What It Does

Installs the `kube-arangodb` Helm chart (pinned **1.4.5**, `arangodb-operator/Pulumi.dcr-kube1.yaml`). Since 1.4.x the operator is **namespaced** — the binary watches only its own pod's namespace — so the release installs into the **app namespace** (`prod`, from the namespace-bootstrap export), next to the `ArangoDeployment` CRs it manages. The operator watches for `ArangoDeployment` custom resources and manages the Cluster's pods, PVCs, and internal Service. The CRD API group (`database.arangodb.com/v1`) is unchanged from 1.2.x, so existing `ArangoDeployment` resources need no migration; the deployment-replication feature was removed upstream in 1.3.x/1.4.x and its values wiring is gone.

## Command

```bash
just arangodb deploy-operator
```

## Behavior

- Runs `ensure-stack` → `preview` → `create-resource` on `arangodb-operator`
- Waits for a ready `app.kubernetes.io/name=kube-arangodb` pod
- Asserts the `arangodeployments.database.arangodb.com` CRD exists

## Configuration

- **No secrets required**
- `--namespace` defaults to `prod`, and must equal the `namespace-bootstrap` stack's `appNamespace` export — the run stops on a mismatch (the 1.4 operator is namespaced, so the release cannot deploy anywhere else)
- The program probes the `namespace-bootstrap` stack (`StackReference` + a live `GetNamespace` read via `internal/nsprobe`) — preview fails with a pointer to [`pulumi-setup.md` §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces) when the namespace is missing. Nothing in this stack creates a namespace.

## Wait Budget

`--retries 60 --interval 10` (10 minutes) before giving up and printing pods.

## Scope

Touches nothing else — no Cluster CR, no databases, no backup.
