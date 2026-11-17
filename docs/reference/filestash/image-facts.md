# Filestash Image Facts

Back to: [Filestash Design](../../plans/2026-11-17-filestash-design.md)

**Status**: Source facts. The operator skipped the local container test.
Each fact comes from the Filestash source at revision `7509e60`
(2026-11-17), the vendor documentation, or Docker Hub. The first rollout on
`experiments` checks each fact against the running image. A fact marked
**Unverified** is a release-test item.

## Images

| Image | Tag | Digest | Note |
|---|---|---|---|
| `machines/filestash` | `latest` | `sha256:89e70f0fccc4dcce3f47e5d490ee961f912a5ffd4e30b98216e3e37e469035e2` | Pushed 2026-11-16. The vendor publishes no version tags. The digest is the pin. |
| `keinos/sqlite3` | `3.54.1` | `sha256:b577e65892c6468d94b390097eda4488e05253470c161bdea68a05abb961fa17` | State backup copy. |
| `alpine/kubectl` | `1.35.4` | `sha256:9ccd823647622b620dce092d0f1406b8b41d152ce08ac963e3d4683770b14dfd` | State backup scale steps. Cluster `kubectl` is 1.35. |
| `busybox` | `1.37.0` | `sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec` | Seed init container and plugin bundle base. |

## Behavior

| Fact | Result | Source |
|---|---|---|
| Port and user | Port `8334`, user `filestash` (UID 1000 in the vendor Helm chart). | `docker/Dockerfile`, chart `0.1.4` |
| Health path | `GET /healthz` | `server/routes.go` |
| State paths | `state/config/`, `state/db/`, `state/plugins/`, `state/log/`, `state/search/`, `state/certs/` under `/app/data`. Temporary files go to `/app/data/cache/`. | `server/pkg/env/constants.go` |
| `ADMIN_PASSWORD` | Stored as `auth.admin`, compared with bcrypt. A plaintext value does not work. Use a bcrypt hash. | `server/pkg/config/config.go`, vendor install guide |
| No `ADMIN_PASSWORD` | `/admin` shows the first-run password page. **Unverified.** | Vendor install guide |
| `APPLICATION_URL` | Sets `general.host`. | `server/pkg/config/config.go` |
| `general.secret_key` | Generated at first start (16 characters) and saved in `config.json`. Shares and sessions depend on it. A restore must keep `config.json`. | `server/pkg/config/config.go` |
| `CONFIG_SECRET` | Key for encrypted fields in `config.json`. | `server/pkg/config/config_state.go` |
| Seed connection shape | `{"type":"s3","label":"<label>","endpoint":"<url>","region":"<region>"}` in `connections`. Pinned fields must match at login. **Unverified** in the image. | `server/pkg/session/handler_auth_direct.go`, configuration guide |
| Share URL form | `/s/<id>`. The same prefix also serves WebDAV for the share. | `server/routes.go` |
| Upload without `s3:GetObject` | **Unverified.** Release-test item. | Not testable from source |
| Range through the share | **Unverified.** Release-test item. | Not testable from source |
| Browser shell | `/admin/tty/`, off unless `features.server.console_enable` is `true`. | `server/plugin/plg_handler_console` |
| F4 web console | A frontend widget. It sends the same file API calls as the page, with the same session rights. It is not a server shell. | `server/plugin/plg_widget_console` |
| Search timeout key | `features.search.explore_timeout` (seconds). | `server/plugin/plg_search_stateless/config.go` |
| Telemetry key | `log.telemetry` | `server/pkg/config/config.go` |
| Compiled plugins in the image | Includes `plg_search_stateless`, `plg_image_c`, `plg_backend_s3`, `plg_handler_console`, `plg_widget_console`. | `server/plugin/index.go` |
| Runtime plugins | ZIP files in `state/plugins/`, loaded at start. | `server/pkg/env/constants.go`, plugin guide |

## Admin Path Routing

The router is `gorilla/mux`. These results come from its default behavior.
The live probe (`just filestash probe-admin`) is the gate.

| Request path | Reaches admin content |
|---|---|
| `/admin`, `/admin/`, `/admin/api/*` | Yes |
| `/Admin` | No. The router is case-sensitive. |
| `//admin`, `/./admin`, `/x/../admin` | Redirect (HTTP 301) to `/admin`. The redirect then hits the Gateway block. |
| `/%61dmin`, `/admin%2Fapi/config` | **Unverified.** The router matches the decoded path. The Gateway match on encoded paths is a release-test item. |

## Conditional Write

MinIO supports `If-None-Match: *` on `PutObject` from release
`2024-11-07`. The cluster runs `2025.7.23`. `mc cp` has no flag for this
header. **Ruling:** the publication recipe uses the single-writer Lease and
the `mc stat` check. It does not send a conditional write.
