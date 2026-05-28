# weft-webui

A web dashboard for **Weft** — comparable in spirit to OpenStack Horizon.
One Go binary serves a small JSON API **and** the embedded SvelteJS single-page
app, exposing the platform's object types: tenants, projects, users, groups,
networks, floating IPs, security groups & rules, volumes, shares, microVMs,
instances, flavors, and hosts.

## Stack

- **Backend** — Go (stdlib `net/http`), serves `/api/*` and the embedded SPA.
- **Frontend** — Svelte 5 + Vite, Tailwind CSS v4, DaisyUI v5.
- **Data-driven** — the UI is generated from a single **resource registry**
  ([`internal/server/resources.go`](internal/server/resources.go)). The sidebar,
  the overview cards, and every table come from it, so adding a new object type
  is one entry — no new pages.

## Layout

```text
.
├── main.go                     entry point ; embeds web/dist via go:embed
├── internal/server/
│   ├── server.go               routes + JSON API
│   ├── resources.go            resource registry + mock data  ← single source of truth
│   └── spa.go                  serves the SPA with deep-link fallback
└── web/                        SvelteJS frontend
    ├── src/
    │   ├── App.svelte           layout shell
    │   ├── lib/api.ts           API client
    │   ├── lib/router.ts        tiny hash router
    │   └── lib/components/      Sidebar, Topbar, ResourceTable, ResourcePage, Overview
    └── dist/                    build output (embedded by the binary)
```

## Run

```sh
# One-off build + serve (SPA + API on http://localhost:8080)
task run
# → open http://localhost:8080

# Live-reload development — two terminals :
task dev:api      # Go API on :8080
task dev:web      # Vite dev server on :5173 (proxies /api → :8080)
# → open http://localhost:5173
```

Without `task`:

```sh
cd web && npm install && npm run build && cd ..
go run .
```

> `go:embed` needs `web/dist` to exist at compile time, so always build the
> frontend before the binary. `task build` / `task run` do this for you.

## API

| Method · path                 | Returns                                            |
| ----------------------------- | -------------------------------------------------- |
| `GET /api/resources`          | resource metadata (id, label, section, columns)    |
| `GET /api/resources/{id}`     | rows for one object type                           |
| `GET /api/summary`            | counts per object type (overview)                  |
| `GET /api/healthz`            | liveness                                           |

## Adding an object type

Append one `Resource` to the registry in
[`internal/server/resources.go`](internal/server/resources.go) — its `Section`,
`Columns`, and rows. The sidebar entry, overview card, and table appear
automatically.

## Live mode

By default the server returns **mock data** for every resource. Point it at a
running `weft` daemon with `--weft-socket` and any resource that has been
wired calls the real gRPC API via [`weft-client`](../weft-client). Unwired
resources stay on their mock until they are migrated one at a time.

```sh
# Mock mode — default
task run

# Live mode against a local daemon
go run . --weft-socket "$HOME/.vzd/vzd.sock"
# … or an SSH-tunneled socket (see weft-client)
go run . --weft-socket "ssh://you@dc-a.example/.vzd/vzd.sock"
```

**Wired so far** :

| Resource         | Status | Backing RPC                          |
| ---------------- | ------ | ------------------------------------ |
| Projects         | live   | `VzdService.ListProjects`            |
| microVMs         | live   | `VzdService.ListVMs`                 |
| Networks         | live   | `VzdService.ListNetworks`            |
| Hosts            | live   | `VzdService.ListHosts`               |
| Volumes          | live   | `VzdService.ListVolumes`             |
| Users            | live   | `VzdService.ListUsers`               |
| Security Groups  | live   | `VzdService.ListSecurityGroups`      |
| everything else  | mock   | —                                    |

When a live RPC fails (daemon unreachable, permission denied, …) the handler
returns **502 Bad Gateway** with the underlying error message — easier to
debug than silently falling back to mock.

## Production deployment

