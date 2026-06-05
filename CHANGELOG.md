# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Plugin catalogue : static fallback when no live agent is wired**.
  `/api/plugins/catalogue` previously returned an empty list when
  `live == nil`, which made the superadmin Plugins panel look
  broken in dev / preview / detached mode. New
  `staticPluginCatalogue()` in `internal/server/api_plugins.go`
  mirrors the 14 plugins shipped under `weft/catalogue/*` so the
  list is always non-empty. The install drawer's POST still hits
  the live RPC, so a disconnected webui surfaces a clean
  "plugin install requires a wired weft-agent" — the catalogue
  remaining visible is the point. Test guard
  (`api_plugins_static_test.go`) locks the slug set so any HCL
  change forces an explicit update of the fallback.

- **Volume snapshots + backups in the dashboard**.
  - Two new tabs on `VolumeDrawer` : *Snapshots* (list + per-row Revert / Restore / Backup / Delete actions, with backend-aware gating of Revert + Backup) and *Backups* (target URL input, list scoped to the volume, per-row Restore / Delete).
  - Two modals : `CreateSnapshotModal` and `CreateBackupModal` (scheme dropdown defaulting to `oci://`, scheme-aware placeholder URL ; explicit note that the passphrase is daemon-side env-only, never seen by the SPA).
  - Nine typed wrappers in `wclient` (`ListVolumeSnapshots` / `CreateVolumeSnapshot` / `RestoreVolumeSnapshot` / `RevertVolumeSnapshot` / `DeleteVolumeSnapshot` / `CreateVolumeBackup` / `ListVolumeBackups` / `DeleteVolumeBackup` / `RestoreVolumeBackup`).
  - Nine huma routes in `internal/server/api_volume_snapshots.go` + `internal/server/api_volume_backups.go`. Live-only with Audit + `userActionCtx` instrumentation ; no mock fallback (persistence is daemon-owned, mocking would lie).
  - Nine typed `api.ts` helpers + projection types (`VolumeSnapshotRow`, `VolumeBackupRow`) regenerated through `openapi-typescript`.
  - `Volume.backend` propagated end-to-end : wclient `ListVolumes` → row projection → drawer affordance gating + Backups-tab warning banner when the volume isn't block-backed.

- **Live per-VM firewall status badge** (`FirewallStatusBadge`).
  Subscribes to the synthetic `firewall.status` PlatformEvent the
  host-side `firewallpub.StatusReceiver` re-emits onto the existing
  `/api/events` SSE stream and renders a compact pill next to the
  VM status header in `MicroVMDrawer`. States : green (healthy +
  N user rules), amber (default-deny only), red (Degraded, with
  `LastError` on hover), grey (pending OR stale = no publish in
  > 35 s = 3 missed agent ticks). New `firewallStatus.ts` derived
  store + 8 vitest cases on the projection. Commits `ad5efe9`,
  `f93ca48`.

## [0.2.0] - 2026-06-02

v0.2.0-track work since `v0.1.0` (`29a98ee`).

### Added

- **Scheduling-rule editor** : Inventory section gains AZs + Racks
  + Hosts CRUD with an isometric map (`15af003`), then re-organised
  with the Hosts tree above the map (`d242543`). Inventory tree view
  rolled into Admin (`a8e8fd7`).
- **Tenant dashboard** : pagination of `microvms` lists via
  `getAllRows` for fleets > 1000 (`820d65d`), default cap raised in
  step with the huma maximum (`2cff24f`).
- **Federation peers page** + **plugin install drawer**
  (`3d52434`).
- **Audit-log viewer** : tail endpoint, admin browser, and the
  auth / inventory trail surfaced to operators (`8723508`).
  Audit emits OIDC auth-lifecycle events `login.start`,
  `callback.failed`, `callback.success`, `logout` (`1a76a08`).
  Re-landed with a hardened TLS strict config (`c68b1f1`).
- **State-file snapshots** : pre-mutation rotation under
  `<path>.history/` so the operator can roll back a bad write
  (`a83e7d7`).
- **Inventory persistence** : CRUD endpoints + tree
  Add/Edit/Delete + arch coverage (`58a0634`), AZ/Rack/Host rows
  persisted to JSON across restarts (`accca61`). Mock layer also
  persists DNS, security-groups, scripts (`57f4db7`).
- **Inventory view modes** : default to 2D rack-elevation view,
  isometric 3D behind a toggle (`563c8b0`).
- **Deterministic ordering** : `/api/resources/{azs,racks,hosts}`
  return rows in deterministic order so diffs are stable
  (`aa19813`).
- **CSRF protection** : cross-origin mutations on `/api/*` rejected
  via Origin/Referer check (`79b45d2`).
- **Auth callback throttle** : per-IP failure rate-limit on the
  OIDC callback path + concurrency proof for inventory writes
  (`95f4f51`).
- **Graceful shutdown** : `BaseContext` cancelled on SIGTERM so SSE
  streams drain in < 1 s (`3bb7e4e`).
- **Hardening** : body-size cap on `/api/*` ; `readyz` probes
  state-file writability (`f4dfb1c`).
- **Deploy story** : new prod knobs surfaced in the systemd unit +
  README (`21e56af`).
- **Reproducible build + supply chain** : `SOURCE_DATE_EPOCH`-pinned
  bit-reproducible OCI image (`6f69d38`).
