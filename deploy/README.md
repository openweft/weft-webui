# Deploying weft-webui

weft-webui is part of the openweft control plane. The canonical
shape is **OCI image → infra microVM**, managed by weft itself :

1. CI publishes a 4-arch (amd64 / arm64 / riscv64 / loong64)
   image to `ghcr.io/openweft/weft-webui:vX.Y.Z` via the
   release.yml tag-gated workflow.
2. `weft up` (day-0 planner, see [weft/cluster/](https://github.com/openweft/weft/tree/main/cluster))
   pulls the image + boots it as an infra microVM next to the
   etcd / NATS / dex / coredns / zot stack.
3. State (audit log + inventory / dns / security / scripts JSON
   files) lives on a weft-block volume attached to the microVM,
   so snapshots + replication come for free.
4. The dashboard surfaces through a weft `loadbalancer` resource ;
   weft-agent's embedded Caddy ([weft/agent/proxy/](https://github.com/openweft/weft/tree/main/agent/proxy))
   handles TLS termination + Let's Encrypt + the WireGuard-only
   admin port routing as part of the regular L7 plane.

Docker / Kubernetes are competitor platforms — the goal of
openweft is to be what you deploy *to*, not what you deploy
weft-webui under.

## Bring-up via `weft up`

Once your `cluster.hcl` includes a `weft-webui` infra stanza,
day-0 brings the dashboard up alongside every other control-plane
service :

```sh
weft up --cluster cluster.hcl --apply
```

The planner pulls the OCI image, sizes the microVM (defaults
1 vCPU / 256 MiB / 5 GiB volume), boots it, and adds the
loadbalancer rules. The infra microVM uses the same hardening
posture as every other tenant microVM : weft-microvm-init as PID
1, virtio-fs rootfs from the OCI image, WireGuard mesh peer for
control-plane chatter.

## Configuring the dashboard

Per-cluster knobs live in `cluster.hcl`'s `weft-webui {}` block.
Every flag the binary accepts has an env-var equivalent ; the
infra microVM is configured by stamping environment into its
boot config, NOT by editing files on a host. See `weft up`'s
catalogue entry for the full schema.

Common knobs :

| key                                | default        | purpose                                              |
| ---------------------------------- | -------------- | ---------------------------------------------------- |
| `WEBUI_AUTH_MODE`                  | oidc           | "oidc" (prod) or "none" (dev only)                   |
| `WEBUI_OIDC_ISSUER`                | (required)     | Dex / Keycloak / Authentik issuer URL                |
| `WEBUI_PUBLIC_URL`                 | (required)     | Public URL the OIDC redirect builder uses            |
| `WEBUI_MAX_REQUEST_BODY_BYTES`     | 1048576 (1MiB) | Per-request body cap (DoS guard) on `/api/*`         |
| `WEBUI_SHUTDOWN_TIMEOUT`           | 10s            | Grace window for in-flight handlers on SIGTERM       |
| `WEBUI_TLS_MIN_VERSION`            | 1.2            | TLS handshake floor (`1.2` / `1.3`)                  |
| `WEBUI_ALLOWED_ORIGINS`            | (empty)        | CSV of cross-origin clients allowed to mutate (terraform-provider-weft, ops dashboards) |
| `WEBUI_STATE_HISTORY_KEEP`         | 0 (off)        | Snapshot retention per state file ; production = 20  |
| `WEBUI_AUDIT_LOG_PATH`             | (empty=off)    | JSONL audit trail ; production = on                  |
| `WEBUI_AUDIT_ROTATE_BYTES`         | 104857600      | Audit log rotation threshold (100 MiB)               |
| `WEBUI_AUDIT_RETENTION_DAYS`       | 0 (off)        | Delete rotated audit log siblings older than N days  |

## State + persistence

While the live weft-network controller is in mid-rollout, the
inventory / DNS / security-group / scripts surface flushes to
JSON files at `/var/lib/weft-webui/` (mounted from the weft-block
volume). Every successful flush atomically renames the previous
file under `<path>.history/<RFC3339>.json` and prunes the dir
down to `WEBUI_STATE_HISTORY_KEEP` snapshots.

One-step rollback from inside the microVM :

```sh
weft microvm exec weft-webui -- \
  cp /var/lib/weft-webui/inventory.json.history/2026-06-02T12-37-42Z.json \
     /var/lib/weft-webui/inventory.json
weft microvm restart weft-webui
```

Once weft-network exposes the matching gRPC RPCs (`RegisterAZ`,
`RegisterRack`, `RegisterHost`, …) the mock fallback goes inert
and the source of truth shifts to etcd. The JSON layer stays
around as the in-process cache so the dashboard works through
brief controller outages.

### Disaster recovery

The canonical path is weft-block volume snapshots — managed by
the same `volume backup` / `volume restore` flow every tenant
microVM uses. The standalone shell scripts under `deploy/scripts/`
(`weft-webui-backup.sh` + `weft-webui-restore.sh`) are an escape
hatch you can run from inside the microVM via `weft microvm exec`
when you want a portable tarball decoupled from weft-block ; the
README inside each script documents the flags.

## Health probes

- `GET /api/healthz` — liveness (200 + `{ok:true}` while the
  process is up). Used by `weft up`'s post-bring-up readiness
  check + by the embedded Caddy's upstream health monitor.
- `GET /api/readyz` — readiness. 200 + `{ok:true,mode:live|mock}`
  in the happy path ; 503 + `{ok:false,degraded:[…]}` when a
  configured `WEBUI_*_PATH` state file isn't writable (probe
  creates a sibling `.readyz-probe-*` dir and rolls it back).
  Caddy pulls the microVM out of the LB backend on 503.

## Pointing the dashboard at the daemons

- `WEBUI_WEFT_SOCKET` — required for live microVM / volume /
  share data. Empty falls back to mock mode (dev only). Typical
  shape inside the infra microVM : `tcp:weft-agent.weft.internal:7700`.
- `WEBUI_WEFT_NETWORK_SOCKET` — optional. When set the Networking
  panels (routers, LBs, DNS, scheduling rules) route through
  [weft-network](https://github.com/openweft/weft-network).
  Unreachable daemon → transparent mock fallback, no hard error.

## Local dev (outside the microVM)

For iterating on the binary without round-tripping through `weft
microvm pull`, `task dev` boots the binary on the host with
`WEBUI_DEV_MODE=true` (no auth, mock data, insecure cookies, dev
banner on stderr). See the top-level `Taskfile.yml`.