`weft-webui` runs in two modes, picked by `WEBUI_DEV_MODE` :

| Mode      | Auth | Cookies   | Mock fallback | Use for                |
| --------- | ---- | --------- | ------------- | ---------------------- |
| `prod`    | OIDC | `Secure`  | rejected      | real deployments       |
| `dev`     | none | insecure  | allowed       | `task dev:*`, local UI |

Configuration is env-first ; flags override env for the common knobs.

| Variable                    | Default                  | Notes                                                       |
| --------------------------- | ------------------------ | ----------------------------------------------------------- |
| `WEBUI_LISTEN_ADDR`         | `:8080`                  | also `--addr`                                               |
| `WEBUI_WEFT_SOCKET`         | _empty_                  | unix path or `ssh://…` ; required in prod                   |
| `WEBUI_TLS_CERT` / `_KEY`   | _empty_                  | optional ; set both or neither                              |
| `WEBUI_DEV_MODE`            | `false`                  | dev mode                                                    |
| `WEBUI_AUTH_MODE`           | `oidc` (prod), `none` (dev) |                                                          |
| `WEBUI_OIDC_ISSUER`         | _empty_                  | e.g. `https://dex.example/dex`                              |
| `WEBUI_OIDC_CLIENT_ID`      | _empty_                  |                                                             |
| `WEBUI_OIDC_CLIENT_SECRET`  | _empty_                  | confidential clients only                                   |
| `WEBUI_OIDC_REDIRECT_URL`   | _empty_                  | falls back to `WEBUI_PUBLIC_URL + /api/auth/callback`       |
| `WEBUI_OIDC_SCOPES`         | `openid,email,profile,groups` | comma-separated                                      |
| `WEBUI_PUBLIC_URL`          | _empty_                  | external base URL (only used to derive the redirect)        |
| `WEBUI_SESSION_KEY`         | _empty_                  | ≥ 32 bytes, hex or base64 ; required in prod                |
| `WEBUI_COOKIE_DOMAIN`       | _empty_                  | host-only by default                                        |
| `WEBUI_COOKIE_SECURE`       | `true` in prod           | override only for testing                                   |
| `WEBUI_SESSION_MAX_AGE`     | `43200`                  | seconds                                                     |

Generate a session key:

```sh
head -c 32 /dev/urandom | xxd -p -c 64
```

Minimal prod invocation :

```sh
WEBUI_WEFT_SOCKET=/var/run/vzd.sock \
WEBUI_OIDC_ISSUER=https://dex.example/dex \
WEBUI_OIDC_CLIENT_ID=weft-webui \
WEBUI_OIDC_CLIENT_SECRET=… \
WEBUI_PUBLIC_URL=https://weft.example \
WEBUI_SESSION_KEY=$(head -c 32 /dev/urandom | xxd -p -c 64) \
  ./weft-webui
```

### Request flow

1. SPA fetches `/api/me` ; missing session → 401 with `{login: /api/auth/login}`.
2. `api.ts` redirects to `/api/auth/login?return_to=…` ; the server issues the
   OIDC authorization request (state + PKCE), drops a short-lived state cookie,
   bounces the browser to the IdP.
3. IdP returns to `/api/auth/callback` ; server exchanges code, verifies ID
   token + nonce, mints a signed-cookie session, redirects back to `return_to`.
4. Every protected `/api/*` request now carries the cookie. The bearer token
   from the session is attached to outgoing gRPC metadata for each list call,
   so `vzd` enforces per-user RBAC.

### Security headers

Every response gets : `Content-Security-Policy` (relaxed in dev for Vite HMR),
`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `Permissions-Policy`, and
`Cross-Origin-Opener-Policy: same-origin`. API responses also get
`Cache-Control: no-store`.

## Status / next steps

Still TODO : create/edit/delete actions (the row menu is stubbed), detail
drawers, per-project scoping in more list calls (ListProjects exposes the
allowed set), and progressively wiring the remaining resources to `VzdService`.
