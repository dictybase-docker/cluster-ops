# Filestash File Manager Deploy

The `filestash/` Pulumi project deploys **Filestash** (`machines/filestash`), a web file manager, in front of the cluster MinIO. Collaborators upload files through private upload links into the private bucket `uploads`. The public browses the bucket `downloads` through one read-only public link, and downloads each published file from a direct file URL such as `https://storage.dictycr.org/downloads/<dataset>/<file>`.
→ [Design](plans/2026-11-17-filestash-design.md) · [Glossary](../GLOSSARY.md)

**Two Pulumi projects make the service.** `filestash/` runs Filestash and holds the admin password hash, `CONFIG_SECRET`, and the MinIO user keys as KMS-encrypted stack values. `filestash-backup/` makes a `restic` snapshot of the Filestash state every 4 hours to `gs://restic-filestash-backup-<project-id>`. Velero does not back up the Filestash state, because a disk snapshot of a live SQLite file is not consistent.
→ [ADR 0001](adr/0001-filestash-state-backup-with-restic.md) · [Backup and restore](reference/filestash/backup-restore.md)

**Each project has two stack files.** `dcr-kube1` (namespace `prod`) uses a Gateway route. `experiments` (namespace `dev`) uses an nginx Ingress, because that cluster has no Gateway stack. Do every first rollout on `experiments`, then on `dcr-kube1`.
→ [First rollout order](reference/filestash/first-run.md)

