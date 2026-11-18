# Filestash for MinIO — Design

**Status**: Design for approval. Nothing in this document is deployed.
**Date**: 2026-11-17
**Cluster**: `dcr-kube1` (namespace `prod`)
**Source check**: Filestash `master` at revision `7509e60` (2026-11-17).

## 1. Intent

The project needs a web file manager in front of the MinIO on `dcr-kube1`.
The file manager has two jobs:

1. **Collaborator uploads.** Outside collaborators send files to the project.
   A collaborator does not get a MinIO account or a Filestash account.
2. **Public datasets.** Any person can browse and download a published
   dataset. The person does not need an account.

Large downloads occur sometimes, but not every day. One Filestash pod is the
first design. Capacity tests (Section 11) decide if one pod is sufficient.

## 2. Goals, Non-Goals, Anti-Goals

**Goals**

- Each collaborator has a private upload link to a private prefix.
- Only the email addresses on the link can use it.
- The public can browse and download the `downloads` bucket through one link.
- Each published file has a direct, stable URL, for example
  `https://storage.dictycr.org/downloads/strains-2026/table.csv`. People can
  cite the URL and use it in scripts (`curl`, `wget`).
- Staff review each upload before publication.
- The deployment follows the repository pattern: a Pulumi Go project, a
  Gateway route, a `just` module, a guide, reference documents, and a
  workflow diagram.

**Non-goals**

- Single sign-on for staff. Logto is off. A later change can add it.
- Anonymous S3 access to the `uploads` bucket.
- Anonymous S3 list access to any bucket. The public browses folders only
  through Filestash, because MinIO cannot show an HTML folder list.
- High availability. Filestash keeps its state in SQLite on one disk.
- A content delivery network for large public files.

**Anti-goals** (failures, even if the system works)

- An upload goes directly into a public location.
- A collaborator can read, list, or replace the files of another collaborator.
- The Filestash service uses the MinIO root credential.
- The admin console or the first-run setup page is open to the internet
  without a password.
- The login page lets a person connect Filestash to a server of their choice.

## 3. Constraints

- The Cilium Gateway `dictycr-gateway` exposes every public service.
  It ends TLS with Let's Encrypt certificates.
- MinIO is one pod on the `stateful-db` pool. The Service is `minio:9000`.
  The public S3 hosts are `storage.dictycr.org` and `slimestorage.dictycr.org`.
- The MinIO backup (`restic` to GCS) copies all buckets and their anonymous
  policies, as the reference document states. A restore test must confirm
  that the new buckets are in the backup.
- Recipe arguments default from the cluster environment. No recipe hardcodes
  a cluster value (`AGENTS.md`).
- Go code in this repository uses the `fp-go` house style (`AGENTS.md`).
- Filestash email verification needs an SMTP server. The repository has no
  SMTP configuration now.

## 4. Verified Filestash Behavior

These facts come from the source at revision `7509e60`. The source does not prove the contents of a released image. The builder repeats each check against the selected image tag.

| Behavior | Source file | Effect on the design |
|---|---|---|
| The image runs `/app/filestash` on port `8334` as user `filestash`. | `docker/Dockerfile` | The Service targets port `8334`. The pod runs as non-root. |
| The configuration is in `/app/data/state/config/config.json`. Shares are in the SQLite file `share.sql` under the state directory. | `server/pkg/share/db.go` | The state directory needs a PVC and a backup. |
| `ADMIN_PASSWORD` and `APPLICATION_URL` are environment variables. The admin field has the type `bcrypt`. | `server/pkg/config/config.go` | The Secret holds a bcrypt hash. The source does not prove the format in the image. The builder confirms it against the selected image. |
| `CONFIG_SECRET` sets the key that encrypts secrets in `config.json`. | `server/pkg/config/config_state.go` | The Secret holds `CONFIG_SECRET`. A change of this value breaks the stored secrets. |
| A login is permitted only for a configured connection. Each pinned field, for example `endpoint`, must match. A pinned `path` limits the login to that prefix. | `server/pkg/session/handler_auth_direct.go` | Pin `type`, `endpoint`, and `region`. A person cannot point Filestash at a different server. |
| A share stores the encrypted session of the staff member who made it. The session contains the MinIO credential. The share uses that credential. | `server/pkg/share/handler.go` | The credential at share creation sets the upper limit of the share. `share.sql` holds encrypted credentials. Protect the PVC and its backups as secret data. |
| A share has `path`, `users`, `password`, `expire`, `can_read`, `can_write`, and `can_upload`. | `server/pkg/core/share.go` | Upload links use `can_upload` only. The public link uses `can_read` only. |
| An email share sends a six-character code through SMTP. If SMTP fails, the share returns `Couldn't send email`. | `server/pkg/share/model.go` | Email shares need SMTP before release. |

