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

## Mock-layer state persistence

While the live weft-network controller is in mid-rollout, the
dashboard's inventory / DNS / security-group / scripts surface is
served by an in-process mock that flushes to JSON files. Production
deployments should opt-in to disk persistence so operator changes
survive a restart :

```
WEBUI_INVENTORY_PATH=/var/lib/weft-webui/inventory.json
WEBUI_DNS_PATH=/var/lib/weft-webui/dns.json
WEBUI_SECURITY_PATH=/var/lib/weft-webui/security.json
WEBUI_SCRIPTS_PATH=/var/lib/weft-webui/scripts.json
WEBUI_STATE_HISTORY_KEEP=20
```

Every successful flush atomically renames the previous file under
`<path>.history/<RFC3339>.json` and prunes the dir down to
`WEBUI_STATE_HISTORY_KEEP` snapshots. One-step rollback :

```sh
sudo cp /var/lib/weft-webui/inventory.json.history/2026-06-02T12-37-42Z.json \
        /var/lib/weft-webui/inventory.json
sudo systemctl restart weft-webui
```

Once weft-network exposes the matching gRPC RPCs (`RegisterAZ`,
`RegisterRack`, `RegisterHost`, …) the mock fallback goes inert and
the source of truth shifts to etcd. The JSON layer stays around as
the in-process cache so the dashboard works through brief controller
outages.

### Backup

`deploy/scripts/weft-webui-backup.sh` packages every persistence
path declared in `/etc/default/weft-webui` (or the env vars in the
current shell) plus the sibling `<path>.history/` rotation
directories plus the audit log into one timestamped tarball :

```sh
sudo install -m 0755 deploy/scripts/weft-webui-backup.sh \
  /usr/local/bin/weft-webui-backup.sh
sudo /usr/local/bin/weft-webui-backup.sh \
  /var/backups/weft-webui-$(date +%F).tar.gz
```

A MANIFEST file inside the tarball records hostname + timestamp +
the source paths so a cross-cluster restore can refuse mismatched
metadata. Drop the script into cron / a borg pre-hook / Restic
pre-exec and the dashboard's persisted state survives a
node-reimage.

### Restore

`deploy/scripts/weft-webui-restore.sh` is the companion. It reads
the tarball, checks the MANIFEST hostname against the running box
(refuses cross-cluster restores unless `--force`), stops the
weft-webui systemd unit, drops every captured file into place,
then restarts the daemon.

```sh
sudo install -m 0755 deploy/scripts/weft-webui-restore.sh \
  /usr/local/bin/weft-webui-restore.sh

# Dry-run first : prints the file list without writing.
sudo /usr/local/bin/weft-webui-restore.sh --dry-run \
  /var/backups/weft-webui-2026-06-02.tar.gz

# Real restore.
sudo /usr/local/bin/weft-webui-restore.sh \
  /var/backups/weft-webui-2026-06-02.tar.gz
```

Pass `--force` only for a deliberate cross-cluster restore (e.g.
seeding a fresh box from a peer). Without it the MANIFEST hostname
check guards against accidentally cross-pollinating one cluster's
sessions / audit trails into another.

## Operational tunables

| env                                | default        | purpose                                              |
| ---------------------------------- | -------------- | ---------------------------------------------------- |
| `WEBUI_MAX_REQUEST_BODY_BYTES`     | 1048576 (1 MiB)| Per-request body cap (DoS guard) on `/api/*`         |
| `WEBUI_SHUTDOWN_TIMEOUT`           | 10s            | Grace window for in-flight handlers on SIGTERM       |
| `WEBUI_TLS_MIN_VERSION`            | 1.2            | TLS handshake floor (`1.2` / `1.3`)                  |
| `WEBUI_ALLOWED_ORIGINS`            | (empty)        | CSV of cross-origin clients allowed to mutate (terraform-provider-weft, ops dashboards) |
| `WEBUI_STATE_HISTORY_KEEP`         | 0 (off)        | Snapshot retention per state file ; production = 20  |
| `WEBUI_AUDIT_LOG_PATH`             | (empty=off)    | JSONL audit trail ; production = on                  |
| `WEBUI_AUDIT_ROTATE_BYTES`         | 104857600      | Audit log rotation threshold (100 MiB)               |
| `WEBUI_AUDIT_RETENTION_DAYS`       | 0 (off)        | Delete rotated audit log siblings older than N days ; sweep at boot + every 6h |