**Status**: Production procedure. The stack files hold `<...>` placeholders and `backup.maxAgeHours: 0`. Validation rejects both. A deploy cannot start until you do [3. Set the Stack Values](#3-set-the-stack-values).

![Filestash Deploy Workflow](reference/filestash/filestash-deploy-workflow.png)

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Scaffold the Stacks](#1-scaffold-the-stacks)
- [2. Build the Plugin Bundle](#2-build-the-plugin-bundle)
- [3. Set the Stack Values](#3-set-the-stack-values)
- [4. Store the Admin Password and CONFIG_SECRET](#4-store-the-admin-password-and-config_secret)
- [5. Create the Buckets and Users](#5-create-the-buckets-and-users)
- [6. Deploy with the Route Off](#6-deploy-with-the-route-off)
- [7. Configure and Deploy the State Backup](#7-configure-and-deploy-the-state-backup)
- [8. Configure the Admin Console, SMTP, and Links](#8-configure-the-admin-console-smtp-and-links)
- [9. Make the downloads Bucket Public](#9-make-the-downloads-bucket-public)
- [10. Publish Files](#10-publish-files)
- [11. Remove a Published File](#11-remove-a-published-file)
- [12. Run the Isolation Tests](#12-run-the-isolation-tests)
- [13. Do a Restore Test](#13-do-a-restore-test)
- [14. Turn On the Public Route](#14-turn-on-the-public-route)
- [15. Upgrade and Roll Back](#15-upgrade-and-roll-back)
- [16. Clean Up Uploads and Audit Links](#16-clean-up-uploads-and-audit-links)
- [17. Teardown](#17-teardown)
- [18. Verify](#18-verify)
- [19. Troubleshooting](#19-troubleshooting)
- [20. Related Documents](#20-related-documents)

---

## Quick Reference

For experienced users. Full details are in the sections below.

| Intent | Sections |
|---|---|
| First rollout on a cluster | 1 to 10, 12, 13, 14, 18 |
| Publish reviewed uploads | 10 |
| Recover lost or damaged state | 13 (`--in-place yes`) |
| Upgrade Filestash | 15 |

```bash
# Enter the cluster environment first
just cluster-env --env <env> --cluster <cluster>

# 1. Stack files (a new cluster only)
just gcp-pulumi scaffold-filestash-stack --state-size 5Gi --host <filestash-host>
# 2. Plugin bundle
just filestash build-plugins --tag <yyyy.mm.dd>
# 3. Host, recovery point target, plugin pins
just gcp-pulumi set-config --folder filestash --key properties.applicationURL --value https://<filestash-host>
just gcp-pulumi set-config --folder filestash --key properties.backup.maxAgeHours --value <hours>
just gcp-pulumi set-config --folder filestash --key properties.plugins.image.tag --value <tag>
just gcp-pulumi set-config --folder filestash --key properties.plugins.image.digest --value sha256:<digest>
# 4. Admin password (hidden prompt) and CONFIG_SECRET, encrypted in the stack file
just filestash create-secret
# 5. Buckets and the four MinIO users
just filestash create-buckets
just filestash create-users
# 6. Deploy (route off)
just filestash deploy
# 7. State backup (configure-backup uses credentials/<project>/sa-manager.json)
just filestash configure-backup
just filestash deploy-backup
just filestash backup-now
# 8. Staff login keys, then the admin console: SMTP, upload links, public link
just filestash login-keys --role intake
just filestash login-keys --role public
just filestash admin
# 9. Public read on downloads
just filestash publish-bucket
# 10. Publish reviewed uploads
just filestash publish --source <collaborator>/<path> --dataset <name>
# 12. Isolation tests through the port-forward
just filestash admin
# 13. Restore test
just filestash restore-state --target-namespace filestash-restore-test
just filestash restore-cleanup --target-namespace filestash-restore-test
# 14a. Route on, dcr-kube1 only (gateway stack)
just gcp-pulumi set-config --folder gateway --key properties.routes.filestash.hosts.0 --value <filestash-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.filestash.enabled --value true
just gateway deploy
# 14b. Route on, experiments only (filestash stack)
just gcp-pulumi set-config --folder filestash --key properties.ingress.hosts.0 --value <filestash-host>
just gcp-pulumi set-config --folder filestash --key properties.ingress.enabled --value true
just filestash deploy
# 14c. After the DNS record resolves, from a host outside the cluster
just filestash probe-admin --host <filestash-host>
just filestash probe-public --host <minio-host> --key <dataset>/<file>
# 18. Verify
just filestash verify
```

Optional steps:

```bash
# 11. Remove one published file
just filestash unpublish-file --key <dataset>/<file> --confirm yes
# 13. Disaster recovery (destructive)
just filestash list-snapshots
just filestash restore-state --in-place yes --snapshot <id>
# 15. Upgrade, and roll back
just filestash upgrade --tag <tag> --digest sha256:<digest>
just filestash rollback
# 16. Retention and link audit
just filestash purge-uploads --older-than <days>
just filestash audit-shares
# Grow the state PVC
just filestash resize-state --size <quantity>
# 17. Teardown (destructive: deletes every share link)
just filestash teardown --delete-pvc yes
```

---

**Everything below runs inside the cluster-env sub-shell.**

- Enter it with `just cluster-env --env <env> --cluster <cluster>`. The sub-shell exports `PULUMI_STACK`, `PULUMI_BACKEND_URL`, `PULUMI_GCP_CREDENTIALS`, `PROJECT_ID`, and `KUBECONFIG`.
- Recipes default `--stack` to `$PULUMI_STACK` and `--namespace` to `properties.namespace` in `filestash/Pulumi.<stack>.yaml`.
- Outside the sub-shell, recipes fail with `Error: no stack name`.
- Commit `filestash/Pulumi.<stack>.yaml` and `filestash-backup/Pulumi.<stack>.yaml` after each step that changes them. The encrypted values in them are the only copy.

## 1. Scaffold the Stacks

Write `filestash/Pulumi.<stack>.yaml` and `filestash-backup/Pulumi.<stack>.yaml` from the cluster registry. Git already holds both files for `dcr-kube1` and `experiments`. Skip this step on those clusters.
→ [Install details](reference/filestash/install.md#just-gcp-pulumi-scaffold-filestash-stack)

```bash
just gcp-pulumi scaffold-filestash-stack --state-size 5Gi --host <filestash-host>
```

---

## 2. Build the Plugin Bundle

Build the plugin bundle. The recipe downloads and checks the DOCX editor ZIP.
→ [Plugin bundle details](reference/filestash/plugins.md#just-filestash-build-plugins)

```bash
just filestash build-plugins --tag <yyyy.mm.dd>
```

---

## 3. Set the Stack Values

Replace each `<...>` placeholder and set the recovery point target. Validation names every value that is still missing. The route host comes later, in [14. Turn On the Public Route](#14-turn-on-the-public-route).
→ [Placeholders for each cluster](reference/filestash/install.md#stack-values-to-set)

```bash
just gcp-pulumi set-config --folder filestash --key properties.applicationURL --value https://<filestash-host>
just gcp-pulumi set-config --folder filestash --key properties.backup.maxAgeHours --value <hours>
just gcp-pulumi set-config --folder filestash --key properties.plugins.image.tag --value <tag>
just gcp-pulumi set-config --folder filestash --key properties.plugins.image.digest --value sha256:<digest>
```

---

## 4. Store the Admin Password and CONFIG_SECRET

Store the bcrypt hash of the admin password and a random `CONFIG_SECRET` as encrypted values in the stack file. A second run keeps `CONFIG_SECRET`.
→ [Install details](reference/filestash/install.md#just-filestash-create-secret)

```bash
just filestash create-secret
```

---

## 5. Create the Buckets and Users

Create the private bucket `uploads`, the bucket `downloads`, and the MinIO users `filestash-intake`, `filestash-public`, `filestash-operator`, and `filestash-publisher`. The keys go into the stack file, encrypted.
→ [Users and policies](reference/filestash/users-and-policies.md)

```bash
just filestash create-buckets
just filestash create-users
```

---

## 6. Deploy with the Route Off

Deploy Filestash, its state PVC, and Secrets `filestash` and `filestash-users` from the stack values. The Gateway route and the Ingress stay off until [14. Turn On the Public Route](#14-turn-on-the-public-route).
→ [Install details](reference/filestash/install.md#just-filestash-deploy)

```bash
just filestash deploy
```

---

## 7. Configure and Deploy the State Backup

`configure-backup` makes the backup service account and its key with the `sa-manager` key file. Then deploy the bucket and CronJob, and make the first snapshot. Filestash stays up during each backup.
→ [Backup and restore](reference/filestash/backup-restore.md)

```bash
just filestash configure-backup
just filestash deploy-backup
just filestash backup-now
```

---

## 8. Configure the Admin Console, SMTP, and Links

Print the login keys of `filestash-intake` and `filestash-public`. Open the admin console through a port-forward and set SMTP. Then make each upload link as `filestash-intake` and the public link as `filestash-public`.
→ [Links and SMTP](reference/filestash/shares.md) · [What a collaborator sees](reference/filestash/collaborator.md)

```bash
just filestash login-keys --role intake
just filestash login-keys --role public
just filestash admin
```

---

## 9. Make the downloads Bucket Public

Set anonymous `s3:GetObject` on `downloads/*`. Anonymous list stays denied, and `uploads` stays private.
→ [Users and policies](reference/filestash/users-and-policies.md#bucket-policy)

```bash
just filestash publish-bucket
```

---

## 10. Publish Files

Copy reviewed uploads from `uploads/<source>/` to `downloads/<dataset>/`. The recipe refuses to replace a file and prints each direct file URL.
→ [Publish details](reference/filestash/publish.md)

```bash
just filestash publish --source <collaborator>/<path> --dataset <name>
```

---

## 11. Remove a Published File

**Optional.** Delete one published file. Its direct file URL then returns 404.
→ [Publish details](reference/filestash/publish.md#just-filestash-unpublish-file)

```bash
just filestash unpublish-file --key <dataset>/<file> --confirm yes
```

---

## 12. Run the Isolation Tests

Test two upload links (Alice and Bob) through the port-forward. Every test must pass before the public route goes on.
→ [Isolation tests](reference/filestash/first-run.md#isolation-tests)

```bash
just filestash admin
```

---

## 13. Do a Restore Test

Restore the newest snapshot into a new namespace with no egress for Filestash, check it, and delete the namespace. For disaster recovery, use `--in-place yes` instead.
→ [Backup and restore](reference/filestash/backup-restore.md#just-filestash-restore-state)

```bash
just filestash restore-state --target-namespace filestash-restore-test
just filestash restore-cleanup --target-namespace filestash-restore-test
```

---

## 14. Turn On the Public Route

Turn on the route in the block for your cluster. Then add the DNS `A` record. Run both probes from a host outside the cluster. If a probe fails, turn the route off again.
→ [Admin access and route](reference/filestash/admin-access.md#dns-sequence) · [Public probe](reference/filestash/operations.md#just-filestash-probe-public)

**On `dcr-kube1` (Gateway route in the `gateway` stack):**

```bash
just gcp-pulumi set-config --folder gateway --key properties.routes.filestash.hosts.0 --value <filestash-host>
just gcp-pulumi set-config --folder gateway --key properties.routes.filestash.enabled --value true
just gateway deploy
```

---

**On `experiments` (nginx Ingress in the `filestash` stack):**

```bash
just gcp-pulumi set-config --folder filestash --key properties.ingress.hosts.0 --value <filestash-host>
just gcp-pulumi set-config --folder filestash --key properties.ingress.enabled --value true
just filestash deploy
```

---

**On both clusters, after the DNS record resolves, from a host outside the cluster:**

```bash
just filestash probe-admin --host <filestash-host>
just filestash probe-public --host <minio-host> --key <dataset>/<file>
```

---

## 15. Upgrade and Roll Back

**Optional.** Upgrade makes a snapshot first, prints its id, and saves the current image. Run `rollback` only when the new release fails.
→ [Upgrade](reference/filestash/operations.md#just-filestash-upgrade) · [Rollback](reference/filestash/operations.md#just-filestash-rollback)

```bash
just filestash upgrade --tag <tag> --digest sha256:<digest>
just filestash rollback
```

---

## 16. Clean Up Uploads and Audit Links

**Optional.** List or delete old uploads, and check every share link against the rules.
→ [Operations](reference/filestash/operations.md#just-filestash-purge-uploads)

```bash
just filestash purge-uploads --older-than <days>
just filestash audit-shares
```

---

## 17. Teardown

**Destructive.** Deletes the state PVC: `config.json` and every share link. The buckets and the restic snapshots stay.
→ [Operations](reference/filestash/operations.md#just-filestash-teardown)

```bash
just filestash teardown --delete-pvc yes
```

---

## 18. Verify

Read-only checks: rollout, PVC use, plugins, browser shell off, and bucket policies. Also snapshot age, the backup Secret, and the Velero label on the state PVC.
→ [Verify details](reference/filestash/verify.md)

```bash
just filestash verify
```

---

## 19. Troubleshooting

→ [Full troubleshooting table](reference/filestash/troubleshooting.md)

---

## 20. Related Documents

- [Design](plans/2026-11-17-filestash-design.md), [implementation plan](plans/2026-11-17-filestash-implementation.md), and [ADR 0001](adr/0001-filestash-state-backup-with-restic.md)
- [Glossary](../GLOSSARY.md)
- [First rollout order](reference/filestash/first-run.md)
- [Install details](reference/filestash/install.md)
- [Users and policies](reference/filestash/users-and-policies.md)
- [Plugin details](reference/filestash/plugins.md)
- [Links and SMTP](reference/filestash/shares.md)
- [What a collaborator sees](reference/filestash/collaborator.md)
- [Publish details](reference/filestash/publish.md)
- [Admin access and route](reference/filestash/admin-access.md)
- [Backup and restore](reference/filestash/backup-restore.md)
- [Operations](reference/filestash/operations.md)
- [Verify details](reference/filestash/verify.md)
- [Image facts](reference/filestash/image-facts.md)
- [MinIO Deploy Guide](minio-deploy.md) and [Gateway Deploy Guide](gateway-deploy.md)