## 5. Approaches

| Approach | Summary | Advantages | Disadvantages |
|---|---|---|---|
| **A. One Filestash instance (selected)** | One pod serves upload links and the public link. | One route, one PVC, and one backup. | Large public downloads and uploads share one pod. |
| B. Filestash and a proxy | Filestash serves uploads. A stateless `rclone serve http` pod serves public HTML pages. | Public traffic is separate. The proxy can scale. | Two components to deploy and document. Plain public pages. |
| C. Two Filestash instances | One instance for uploads and one for public data. | A failure in one does not stop the other. | Two PVCs, two configurations, and two backups. |

The user selected **A**. If the capacity tests fail, add the proxy from **B**.
The `downloads` bucket does not change, so the move is small.

## 6. Architecture

```
Collaborator ──upload link + email code──┐
                                         ▼
Public ──browse (read-only link)──► Gateway ──► Filestash pod ──► MinIO (minio:9000)
                          files.<domain>      (PVC: state)      ├─ uploads   (private)
Staff ──login with own credential────────┘                      └─ downloads (public read)
                                                                      ▲
Public ──direct file URL──► Gateway ── storage.dictycr.org ───────────┘
```

- Filestash connects to MinIO through the in-cluster Service. It does not use
  the public S3 host.
- The public has two paths to the data:
  - **Browse:** the Filestash read-only link shows folders and file lists.
  - **Download:** each file has a direct URL on `storage.dictycr.org`.
    MinIO sends the file, so large downloads do not go through Filestash.
- The `downloads` bucket gets an anonymous policy with `s3:GetObject` on
  `arn:aws:s3:::downloads/*` only. It has no `s3:ListBucket`, so a direct
  S3 request cannot list the bucket. This is the same model as the
  current `editor` bucket.
- The `uploads` bucket stays private. It has no anonymous policy.

## 7. Two Permission Layers

Layer 1 is the MinIO policy of each credential. Layer 2 is the Filestash share
flags and the share path. Layer 1 limits the damage if layer 2 fails.

| MinIO user | Who uses it | MinIO policy | Used for |
|---|---|---|---|
| `filestash-intake` | Staff, to create upload links | `PutObject` on `uploads/*`. `ListBucket` on `uploads`. No `GetObject`. No `DeleteObject`. | Collaborator upload links |
| `filestash-public` | Staff, to create the public link | `GetObject` and `ListBucket` on `downloads` only. | The public read-only link |
| `filestash-operator` | Staff, for review in Filestash | `GetObject`, `DeleteObject`, and `ListBucket` on `uploads`. `GetObject` and `ListBucket` on `downloads`. No `PutObject` on `downloads`. | Review and delete uploads. |
| `filestash-publisher` | The `just filestash publish` and `unpublish-file` recipes only | `GetObject` and `ListBucket` on `uploads`. `PutObject`, `DeleteObject`, `GetObject`, and `ListBucket` on `downloads`. | Copy approved files to `downloads`. Never used for a Filestash login. |

Rules:

- No Filestash path uses the MinIO root credential.
- Staff make each upload link only in a session with `filestash-intake`.
  Staff make the public link only in a session with `filestash-public`.
- Staff never make a share in a `filestash-operator` session.
- Only `filestash-publisher` can write to `downloads`. No Filestash session
  can change a published file.
- The `just` module creates the four users and their policies.

**Where each credential exists**

| Location | Form | Content |
|---|---|---|
| Kubernetes Secret `filestash-users` | Base64 in the cluster | The four MinIO keys. The `just` module reads them. |
| Operator password store | Encrypted | The three MinIO keys for staff login. Not `filestash-publisher`. |
| `share.sql` on the PVC | Encrypted with the Filestash secret key | A copy of the session credential in each share. |
| `config.json` on the PVC | Encrypted fields | SMTP password only. No MinIO key. |
| Git repository | None | Only Secret names and key names (pointers). |

