# Filestash First Rollout Order

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

Do the first rollout on `experiments`. Start `dcr-kube1` only after every
step on `experiments` passes, including the restore test and the
[release tests](../../plans/2026-11-17-filestash-implementation.md).

## Table of Contents

- [Differences Between the Clusters](#differences-between-the-clusters)
- [Order on experiments](#order-on-experiments)
- [Order on dcr-kube1](#order-on-dcr-kube1)
- [Values to Decide Before the First Rollout](#values-to-decide-before-the-first-rollout)

## Differences Between the Clusters

| Item | `experiments` | `dcr-kube1` |
|---|---|---|
| Namespace | `dev` | `prod` |
| Public route | nginx Ingress in the `filestash` stack, off until step 10 | Gateway route `filestash` in the `gateway` stack, off until step 10 |
| Admin block | Ingress `filestash-admin-block` | Gateway rule on `/admin` |
| TLS | kcert, Secret `filestash-tls-secret` | Gateway listener, Secret `filestash-tls` |
| Direct file URL host | `storage.dictybase.dev` | `storage.dictycr.org` |
| State PVC size | `2Gi` | `5Gi` |
| Backup bucket | `restic-filestash-backup-dcr-experiments` | `restic-filestash-backup-dcr-kube1` |

## Order on experiments

1. Plugin license review and `just filestash build-plugins` (once for both clusters).
2. Guide Section 3: set `applicationURL`, `backup.maxAgeHours`, and the plugin pins. The Ingress stays off (`ingress.enabled: false`).
3. `create-secret`, `create-buckets`, `create-users`, then commit both stack files.
4. `deploy`.
5. `configure-backup` (it uses `credentials/dcr-experiments/sa-manager.json`), `deploy-backup`, `backup-now`, then commit `filestash-backup/Pulumi.experiments.yaml`.
6. Admin console: SMTP, two test upload links (Alice and Bob), and the public link.
7. `publish-bucket`, then one `publish` of a test file.
8. Isolation tests from the [design](../../plans/2026-11-17-filestash-design.md), through the port-forward.
9. `restore-state --target-namespace filestash-restore-test`, then `restore-cleanup`.
10. Set `ingress.hosts.0` to the real host and `ingress.enabled` to `true`, `deploy`, add the DNS record, `probe-admin`, `probe-public`.
11. `verify`.

## Order on dcr-kube1

The same steps, with these changes:

- Step 5: `configure-backup` uses `credentials/dcr-kube1/sa-manager.json`.
- Step 10: the Ingress stays off (`ingress.enabled: false`).
- Step 10: set `routes.filestash.hosts.0` and `routes.filestash.enabled` in the `gateway` stack, then `just gateway deploy`.
- Use the same plugin bundle tag and digest as `experiments`.

## Values to Decide Before the First Rollout

| Value | Where |
|---|---|
| Filestash host for each cluster | `applicationURL`, and the Ingress or Gateway host |
| SMTP server, port, user, password, and sender | Admin console |
| `backup.maxAgeHours` (recovery point target) | `filestash/Pulumi.<stack>.yaml` |
| Retention period for `uploads` | `purge-uploads --older-than` |
| Plugin licenses and reviewer | `filestash/plugins/plugins.lock.yaml` |
| Owner and frequency of `just filestash verify` | Team runbook |
