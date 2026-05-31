# Deploying weft-webui

Two supported patterns. Pick whichever fits the host topology.

## Container (Docker / Podman / Kubernetes)

The Dockerfile at the repo root assembles the binary in three stages :

1. **node** — builds the Svelte SPA into `web/dist/`.
2. **go** — compiles the Go binary with `//go:embed all:web/dist`.
3. **distroless/static** — runtime, ~18 MB, runs as `nonroot`.

```sh
docker build \
  --build-arg VERSION=$(git describe --tags --always --dirty) \
  -t ghcr.io/openweft/weft-webui:dev .
```

Run :

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
  ghcr.io/openweft/weft-webui:dev
```

The admin listener (port 8088) is bound to localhost by default for
defence in depth ; reach it from inside the cluster via the WireGuard
overlay or expose it explicitly when running standalone.

## systemd (bare metal / weft infra microVM)

`deploy/systemd/weft-webui.service` is the unit file. Same hardening
baseline as `weft-network.service` (NoNewPrivileges, ProtectSystem=
strict, seccomp `@system-service`).

```sh
sudo install -m 0755 ./weft-webui /usr/local/bin/weft-webui
sudo install -m 0644 ./deploy/systemd/weft-webui.service \
                     /etc/systemd/system/weft-webui.service
sudo useradd --system --no-create-home --shell /usr/sbin/nologin weft
sudo systemctl daemon-reload
sudo systemctl enable --now weft-webui
```

Configure via `/etc/default/weft-webui` :

```
WEBUI_WEFT_SOCKET=unix:///run/weft-agent/weft.sock
WEBUI_WEFT_NETWORK_SOCKET=unix:///run/weft-network/weft-network.sock
WEBUI_AUTH_MODE=oidc
WEBUI_OIDC_ISSUER=https://dex.weft.internal
WEBUI_OIDC_CLIENT_ID=weft-webui
WEBUI_OIDC_CLIENT_SECRET=…
WEBUI_PUBLIC_URL=https://weft.example.com
WEBUI_AUDIT_LOG_PATH=/var/lib/weft-webui/audit.log
```

The audit log writes through `ReadWritePaths=/var/lib/weft-webui`
even with the strict filesystem hardening ; rotates at 100 MB by
default (override `WEBUI_AUDIT_ROTATE_BYTES`).

## Reverse proxy & TLS

The unit does **not** terminate TLS — front it with Caddy / nginx
on the host, or rely on the weft-agent's embedded Caddy
([see `weft/agent/proxy/`](https://github.com/openweft/weft/tree/main/agent/proxy))
when the dashboard is reached through a `loadbalancer` resource. The
`WEBUI_PUBLIC_URL` env var is what the OIDC redirect builder uses ;
keep it aligned with whatever the front proxy presents.

## Pointing the dashboard at the daemons

- `--weft-socket <addr>` (or `WEBUI_WEFT_SOCKET`) — required for live
  microVM / volume / share / etc. data. Empty falls back to mock mode
  (dev only).
- `--weft-network-socket <addr>` (or `WEBUI_WEFT_NETWORK_SOCKET`) —
  optional. When set the Networking panels (routers, LBs, DNS,
  scheduling rules) route through the
  [weft-network daemon](https://github.com/openweft/weft-network).
  Unreachable daemon → transparent mock fallback, no hard error.

`<addr>` is the same shape both flags accept : `unix:///path/to/sock`
or `tcp:host:port`.
