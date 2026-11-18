# Filestash First Rollout Order

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

Do the first rollout on `experiments`. Start `dcr-kube1` only after every
step on `experiments` passes. This includes the isolation tests, the restore
test, and the [release tests](../../plans/2026-11-17-filestash-implementation.md).
The public route goes on last, after the isolation tests and the restore test.

## Table of Contents

- [Differences Between the Clusters](#differences-between-the-clusters)
- [Order on experiments](#order-on-experiments)
- [Order on dcr-kube1](#order-on-dcr-kube1)
- [Isolation Tests](#isolation-tests)
- [Values to Decide Before the First Rollout](#values-to-decide-before-the-first-rollout)

## Differences Between the Clusters

| Item | `experiments` | `dcr-kube1` |
|---|---|---|
| Namespace | `dev` | `prod` |
| Public route | nginx Ingress in the `filestash` stack, off until step 10 | Gateway route `filestash` in the `gateway` stack, off until step 10 |
| Route keys to set at step 10 | `ingress.hosts.0`, `ingress.enabled` in `filestash/Pulumi.experiments.yaml` | `routes.filestash.hosts.0`, `routes.filestash.enabled` in `gateway/Pulumi.dcr-kube1.yaml` |
| Admin block | Ingress `filestash-admin-block` | Gateway rule on `/admin` |
| TLS | kcert, Secret `filestash-tls-secret` | Gateway listener, Secret `filestash-tls` |
| Direct file URL host (`probe-public --host`) | `storage.dictybase.dev` | `storage.dictycr.org` |
| State PVC size | `2Gi` | `5Gi` |
| `sa-manager` key for `configure-backup` | `credentials/dcr-experiments/sa-manager.json` | `credentials/dcr-kube1/sa-manager.json` |
| Backup bucket | `restic-filestash-backup-dcr-experiments` | `restic-filestash-backup-dcr-kube1` |

## Order on experiments

Each step names the recipe. The guide section with the same recipe has the command.

1. `lock-office-deps` (only when `office-deps.lock.yaml` has an empty `sha256`), the [license and review procedure](plugins.md#license-and-review-procedure), then `build-plugins`. Do this once for both clusters.
2. Set `applicationURL`, `backup.maxAgeHours`, and the plugin pins. Refer to [Stack Values to Set](install.md#stack-values-to-set). The Ingress stays off (`ingress.enabled: false`).
3. `create-secret`, `create-buckets`, `create-users`. Then commit `filestash/Pulumi.experiments.yaml`.
4. `deploy`.
5. `configure-backup`, `deploy-backup`, `backup-now`. Then commit `filestash-backup/Pulumi.experiments.yaml`.
6. `login-keys --role intake` and `login-keys --role public`. Then, in the admin console: SMTP, two test upload links (Alice and Bob), and the public link.
7. `publish-bucket`, then one `publish` of a test file. Record the key of the test file for step 10.
8. The [isolation tests](#isolation-tests), through the port-forward.
9. `restore-state --target-namespace filestash-restore-test`, then `restore-cleanup --target-namespace filestash-restore-test`.
10. Set `ingress.hosts.0` to the real host and `ingress.enabled` to `true`. Run `deploy` and commit the stack file. Add the DNS record. Then run `probe-admin` and `probe-public` from outside the cluster.
11. `verify`.

## Order on dcr-kube1

Do the same steps, with these changes:

- Step 1: skip it. Use the same plugin bundle tag and digest as `experiments`.
- Step 5: `configure-backup` uses `credentials/dcr-kube1/sa-manager.json`.
- Step 10: the Ingress stays off (`ingress.enabled: false`).
- Step 10: set `routes.filestash.hosts.0` and `routes.filestash.enabled` in the `gateway` stack. Then run `just gateway deploy` and commit `gateway/Pulumi.dcr-kube1.yaml`.
- Step 10: the `probe-public` host is `storage.dictycr.org`.

## Isolation Tests

Do these tests on each cluster, before the public route goes on. The
[design test table](../../plans/2026-11-17-filestash-design.md#11-tests-release-blockers)
gives each test and its expected result. A failed test is a release blocker.

1. Run `just filestash admin`. Keep the port-forward open.
2. Get the intake keys with `just filestash login-keys --role intake`.
3. Open `http://localhost:8334/` and log in as `filestash-intake`.
4. Make one upload link on `/uploads/alice/` for the Alice address. Give it an expiry date.
5. Make one upload link on `/uploads/bob/` for the Bob address. Give it an expiry date.
6. Open each link at `http://localhost:8334/s/<id>`. The link that Filestash shows has the public host, and that host has no DNS record yet.
7. Do each test in the "Isolation tests" table of the design. Record each result.
8. Do the tests of the public link that use the Filestash link only. `probe-public` does the direct URL tests at step 10.
9. Delete the two test links. Then run `just filestash audit-shares`.

When a test fails, do not turn on the route. Record the result and ask for a
security review.

## Values to Decide Before the First Rollout

| Value | Where |
|---|---|
| Filestash host for each cluster | `applicationURL`, and the Ingress or Gateway host |
| SMTP server, port, user, password, and sender | Admin console |
| Two test email addresses (Alice and Bob) | [Isolation Tests](#isolation-tests) |
| `backup.maxAgeHours` (recovery point target) | `filestash/Pulumi.<stack>.yaml` |
| Retention period for `uploads` | `purge-uploads --older-than` |
| Plugin licenses and reviewer | `filestash/plugins/plugins.lock.yaml` |
| Owner and frequency of `just filestash verify` | Team runbook |
