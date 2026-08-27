# Prerequisites & Execution Context

Back to: [kOps Cluster Setup](../../kops-setup.md)

## The GCP Project

You need an existing **GCP project with billing enabled**. An org admin creates it in the GCP console.

Note the `PROJECT_ID` — it goes in the [cluster env file](cluster-env.md) so later recipes can default `--project` from the activated shell.

> **One project = one cluster.** Keep cluster-specific files scoped per project — see [file isolation](file-isolation.md).

## Core System Tools

Install these through your system package manager:

| Tool | Why You Need It | Install |
|------|-----------------|---------|
| **Go** (≥1.26) | Builds the `cluster-ops` binary (`cmd/cluster-ops/main.go`) | [go.dev/dl](https://go.dev/dl/) |
| **just** | The task runner — all operations are `just` commands | [github.com/casey/just](https://github.com/casey/just#installation) |
| **gcloud** | Google Cloud CLI for authentication and GCP API interactions | [cloud.google.com/sdk](https://cloud.google.com/sdk/docs/install) |
| **jq** | JSON processor used behind the scenes | [jqlang.github.io/jq](https://jqlang.github.io/jq/download/) |
| **envsubst** | Renders `config/kops/_starter/*.tmpl` during `bootstrap-bundle` | `brew install gettext` / `apt install gettext-base` |

## Verify Everything

```bash
just check-tools
```

Prints PASS/FAIL and a version for each core system tool, then for each asdf-pinned tool ([tool versions](tool-versions.md)).

Check only the system tools, before asdf is set up:

```bash
just check-tools --skip-asdf yes
```

## Two Stores, One Handoff

Until the first YAML exists, the operator env file `.env.<env>.<cluster>` holds `PROJECT_ID` plus credentials and local paths.

From [bootstrap](bootstrap.md) onward, cluster identity and shape live **only** in Git under `config/kops/<cluster>/`. Do not copy kops name, state store, bucket name, or Kubernetes version back into the env file — see [cluster env](cluster-env.md#after-bootstrap--git-takes-identity).
