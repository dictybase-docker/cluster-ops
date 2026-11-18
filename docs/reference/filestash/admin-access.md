# Filestash Admin Access and Route

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Rules](#rules)
- [Gateway Clusters](#gateway-clusters)
- [Ingress Clusters](#ingress-clusters)
- [DNS Sequence](#dns-sequence)
- [just filestash probe-admin](#just-filestash-probe-admin)

## Rules

- Set `ADMIN_PASSWORD` before the route exists, so no visitor can do the first-run setup.
- A password alone is not enough. The internet must not reach `/admin` or `/admin/api/*`.
- Staff use the admin console only through `just filestash admin`.
- If `probe-admin` fails, turn the route off and report the variant.
- The browser shell `/admin/tty/` stays off (`features.server.console_enable=false`). `verify` checks it.

## Gateway Clusters

The `filestash` route in `gateway/Pulumi.<stack>.yaml` has `blockedPathPrefixes: [/admin]`. Each blocked prefix gets a rule with a `RequestRedirect` to `/` (HTTP 302) and no backend. Gateway API selects the longest prefix, so `/admin` and every path under it never reach Filestash. The route is `enabled: false` until [Turn On the Public Route](../../filestash-deploy.md#14-turn-on-the-public-route).

| Key | Value |
|---|---|
| `routes.filestash.hosts` | `<filestash-host>` in Git. Set `routes.filestash.hosts.0` to the real host at the route step. With the route on, a placeholder stops `just gateway deploy`. |
| `routes.filestash.tlsSecret` | `filestash-tls` |
| `routes.filestash.serviceName` / `servicePort` | `filestash` / `8334` |
| `routes.filestash.blockedPathPrefixes` | `/admin` (each value must start with `/`) |

## Ingress Clusters

Set `ingress.hosts.0` and `ingress.enabled` in the `filestash` stack at the route step. The stack then makes Ingress `filestash-admin-block` on `Prefix /admin` with `nginx.ingress.kubernetes.io/permanent-redirect: /`. Ingress `filestash` sets `proxy-body-size: 0`, `proxy-request-buffering: off`, and 3600-second read and send timeouts. A 60-second proxy timeout breaks large uploads.

## DNS Sequence

1. Deploy with the route off. Do the [isolation tests](first-run.md#isolation-tests) through `just filestash admin`. Then do the [restore test](backup-restore.md#test-restore).
2. Turn the route on for the cluster:
   - Gateway clusters: set `routes.filestash.hosts.0` and `routes.filestash.enabled` in the `gateway` stack, then run `just gateway deploy`.
   - Ingress clusters: set `ingress.hosts.0` and `ingress.enabled` in the `filestash` stack, then run `just filestash deploy`.
3. Commit the stack file that changed.
4. Add the DNS `A` record. The certificate issuer makes the TLS Secret after the record resolves.
5. From a host outside the cluster, run `just filestash probe-admin --host <filestash-host>`.
6. From the same host, run `just filestash probe-public --host <minio-host> --key <dataset>/<file>`. Refer to [just filestash probe-public](operations.md#just-filestash-probe-public).

If a probe fails, set the `enabled` key back to `false` and deploy again.

## just filestash probe-admin

### What It Does

Checks from outside the cluster that no admin path variant reaches the admin console or the admin API. The recipe makes no change.

### Behavior

1. Sends `GET`, `POST`, `PUT`, `DELETE`, and `OPTIONS` with `curl --path-as-is` to each path in the table. `--path-as-is` sends each variant unchanged.
2. Compares each response with the pass rule of its row.
3. Fails closed. Each failed request prints `FAIL`, and the recipe exits non-zero at the end.

| Paths | Pass |
|---|---|
| `/admin`, `/admin/`, `/admin/api/session`, `/admin/api/config`, `/admin/api/logs`, `/admin/tty/` | A 3xx redirect to `https://<host>/` (the block rule). |
| `/Admin`, `//admin`, `/./admin`, `/x/../admin` | No admin content. |
| `/%61dmin`, `/%61dmin/api/session`, `/admin%2Fapi/config`, `/admin%2Fapi/session` | A 3xx or 4xx, and no admin content. |

Every path fails on no response, on the admin page marker `backoffice`, or on any JSON body with `"status"` (any status code).

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--host` | Yes | none | The public Filestash host, not the MinIO host. |

The recipe takes no `--stack` or `--namespace`. It uses the public URL only.