- **Real logo + favicon** ; early-dev banner dropped (`0e7c42e`).
- **BSD 3-Clause LICENSE** (`fb4fce2`).

### Changed

- **Auth coverage** lifted from ~24 % → ~83 % via the auth-hardening
  pass (CSRF + rate-limit + token-refresh + state snapshots + audit
  trail). `internal/auth` now meets the project-wide Plan-B coverage
  target.
- Admin Inventory category absorbed into Admin proper ; the Tree
  view is now labelled "Inventory" (`a8e8fd7`).
- **Hugely-improved OpenAPI** : the huma-generated spec is now
  rich enough that `openapi-typescript` + `openapi-fetch` regenerate
  a usable TS client end-to-end ; the v0.1.0 stub-routes are now
  fully described.

### Fixed

- microVM list correctly paginates past 1000 rows instead of
  silently truncating (`2cff24f` + `820d65d`).

## [0.1.0] - 2026-05-31

### Added

- **Rate limiter** (`internal/ratelimit`) wired into the handler chain :
  token bucket per session.Subject when authenticated, per-IP otherwise.
  Defaults to 100 rps / burst 50 per authenticated user, 20 rps / burst 10
  per IP. 429 response carries `Retry-After`. Lazy idle eviction keeps the
  per-key map bounded ; X-Forwarded-For honoured only when
  `cfg.TrustProxies`.
- **OIDC access-token refresh** (`internal/auth`) : sessions within 60 s of
  expiry get a transparent token exchange via the OAuth2 TokenSource.
  Refresh-token rotation honoured ; revoked tokens fall through to the
  existing 401 path. The middleware re-encodes the cookie with the new
  session ; in-flight requests run with the fresh credentials.
- **Persistent audit log** (`internal/audit`) : JSONL append-only,
  mutex-guarded, size-based rotation (default 100 MiB → `<path>.<RFC3339Nano>`).
  Configurable via `--audit-log-path` + `--audit-rotate-bytes` (env vars
  `WEBUI_AUDIT_LOG_PATH` / `WEBUI_AUDIT_ROTATE_BYTES`). Wired into 3 endpoint
  files (microvm lifecycle / volume CRUD / floating-IP + security-group
  rules) ; emissions sit AFTER `requireLiveCtx()` so audit captures only
  attempted backend mutations, not mock-mode no-ops.
- **Live-first weft-network integration** : when `--weft-network-socket` is
  set, the Networking panels (Routers, Load Balancers, DNS zones, DNS records,
  scheduling rules) route real RPCs to the sibling
  [`openweft/weft-network`](https://github.com/openweft/weft-network) daemon.
  Unreachable daemon → transparent mock fallback.
- **Plugin marketplace** (`internal/server/plugins.go`) : runtime-mutable
  catalogue of *-as-a-service modules. Resources gate on which plugins
  contribute them ; the sidebar shows / hides sections as the operator
  installs / uninstalls plugins.
- **Storage backend plugins** (CubeFS / Ceph / Garage / VersityGW), **registry
  backend plugins** (zot / Harbor), **load-balancer plugins removed** (Envoy
  + Caddy plugins were retired ; the data plane is now Caddy embedded in
  `weft-agent` per the
  [reverse-proxy decision](https://github.com/openweft/weft/blob/main/agent/proxy/doc.go)).
  All seed plugins are libre-licensed (Apache 2.0 / LGPL / AGPL / BSD).
- **Deploy artifacts** :
    - `Dockerfile` (3-stage : node SPA → Go binary with `//go:embed` → distroless/static
      nonroot, ~18 MB).
    - `deploy/systemd/weft-webui.service` (hardened, same baseline as
      weft-network).
    - `deploy/README.md` covering container + systemd patterns, OIDC env
      knobs, ReverseProxy / TLS terminating front.
- **CI** : docker smoke build on every push gates the Dockerfile.
- **Release workflow** : tag-driven (`v*`) multi-arch GHCR publish ;
  `workflow_dispatch` for retry-from-ref.
- Master-detail UI alignment across DNS / Security / Networks / Shares /
  Buckets / Registries / Routers / Load Balancers / Scripts. Per-row
  Edit-button + drawer instead of click-row-to-open. Compact tables with
  bottom-bar pagination + total count + reload.
- Deployments view (scheduling-rule → microVMs sub-table).
- Group-based SSH key authorization (keys derived from group memberships,
  NATS push delivers them).
- Database section (Vitess DBaaS plugin) ; Plugin-as-a-Service marketplace
  with category filter combo + alternatives panel + install-conflict
  warning.

### Tests

- `internal/audit/audit_test.go` : concurrent writes stay framed,
  size-rotation triggers correctly, JSON shape with omitempty.
- `internal/auth/*_test.go` : OIDC refresh happy / revoked / no-refresh-token
  paths, middleware bypass cases, session NeedsRefresh boundary.
- `internal/ratelimit/ratelimit_test.go` : allow/deny per key, IP namespace
  vs user namespace isolation, XFF honouring, Retry-After header shape.
- `internal/server/audit_e2e_test.go` : the Audit() helper correctly pulls
  Subject / Project / RemoteIP / RequestID from the context, stamps Result
  + Error based on the err argument.
