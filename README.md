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

## Status / next steps

Still TODO: auth (OIDC via dex), create/edit/delete actions (the row menu is
stubbed), per-project scoping, detail drawers, and progressively wiring the
remaining resources (microVMs, Hosts, Networks, Volumes, Users, …) to
`VzdService`.