Every `WEBUI_*` env var has a matching `--*` flag for one-off
overrides ; `weft-webui --help` enumerates them.

## Health probes

- `GET /api/healthz` — liveness (200 + `{ok:true}` while the process
  is up). Wire to `k8s.io/livenessProbe`.
- `GET /api/readyz` — readiness. 200 + `{ok:true,mode:live|mock}` in
  the happy path ; 503 + `{ok:false,degraded:[…]}` when a configured
  `WEBUI_*_PATH` state file isn't writable (probe creates a sibling
  `.readyz-probe-*` dir and rolls it back). Take the replica out of
  rotation on 503 — its mutations would succeed in memory but vanish
  on restart.

## Kubernetes

`deploy/k8s/deployment.yaml.example` is a single-replica starter
that bundles every k8s object an operator needs : Namespace,
ConfigMap (matching the systemd EnvironmentFile knobs), Secret
(session key + OIDC client secret), 5 GiB PVC for state, the
Deployment with health + readiness + startup probes, and three
Services (user / tenant / infra).

```sh
# Edit the image tag, hostnames, OIDC issuer, then :
kubectl apply -f deploy/k8s/deployment.yaml.example
```

Notes on the defaults :

- **Single replica + `Recreate` strategy**. The JSON persistence
  layer is per-pod (audit log + inventory / dns / security /
  scripts under the PVC), so two pods would diverge. Bump
  `replicas` after the weft-network swap-out lands and the state
  lives in etcd.
- **Probes hit /api/healthz + /api/readyz**. Readiness flips to
  503 when a configured `WEBUI_*_PATH` state file isn't writable
  — k8s takes the pod out of the Service backend automatically.
- **PVC ReadWriteOnce**. The pod owns the state ; restoring from
  the backup script means `kubectl scale deploy weft-webui --replicas 0`
  before unpacking the tarball.
- **`readOnlyRootFilesystem: true`** — everything writeable lives
  under the mounted PVC. The audit + state files all land in
  `/var/lib/weft-webui/`.
- Front with **Ingress NGINX / Traefik / Cilium Gateway**. The
  Caddyfile snippet a few sections down shows the `X-Forwarded-Proto`
  + `Host` headers weft-webui expects from the L7 proxy.

## Reverse proxy & TLS via Caddy

Operators using Caddy (the canonical weft front-proxy ; the agent
embeds it for L7 routing — see
[project_reverse_proxy_caddy](https://github.com/openweft/weft/blob/main/agent/proxy/))
get a ready-to-edit `deploy/caddy/Caddyfile.example` :

```sh
sudo install -m 0644 deploy/caddy/Caddyfile.example \
  /etc/caddy/sites-enabled/weft-webui.caddy

# Edit the hostnames + binds, then :
sudo systemctl reload caddy
```

The example covers the three-portal split :

- `weft.example.com` → user portal (`:8080`), public, Let's Encrypt auto.
- `tenant.weft.example.com` → tenant portal (`:8082`), public + OIDC.
- `infra.weft.example.com` → infra portal (`:8088`), bound to a
  WireGuard listener IP so it never reaches the public Internet.

The blocks forward `X-Forwarded-Proto: https` + `Host:` so weft-
webui's CSRF Origin check + OIDC redirect builder see the public
hostname rather than `127.0.0.1`. HSTS is stamped at the edge.

For mTLS / internal-CA, swap the auto-Let's-Encrypt with the
commented `tls` directive at the bottom of the example.

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
