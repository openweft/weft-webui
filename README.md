# weft-webui

The web dashboard for **Weft** — a Horizon-style UI for the platform's
object types (tenants, projects, hosts, AZs, racks, microVMs, networks,
subnets, routers, load balancers, DNS zones, security groups, volumes
+ snapshots + backups, shares, S3 buckets, SSH keys, scheduling rules,
flavors, scripts, plugins, registries, federation peers, audit log).

One Go binary serves the typed `/api/*` surface (~145 huma operations,
OpenAPI 3.1 published at `/api/openapi.json`) **and** an embedded
Svelte 5 single-page app — `//go:embed all:web/dist` bakes everything
into the binary, so the runtime image is ~18 MB and needs no
filesystem assets at startup.

- [Architecture](#architecture)
- [Build and run](#build-and-run)
- [API surface](#api-surface)
- [Live-first / mock fallback](#live-first--mock-fallback)
- [Three-portal split (user / tenant / infra)](#three-portal-split-user--tenant--infra)
- [Configuration](#configuration)
- [Telemetry](#telemetry)
- [Audit log](#audit-log)
- [Tests and CI](#tests-and-ci)
- [Container image (GHCR)](#container-image-ghcr)
- [Deployment](#deployment)

## Architecture

- **Backend** — Go (`net/http` + [huma v2](https://huma.rocks) for the
  typed REST surface), embeds the SPA via `//go:embed all:web/dist`.
  Up to **three** listeners (`user`, `tenant`, `infra`) each get their
  own huma router with a different set of registered operations and
  their own SPA bundle (see [Three-portal split](#three-portal-split-user--tenant--infra)).
- **Frontend** — Svelte 5 + Vite, Tailwind CSS v4, daisyUI v5.
  Generated TS client (`openapi-typescript` + `openapi-fetch`) lives
  at [`web/src/lib/api.gen.ts`](web/src/lib/api.gen.ts) and is regenerated
  by `task gen-api`. Both `web/openapi.json` and `api.gen.ts` are
  committed (`linguist-generated=true`) — CI fails on drift via
  `task check:drift`.
- **gRPC client** — [`internal/wclient`](internal/wclient) is the
  thin adapter to the sibling daemons : `Client` wraps
  [`weft-agent`](https://github.com/openweft/weft-agent) (127 RPC
  wrappers, proto v0.9.0) and `NetworkClient` wraps
  [`weft-network`](https://github.com/openweft/weft-network) (19 RPC
  wrappers for routers / LBs / DNS / scheduling rules). Both dial
  lazily, cache the gRPC conn, and stamp the signed-in user's bearer
  on outgoing metadata so `weft-agent` enforces per-user RBAC.
- **Auth** — OIDC Authorization Code + PKCE
  ([`internal/auth`](internal/auth)). Signed-cookie sessions, optional
  access-token refresh (60 s pre-expiry), per-IP throttle on the
  callback path, dev-mode bypass with a mock user.
- **Audit log** — JSONL append-only file with size-based rotation
  ([`internal/audit`](internal/audit)). OIDC lifecycle events
  (`login.start` / `callback.success` / `callback.failed` / `logout`),
  mutating API calls (microVM lifecycle, volume CRUD, security-group
  rules, bucket policies, …), and inventory writes leave a persistent
  trail. Tailable via `GET /api/audit-log`.
- **Rate limit** — per-Subject token bucket when authenticated,
  per-IP otherwise ([`internal/ratelimit`](internal/ratelimit)). 100 rps
  burst 50 for users, 20 rps burst 10 for anonymous IPs ; 429 with
  `Retry-After`.
- **Plugin catalogue** — runtime-mutable list of *-as-a-service modules
  (storage backends, registry backends, lifecycle plugins). Static
  fallback catalogue ([`api_plugins.go: staticPluginCatalogue`](internal/server/api_plugins.go))
  mirrors the 12 HCL plugin manifests so `/api/plugins/catalogue` stays
  non-empty when the live agent is unreachable or returns nothing.
- **Topology** — `weft-webui` talks to `weft-agent` over a Unix
  socket (`/var/run/weft.sock`) or an `ssh://` URL routed through the
  [weft-client](https://github.com/openweft/weft-client) SSH transport.

## Build and run

The build is driven by `task` (Taskfile.yml). Targets that take a Go
or npm path do their dependency steps first — you rarely need to call
them by hand.

```sh
task install        # web/npm install
task gen-api        # dump OpenAPI + regenerate web/src/lib/api.gen.ts
task build-web      # gen-api + vite build → web/dist
task build          # build-web + go build -o weft-webui
task check          # gen-api + go vet + svelte-check + vitest
task check:drift    # fail when web/openapi.json or api.gen.ts is stale
```

Running a dev instance against a local mock store :

```sh
# legacy single-listener — :8080 serves the full surface
task run

# user :8080 + tenant 127.0.0.1:8088 (sets WEBUI_DEV_MODE=true)
task run:dual

# all three portals — user :8080, tenant :8088, infra :8089
task run:trio

# API-only iteration (skips the SPA build, useful with `task dev:web` in another shell)
task dev:api
task dev:api:dual
task dev:api:trio
task dev:web        # Vite dev server on :5173, proxies /api -> :8080
```

Live mode against a running daemon :

```sh
go run . --weft-socket "$HOME/.weft/weft.sock"

# Sibling weft-network (optional ; mocks routers/LBs/DNS when omitted)
go run . \
  --weft-socket "$HOME/.weft/weft.sock" \
  --weft-network-socket "$HOME/.weft/weft-network.sock"

# Over SSH (weft-client transport)
go run . --weft-socket "ssh://you@dc1-r1-h1/.weft/weft.sock"
```

Without `task` :

```sh
cd web && npm install && npm run build && cd ..
go build -o weft-webui .
./weft-webui
```

> `//go:embed` needs `web/dist` to exist at compile time, so the SPA
> build always runs before the Go build. `task build` chains them.

## API surface

- **OpenAPI 3.1 spec** : `/api/openapi.json`
- **Interactive docs** : `/api/docs`
- **Operations** : ~145 typed handlers across these mounts
  ([`internal/server/api.go`](internal/server/api.go)) :

| Mount | Resource family |
| --- | --- |
| `mountFlavorsAPI` | VM flavor catalogue |
| `mountScriptsAPI` | cloud-init / user-script catalogue |
| `mountSSHKeysCatalogueAPI` | cluster-wide SSH-key catalogue + import |
| `mountMicroVMMetadataAPI` | microVM metadata (annotations, properties) |
| `mountMicroVMLifecycleAPI` | microVM create / start / stop / delete |
| `mountNetworkingAPI` | Networks, Routers, LBs, DNS zones + records, security groups |
| `mountTenantsAPI` | Tenants, Projects, Users, Groups, quotas, usage |
| `mountStorageAPI` | Volumes, Shares, Buckets + policies |
| `mountVolumeMetadataAPI` | free-form volume properties |
| `mountVolumeSnapshotsAPI` | volume snapshots |
| `mountVolumeBackupsAPI` | volume backups |
| `mountSubnetsAPI` | subnets |
| `mountVMAuthzAPI` | per-VM RBAC bindings |
| `mountEditableMetadataAPI` | dashboard-driven labels / annotations |
| `mountRegistriesAPI` | OCI registry remotes |
| `mountPluginsAPI` | plugin catalogue + install / uninstall |
| `mountFederationAPI` | peer cluster federation |
| `mountInventoryAPI` | AZs, Racks, Hosts |
| `mountMiscAPI` | overview, healthz, readyz, audit-log tail |

Routes that stay on stdlib (not registered through huma) :
`/api/healthz`, `/api/readyz`, `/api/auth/{login,callback,logout}` (OIDC
redirects), `/api/session/scope`, `/api/events` (hand-rolled SSE
stream), `/metrics`, and the SPA static handler.

## Live-first / mock fallback

Every handler follows the same pattern :

1. If `live != nil` (`--weft-socket` set), call the relevant
   `wclient.Client` method.
2. On `codes.Unimplemented` or "live not wired", fall through to the
   in-memory mock store (seeded with realistic data) so the dashboard
   stays usable in dev / preview mode.
3. On any other gRPC error, return `502 Bad Gateway` with the
   underlying status — easier to debug than silently masking with mock
   data.

The same pattern applies to `liveNet` (`--weft-network-socket`) for
the resources owned by weft-network. SchedulingRule mutations prefer
`live` (weft-agent) over `liveNet` per the openweft pull-model :
weft-agent owns the rule catalogue state, weft-network observes
changes through its reconcile loop.

Wired live RPC families (selected) :

| Family | Backing RPCs |
| --- | --- |
| Tenants / Projects / Users / Groups | `List/Create/Update/DeleteProject`, `ListUsers`, … |
| MicroVMs | `ListVMs`, `CreateVM`, `StartVM`, `StopVM`, `DeleteVM`, metadata + authz |
| Inventory | `ListAZs`, `ListRacks`, `ListHosts`, CRUD on each |
| Networking | `ListNetworks`, `ListSubnets`, `ListLoadBalancers`, `ListRouters`, `ListDNSZones`, `ListDNSRecords` |
| Storage | `ListVolumes`, `CreateVolume`, `ListShares`, `ListBuckets`, volume snapshots + backups, bucket policies |
| Bucket S3 wiring | `CreateBucket` carries `endpoint` + `region` + `access_key_id` + `secret_access_key` (proto v0.9.0) |
| SSH keys | cluster-wide catalogue + `ImportSSHKeyCatalogue` |
| Scheduling | `List/Create/Update/DeleteSchedulingRule` (nominal binding) |
| Registries | `ListRegistryRemotes`, `SetRegistryRemote`, `SearchRegistryRemote` |
| Plugins / Federation | `ListPlugins`, `InstallPlugin`, `UninstallPlugin`, `ListFederationPeers` |

Refer to [`CHANGELOG.md`](CHANGELOG.md) for the full per-tier wiring
journal (proto v0.7.0 → v0.9.0).

## Three-portal split (user / tenant / infra)

`weft-webui` binds up to **three** HTTP listeners, each scoped at the
huma operation registration layer
([`internal/server/api.go`](internal/server/api.go) +
[`internal/server/portals.go`](internal/server/portals.go)). Each
listener also serves its own **separate SPA bundle**
(`web/dist/{user,tenant,infra}/index.html`) built from three
disjoint Svelte shells — the tree-shaker strips the cluster-admin
pages from the user bundle outright.

| Portal | Default | Exposes | Visibility |
| --- | --- | --- | --- |
| user   | `:8080` (`--addr`)        | own-scope reads + writes only (`/api/me`, `/api/projects`, `/api/volumes`, `/api/shares`, `/api/buckets`, `/api/microvms`, `/api/instances`, `/api/ssh-keys`, `/api/sessions/*`, `/api/{readyz,livez}`)         | public Internet (behind a proxy) |
| tenant | empty (`--tenant-addr`)   | user surface + tenant-wide views + tenant-admin mutations (`/api/quotas`, `/api/audit-log` tenant-scoped, `/api/tenants/{name}/{projects,members,...}`)                                                       | tenant VLAN ; tenant-admin + regular users in their tenant |
| infra  | empty (`--infra-addr`)    | tenant surface + cluster-wide ops (`/api/azs`, `/api/racks`, `/api/hosts`, `/api/plugins`, `/api/federation/*`, `/api/registries`, `/api/audit-log` cluster-scoped, `/api/dns-zones`, `/api/security-rules`, `/api/scheduling-rules`, `/api/network-topology`, `/metrics`) | WireGuard mesh only ; never `0.0.0.0` |

**Hard isolation** : a user who hits `:8080` cannot reach
`/api/hosts` or `/api/plugins` or `/api/federation/peers` even by
crafting a URL — the corresponding huma operation is genuinely not
registered on that mux. Probes see a plain `404` with no
"you're not allowed" signal.

**Backward compatibility** : when only `--addr` is set (neither
`--tenant-addr` nor `--infra-addr`), the binary boots in legacy
single-listener mode — `UserAddr` serves the full surface
(everything the infra portal would expose). This keeps `task run` /
`go run .` working on a one-host dev box.

`--admin-addr` (and `WEBUI_ADMIN_ADDR`) is a deprecated alias for
`--tenant-addr` and fires a one-line deprecation log at boot.

Three-portal invocation :

```sh
weft-webui \
  --addr :8080 \
  --tenant-addr :8088 \
  --infra-addr :8089 \
  --weft-socket /tmp/weft.sock
```

## Configuration

Env-first ; flags override env for the common knobs. Required
variables are flagged below ; defaults fire when omitted.

| Variable | Default | Notes |
| --- | --- | --- |
| `WEBUI_USER_ADDR` | `:8080` | user-portal listener (public) ; flag `--addr` ; legacy `WEBUI_LISTEN_ADDR` honoured |
| `WEBUI_TENANT_ADDR` | empty | tenant-portal listener ; flag `--tenant-addr` ; bind on the tenant VLAN |
| `WEBUI_INFRA_ADDR` | empty | infra-portal listener ; flag `--infra-addr` ; bind on the WireGuard mesh, never `0.0.0.0` |
| `WEBUI_ADMIN_ADDR` | empty | **DEPRECATED** alias for `WEBUI_TENANT_ADDR` ; flag `--admin-addr` ; fires a deprecation warning |
| `WEBUI_WEFT_SOCKET` | empty | unix path or `ssh://…` ; flag `--weft-socket` ; required in prod |
| `WEBUI_WEFT_NETWORK_SOCKET` | empty | weft-network controller ; flag `--weft-network-socket` |
| `WEBUI_TLS_CERT` / `_KEY` | empty | set both or neither ; TLS 1.2 minimum (`WEBUI_TLS_MIN_VERSION=1.3` to harden) |
| `WEBUI_DEV_MODE` | `false` | dev mode (no auth, mock fallback) ; flag `--dev` |
| `WEBUI_AUTH_MODE` | `oidc` (prod) / `none` (dev) | flag `--auth-mode` |
| `WEBUI_OIDC_ISSUER` | empty | e.g. `https://dex.example/dex` ; required in prod |
| `WEBUI_OIDC_CLIENT_ID` | empty | required in prod |
| `WEBUI_OIDC_CLIENT_SECRET` | empty | confidential clients |
| `WEBUI_OIDC_REDIRECT_URL` | empty | falls back to `WEBUI_PUBLIC_URL` + `/api/auth/callback` |
| `WEBUI_OIDC_SCOPES` | `openid,email,profile,groups` | comma-separated |
| `WEBUI_PUBLIC_URL` | empty | external base URL ; flag `--public-url` |
| `WEBUI_SESSION_KEY` | empty | ≥ 32 bytes, hex or base64 ; required in prod |
| `WEBUI_COOKIE_DOMAIN` / `_NAME` / `_SECURE` | host-only / `weft_webui_session` / true-in-prod | session cookie tuning |
| `WEBUI_SESSION_MAX_AGE` | `43200` | seconds |
| `WEBUI_ALLOWED_ORIGINS` | empty | extra `scheme://host[:port]` for cross-origin mutations |
| `WEBUI_TRUST_PROXIES` | `false` | honour `X-Forwarded-For` / `X-Forwarded-Proto` |
| `WEBUI_MAX_REQUEST_BODY_BYTES` | `1048576` | `http.MaxBytesReader` cap on `/api/*` |
| `WEBUI_SHUTDOWN_TIMEOUT` | `10s` | grace period after SIGTERM (SSE drained first) |
| `WEBUI_POLICY_STRICT` | `false` | bucket-policy default-deny (AWS-aligned) |
| `WEBUI_AUDIT_LOG_PATH` | empty | JSONL audit file ; flag `--audit-log-path` |
| `WEBUI_AUDIT_ROTATE_BYTES` | `104857600` | size-based rotation threshold |
| `WEBUI_INVENTORY_PATH` | empty | JSON file the AZ/Rack/Host inventory persists to ; flag `--inventory-path` |
| `WEBUI_DNS_PATH` / `_SECURITY_PATH` / `_SCRIPTS_PATH` | empty | mock-layer persistence for each family |
| `WEBUI_STATE_HISTORY_KEEP` | `0` | pre-mutation snapshots under `<path>.history/` for one-step undo |

Generate a session key :

```sh
head -c 32 /dev/urandom | xxd -p -c 64
```

Minimal prod invocation (user UI only) :

```sh
WEBUI_WEFT_SOCKET=/var/run/weft.sock \
WEBUI_OIDC_ISSUER=https://dex.example/dex \
WEBUI_OIDC_CLIENT_ID=weft-webui \
WEBUI_OIDC_CLIENT_SECRET=… \
WEBUI_PUBLIC_URL=https://weft.example \
WEBUI_SESSION_KEY=$(head -c 32 /dev/urandom | xxd -p -c 64) \
WEBUI_AUDIT_LOG_PATH=/var/log/weft-webui/audit.jsonl \
WEBUI_INVENTORY_PATH=/var/lib/weft-webui/inventory.json \
WEBUI_STATE_HISTORY_KEEP=20 \
  ./weft-webui
```

## Telemetry

The admin listener exposes Prometheus metrics on `/metrics` :

```
weft_webui_http_requests_total{persona,method,route,status}
weft_webui_http_request_duration_seconds{persona,method,route}
weft_webui_http_in_flight_requests
weft_webui_grpc_calls_total{method,status}
weft_webui_grpc_call_duration_seconds{method}
weft_webui_auth_logins_total{result}
weft_webui_auth_active_sessions
weft_webui_user_actions_total{sub,action}
weft_webui_build_info{version}
go_*  process_*
```

Route labels are pre-normalised (`/api/resources/:id`,
`/api/buckets/:name/objects`, …) so cardinality stays bounded. The
user port deliberately does **not** mount `/metrics` — its catch-all
returns `index.html` instead.

## Audit log

Append-only JSONL at `--audit-log-path`. Size-based rotation defaults
to 100 MiB → `<path>.<RFC3339Nano>`. OIDC lifecycle events ride the
same file as mutating API calls, so a brute-force probe and a
fat-fingered `DeleteVolume` are both visible in one stream. The
dashboard tails recent entries via `GET /api/audit-log` (admin scope
only).

## Tests and CI

```sh
go test ./...                       # Go unit + e2e (server, wclient, audit, ratelimit, …)
cd web && npm run check             # svelte-check (TS + Svelte type checks)
cd web && npm run test              # vitest unit tests (endpoints, events, router, …)
task check:drift                    # web/openapi.json + api.gen.ts in sync with Go handlers
```

GitHub workflows under [`.github/workflows`](.github/workflows) :

- **ci.yml** — builds the SPA, uploads `web/dist` as an artifact, then
  builds + tests the Go backend on every push and PR.
- **release.yml** — tag-driven (`v*`) or `workflow_dispatch` only.
  Builds the multi-arch (`linux/amd64`, `linux/arm64`) OCI image,
  signs it with cosign keyless, attaches an SPDX SBOM attestation,
  and generates a SLSA L3 provenance attestation. **Does not** publish
  on push to main (project policy : no autopublish during dev).
- **verify-release.yml** — verifies cosign signature + provenance on
  the published image.

## Container image (GHCR)

```sh
docker pull ghcr.io/openweft/weft-webui:v0.2.0
# or
docker pull ghcr.io/openweft/weft-webui:latest
```

The Dockerfile is a three-stage build :

1. `node:20-alpine` — builds the SPA into `web/dist/`.
2. `golang:1.26-alpine` — compiles the Go binary with
   `//go:embed all:web/dist`, statically linked (`CGO_ENABLED=0`),
   trimpath + stripped.
3. `gcr.io/distroless/static-debian12:nonroot` — runtime, ~18 MB,
   includes a CA bundle for dialing the OIDC issuer + GHCR.

Build locally :

```sh
docker build \
  --build-arg VERSION=$(git describe --tags --always --dirty) \
  -t ghcr.io/openweft/weft-webui:dev .
```

## Deployment

Three supported patterns. Pick the one that fits the host topology.

### 1. Via `weft up` (recommended)

The sibling [`weft`](https://github.com/openweft/weft) bring-up
includes the webui in its infra DAG ([`infra/webui/plan.hcl`](https://github.com/openweft/weft/tree/main/infra/webui)).
The plan deploys **three stateless replicas, one per DC**, behind the
embedded Caddy edge proxy in `weft-agent` ; `depends_on = [etcd, dex]`.
The compose runs as part of `weft infra bootstrap`.

### 2. Container (Docker / Podman / Kubernetes)

```sh
docker run --rm \
  -p 8080:8080 \
  -e WEBUI_WEFT_SOCKET=tcp:weft-agent:7700 \
  -e WEBUI_WEFT_NETWORK_SOCKET=tcp:weft-network:7700 \
  -e WEBUI_AUTH_MODE=oidc \
  -e WEBUI_OIDC_ISSUER=https://dex.example.com \
  -e WEBUI_OIDC_CLIENT_ID=weft-webui \
  -e WEBUI_OIDC_CLIENT_SECRET=… \
  -e WEBUI_PUBLIC_URL=https://weft.example.com \
  -e WEBUI_SESSION_KEY=$(head -c 32 /dev/urandom | xxd -p -c 64) \
  ghcr.io/openweft/weft-webui:v0.2.0
```

Full deployment notes, including hardening + reverse-proxy guidance,
live under [`deploy/`](deploy/) — including a sample systemd unit at
[`deploy/systemd/weft-webui.service`](deploy/systemd/weft-webui.service).

### 3. Standalone binary

Drop the binary on the host, point it at a Unix socket or an
`ssh://` URL, and run it under systemd. The binary contains every
runtime asset (HTML, CSS, JS, fonts, OpenAPI spec) ; no external
filesystem dependencies.

## License

BSD 3-Clause — see [`LICENSE`](LICENSE).
