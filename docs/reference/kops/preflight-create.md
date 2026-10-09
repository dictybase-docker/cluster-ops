# Preflight-Create Check Details

Back to: [kOps Cluster Setup](../../kops-setup.md)

## What It Does

Gate for `create-cluster`: verifies the operator environment, toolchain and
pinned versions, service-account credentials, gcloud shell isolation, the SSH
keypair, the git-manifest bundle and the state-store status — everything the
first mutating GCP call depends on. Read-only; every check is reported and the
recipe exits non-zero listing all failures.

## Behavior

1. `CLUSTER_NAME` / `PROJECT_ID` set (later groups report `not run` when they are not).
2. Toolchain binaries on `PATH` and matching pinned versions in `${ASDF_DEFAULT_TOOL_VERSIONS_FILENAME:-.tool-versions}`.
3. `GOOGLE_APPLICATION_CREDENTIALS`: key file exists, `.project_id` matches `PROJECT_ID`, `.client_email` is `kops-cluster-creator@<project>.iam.gserviceaccount.com`.
4. Shell isolation: `CLOUDSDK_ACTIVE_CONFIG_NAME == <project>-kops-cluster-creator`, gcloud account/project match.
5. SSH keypair: public key present under `credentials/<project>/`, matching private key present.
6. Manifest bundle `config/kops/<cluster>/`: files exist, `project`/`name`/`configBase` match, no unresolved `${VAR}` placeholders, instance-group images pinned (not family references), no `spec.taints`, git tree clean.
7. State store: bucket absent or clean-for-creation passes; existing state with running VMs fails (points at `apply-cluster`/`delete-cluster`); probe errors fail with the URI named (never misreported as "missing").

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--cluster` | No | `$CLUSTER_NAME` | Cluster name |
| `--project` | No | `$PROJECT_ID` | GCP project id |
| `--kops-name` | No | `$KOPS_CLUSTER_NAME` / `<cluster>-k8s.local` | Full kops DNS name |
| `--state` | No | `$KOPS_STATE_STORE` / `gs://kops-state-<cluster>` | Kops state URI |
| `--bucket-name` | No | `$BUCKET_NAME` / `kops-state-<cluster>` | State bucket name |
| `--ssh-key` | No | `$SSH_KEY` / `credentials/<project>/k8sVM.pub` | SSH public key path |
| `--root` | Yes | — | Repo root (manifest bundle + git checks) |

## Notes

- On success the wrapper still prints the `_cluster-config-report` summary and `Ready for create-cluster.`
- The pre-migration shell recipe remains as `preflight-create-shell` for one release (rollback).