**Open item for the builder:** Filestash can need `GetObject` or
`HeadObject` to finish an upload. If the upload test fails without
`GetObject`, stop. Record the cause with logs. Bring it to a security review
and to the user. Do not add the permission without explicit approval.

## 8. Bucket Layout

| Bucket | Object key prefix | Example object key | Filestash path |
|---|---|---|---|
| `uploads` | `<collaborator>/` | `alice/file.csv` | `/uploads/alice/` |
| `downloads` | `<dataset>/` | `strains-2026/table.csv` | `/downloads/strains-2026/` |

The `uploads` bucket holds uploads before review, with one prefix for each
collaborator. The `downloads` bucket holds published data. In this document,
`uploads/alice/` means bucket `uploads` and prefix `alice/`.

The user selected the bucket names `uploads` and `downloads`. The names are
part of each published URL, so they must not change after the first release.

## 9. Workflows

**Collaborator upload**

1. Staff log in with `filestash-intake` and open `uploads/<collaborator>/`.
2. Staff make a share with `can_upload` only, the collaborator email in
   `users`, and an expiry date.
3. The collaborator opens the link, enters the email, and enters the code.
4. The collaborator uploads files. The collaborator cannot list or read files.

**Review and publication**

1. Staff log in with `filestash-operator` and examine `uploads/<collaborator>/`.
2. Staff run `just filestash publish` to copy approved files to
   `downloads/<dataset>/`. The recipe uses `mc` in the cluster and logs each copy.
   The recipe sets the correct `Content-Type` on each object, so a browser can
   show or play the file. The recipe refuses to replace an existing object
   key. The recipe uses a conditional write if MinIO supports it, and a
   single-writer lock in all cases. These controls apply only to the
   recipe. An administrator with the MinIO root credential can still
   replace an object, so the design does not promise immutable URLs.
3. Staff delete the reviewed files from `uploads`, or keep them for the
   retention period.

**Public access**

1. Staff log in with `filestash-public` and make one share on `downloads/`
   with `can_read` only and no expiry.
2. The project publishes that link. Any person can browse and download.
3. The project can also publish a direct file URL, for example in a paper:
   `https://storage.dictycr.org/downloads/<dataset>/<file>`.

**Revocation**

1. Staff delete the share in the Filestash share list.
2. A new request on the old link fails at once.
3. For an emergency, rotate the MinIO credential. Every share made with that
   credential then stops. Staff must make the valid shares again and send
   the new links.
4. To stop all public access, delete the public link and remove the
   anonymous policy on `downloads` with `just filestash unpublish-bucket`.
5. To remove one published file, run `just filestash unpublish-file`. Its direct URL then
   returns HTTP 404.
6. Revocation cannot recall files that a person downloaded before the change.

## 10. Deployment Parts

| Part | Location | Notes |
|---|---|---|
| Pulumi project | `filestash/` | Copy the `dcr-mcp/` shape: `config.go`, `deployment.go`, `service.go`, a PVC, and tests. Use the `fp-go` house style. |
| Stack template | `config/templates/filestash-stack.yaml.tmpl` | No cluster value in the template. |
| Scaffold recipe | `just gcp-pulumi scaffold-filestash-stack` | Same pattern as the other services. |
| `just` module | `just_modules/filestash.justfile` | The implementation plan gives the full recipe list. It includes `deploy`, `create-users`, `publish-bucket`, `unpublish-bucket`, `publish`, `unpublish-file`, `admin`, `backup-now`, `restore-state`, `verify`, and `teardown`. `publish-bucket` sets the anonymous `GetObject` policy on `downloads`. |
| Gateway route | `gateway/` | New route key `filestash`. Host and TLS Secret in the stack file. |
| Guide | `docs/filestash-deploy.md` | With workflow diagram. |
| Reference documents | `docs/reference/filestash/` | Install, users, shares, publication, backup, verify, troubleshooting. |

**Pod settings**

- One replica. Strategy `Recreate`, because the PVC is `ReadWriteOnce`.
- Image pinned to a tag and a digest. Not `latest`.
- Non-root user, read-only root file system where Filestash permits it.
- A PVC for `/app/data/state` on `dictycr-balanced`. Proposed size: 5Gi.
- The pod runs on the application pool, not the `stateful-db` pool.
- Environment from Secret `filestash`: `ADMIN_PASSWORD` (bcrypt hash) and
  `CONFIG_SECRET`. `APPLICATION_URL` from the stack configuration.

**Configuration**

