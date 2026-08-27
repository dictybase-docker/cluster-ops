# ArangoDB Troubleshooting

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| No `stateful-db` nodes | IG missing or wrong cluster | [Pool requirements](pool-requirements.md) + [README §3.2](../../kops-setup.md#step-32--customize-yaml-in-git) |
| Pod Pending, taint | CR missing toleration | `arangodb-cluster/Pulumi.prod.yaml` vs node taint |
| Pod Pending, `arm64` | Lab Single | Wrong stack. Do not edit `Pulumi.dev.yaml` |
| Only one Arango pod | Applied `arangodb-single` | Use `arangodb-cluster` with `$PULUMI_STACK`, not lab stacks |
| PVC Pending | No StorageClass / CSI | [`pulumi-setup.md`](../../pulumi-setup.md) |
| Members pile in one zone | Soft anti-affinity | Expected under pressure; see [pool requirements](pool-requirements.md) |
| Quorum loss after drain | No PDB | Drain via operator |
| Loader Pending after taint | Job has no toleration | Add toleration or use `stateless-web` |
| App cannot connect | Wrong host or secret | [Cluster details](cluster.md) DNS + Secret `backend` |
| DB Job 401 | Root password mismatch | Secret `arangodb-pass` vs stack config |
| Backup permission error | Missing bucket or `dictycr` | [Backup details](backup.md) |
| Recipe exits with "no stack name" | Not inside `just cluster-env`, so `$PULUMI_STACK` unset | Enter cluster shell, or pass `--stack <name>`. Deliberate: prod recipes never fall back to `dev` |
| `arangodb-restore` apply fails, "confirmTarget must exactly equal" | Hand-edited config, or stale `restoreId`/`namespace`/`server` | Re-run `just arangodb configure-restore --namespace <ns>` ([restore details](restore.md)). Do not weaken check |
| `restic-restore` init container fails | Wrong `snapshot` ID, `dictycr` mismatch, or empty bucket | List snapshots ([restore details](restore.md)); confirm backup completed at least once |
| `arangorestore` container never starts | Init container still running or failed | `kubectl get pods -n <ns> -l restore-id=<id>` — init must show `Completed` first (Job ordering, not bug) |
