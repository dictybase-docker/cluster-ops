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

The `filestash` route in `gateway/Pulumi.<stack>.yaml` has `blockedPathPrefixes: [/admin]`. Each blocked prefix gets a rule with a `RequestRedirect` to `/` (HTTP 302) and no backend. Gateway API selects the longest prefix, so `/admin` and every path under it never reach Filestash. The route is `enabled: false` until Section 10.

| Key | Value |
|---|---|
| `routes.filestash.hosts` | `<filestash-host>` |
| `routes.filestash.tlsSecret` | `filestash-tls` |
| `routes.filestash.serviceName` / `servicePort` | `filestash` / `8334` |
| `routes.filestash.blockedPathPrefixes` | `/admin` (each value must start with `/`) |

## Ingress Clusters

The stack makes Ingress `filestash-admin-block` on `Prefix /admin` with `nginx.ingress.kubernetes.io/permanent-redirect: /`. Ingress `filestash` sets `proxy-body-size: 0`, `proxy-request-buffering: off`, and 3600-second read and send timeouts. A 60-second proxy timeout breaks large uploads.

## DNS Sequence

1. Deploy with the route off. Do all link tests through `just filestash admin`.
2. Turn the route on. The certificate issuer makes the TLS Secret.
3. Add the DNS `A` record.
4. Run `probe-admin` from outside the cluster.

## just filestash probe-admin

Sends `GET`, `POST`, `PUT`, `DELETE`, and `OPTIONS` with `curl --path-as-is` to these paths: `/admin`, `/admin/`, `/Admin`, `//admin`, `/./admin`, `/x/../admin`, `/%61dmin`, `/%61dmin/api/session`, `/admin%2Fapi/config`, `/admin%2Fapi/session`, `/admin/api/session`, `/admin/api/config`, `/admin/api/logs`, `/admin/tty/`. A response that holds the admin page marker `backoffice`, or any JSON body with `"status"` (any status code), is a failure.

| Flag | Required |
|---|---|
| `--host` | Yes |