- An init container writes a seed `config.json` only if the file does not
  exist. The seed has one S3 connection with pinned `type`, `endpoint`, and
  `region`. It has no credentials.
- After the first start, staff change settings in the admin console.
  The PVC backup keeps those changes.
- The SMTP password goes in through the admin console or through a recipe that
  reads a Secret. It does not go in the Git repository.

**Admin access (release gate)**

- `ADMIN_PASSWORD` is set before the Gateway route exists. Then no internet
  visitor can complete the first-run setup.
- A password alone is not a sufficient control. Before the route goes on,
  the internet must not reach the admin console or the admin API
  (`/admin` and `/admin/api/*`).
- The builder finds a Gateway or Filestash control that blocks those paths,
  and proves it with a test from outside the cluster. Staff then use the
  admin console through `kubectl port-forward` in a `just filestash admin`
  recipe.
- If no control blocks those paths, the route stays off. Bring the problem to
  the user.
- Enable `plg_security_authlock` for login failure limits, if the plugin
  works with this setup.

**DNS and TLS sequence**

1. Deploy Filestash with the route off. Do all tests through port-forward.
2. Turn the route on. The Gateway requests the certificate.
3. Add the DNS `A` record for the host.
4. Run `just filestash verify` for TLS and public tests.

## 11. Tests (Release Blockers)

**Isolation tests.** Use two test users, Alice and Bob, each with one upload link.

| Test | Expected result |
|---|---|
| Alice uses her link with her email | Upload succeeds into `uploads/alice/` only. |
| Alice uses Bob's link with her email | The code request fails. |
| A person with an unlisted email uses Alice's link | The code request fails. |
| Alice tries to list or download in her link | Denied. |
| Alice uploads a file name that already exists in her prefix | Record the result. If the old object is replaced, document it as known behavior. |
| Alice tries to write an object key in Bob's prefix | Denied. Bob's object does not change. |
| Alice uses `../` or an encoded path in an upload | The object stays inside `uploads/alice/`. |
| Alice sends direct API requests (not the web page) to the share endpoints | The same results as the web page. |
| Alice uses the WebDAV endpoint of the share | Denied, or the same limits as the web page. |
| Staff delete Alice's share, then Alice uses the link | Denied. |
| Staff delete Alice's share while Alice has an open verified session | The next request in that session fails. |
| Alice uses the link after the expiry date | Denied. |

**Public tests**

| Test | Expected result |
|---|---|
| Anonymous browse and download from the public link | Succeeds. |
| Anonymous upload, rename, or delete on the public link | Denied. |
| Anonymous access to `uploads` through any link or path | Denied. |
| Anonymous direct S3 request for an object in `uploads` | Denied. |
| Anonymous direct S3 request for an object in `downloads` | Succeeds, with the correct `Content-Type`. |
| Anonymous direct S3 list request on `uploads` or `downloads` | Denied. |
| Anonymous direct S3 upload or delete on `downloads` | Denied. |
| Range request on a direct file URL | HTTP 206. |
| Range request through the Filestash link | Record the result. HTTP 206 is the wanted result, but the design does not promise it. |
| `just filestash publish` with an object key that exists | The recipe stops. The object does not change. |
| Login form with a changed `endpoint` value | Denied. |

**Capability checks** (record the result, no promise)

- Resumable download with Range requests.
- Resumable or chunked upload.

**Capacity tests** (the values are proposals, not requirements)

- Upload one large file through the Gateway. Proposed test value: 10 GB.
  Record the time and the result. Look for Gateway timeouts.
- Run concurrent downloads during one upload. Proposed test value: 10 at a
  time. Do the test through the Filestash link and through the direct URLs.
  Record pod CPU, memory, and transfer rate for Filestash and MinIO.
- The user sets the pass values after the first results. If the results do
  not meet them, consider approach B.

**Backup and restore tests**

> **Superseded (2026-11-17).** The Velero-based backup in the first version of
> this design was replaced. Refer to
> [ADR 0001](../adr/0001-filestash-state-backup-with-restic.md) and
> [Backup and restore](../reference/filestash/backup-restore.md).

- Confirm that the MinIO backup includes the `uploads` and `downloads`
  buckets. Restore one object from each bucket from a snapshot.
- Run `just filestash backup-now`, then
  `just filestash restore-state --target-namespace filestash-restore-test`.
  Confirm that the shares and the configuration exist.
- Do the restore test again after each Filestash upgrade.

## 12. Operations

