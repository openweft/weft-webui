# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