- **Upgrade:** Change the pinned image. Back up the PVC first. Run the
  isolation and public tests after the upgrade.
- **Rollback:** Set the previous image. If the state format changed, restore
  the PVC from the backup before the upgrade.
- **Monitoring:** Pod readiness, PVC free space, backup age, HTTP 5xx rate at
  the Gateway, and Filestash `AUDIT` log lines. The cluster has no alert
  system now. Until the user selects one, `just filestash verify` is a
  manual check.
- **Credential rotation:** Rotate one MinIO user at a time. Shares made with a
  rotated credential stop. Make them again.

## 11a. Clusters and State Disk Size

The Pulumi project has one stack file for each cluster:
`filestash/Pulumi.dcr-kube1.yaml` (namespace `prod`, Gateway route) and
`filestash/Pulumi.experiments.yaml` (namespace `dev`, nginx Ingress, because
the experiments cluster has no Gateway stack). The rollout starts on
`experiments`.

The stack file sets the PVC size in `storage.size`. The code has no default
size. The proposed values are `5Gi` for `dcr-kube1` and `2Gi` for
`experiments`. The PVC holds `config.json`, the SQLite files (small), the
logs, the plugin ZIP files, and 7 backup copies. MinIO holds the uploaded and
published files, not this PVC. The temporary directory `/app/data/cache` uses
an `emptyDir` with a 2Gi limit. Task 1 measures the real sizes. A PVC can
grow with `just filestash resize-state`, but it cannot shrink.

## 12a. Plugins

The user selected these plugins: the `apple` theme, the `adobe`, `biomed`,
`office`, and `photography` viewers, `plg_search_stateless`, and
`plg_image_c`.

- `plg_search_stateless` and `plg_image_c` are compiled into the official
  image. They need configuration only.
- The theme and the viewers are runtime ZIP files. Filestash loads them from
  `/app/data/state/plugins/` at start.
- A plugin bundle image carries the ZIP files. An init container copies them
  into the plugins folder at each start. Git holds the plugin list, and
  a plugin change is an image upgrade.
- `office` has no prebuilt ZIP. The bundle image builds it from the
  Filestash source and pins the LibreOffice WASM files by checksum.
- Each ZIP runs JavaScript in the browser of each public visitor. Each ZIP
  needs a pinned checksum, a license check, and a review.
- The browser shell at `/admin/tty/` stays off. `verify` checks it.
- `fasta`, `gff`, and `gff3` are not in the Filestash MIME table, and `vcf`
  maps to `text/vcard`. A later change can add a viewer for these files.
- The user selected this Pulumi project, not the vendor Helm chart. The
  chart uses `latest`, plain-text environment values, and Ingress only.

## 13. Deployment Inputs (Not Decided)

The builder must not invent these values. Each one needs a user decision.

| Input | Proposal |
|---|---|
| Public host name | `files.dictycr.org` |
| SMTP provider and sender address | Unresolved. Needed before the first email link. |
| Bucket names | `uploads` (private) and `downloads` (public read). User decision. |
| Largest upload size | 10 GB |
| Default upload link expiry | 30 days |
| Retention in `uploads` after review | 90 days. Deployment blocker until the user confirms. |
| Recovery point and recovery time targets | Unresolved. Deployment blocker. |
| Alert system | Unresolved. The cluster has none now. Automated alerts are out of scope until the user selects one. |
| Owner and frequency of the manual `just filestash verify` check | Unresolved. Deployment blocker. |
| Filestash image tag and digest | The builder selects a release and records it. |

## 14. Calls Made in This Design

These are design choices, not user decisions. Change any of them.

- A private `uploads` bucket and a `downloads` bucket with anonymous
  `GetObject` only (user decision: direct file URLs). No anonymous list.
- Four MinIO users, so that each share has the smallest credential and only
  the publication recipe can write to `downloads`. The `filestash-publisher`
  user is a proposed security refinement, not a user decision.
- Publication through a `just` recipe, not through drag and drop in Filestash.
- A seed `config.json` from an init container, then admin console changes.
- The DNS record after the in-cluster tests.
- An online SQLite-consistent `restic` snapshot every 4 hours in a dedicated
  `filestash-backup` stack, with an age check in `verify` (ADR 0001).
- The admin password hash, `CONFIG_SECRET`, and the MinIO keys as
  KMS-encrypted stack values.
- The admin console blocked from the internet as a release gate.
