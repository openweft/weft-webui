# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **MapFloatingIPModal rate-limit slider** : 0..100k pps cap on
  inbound traffic to a floating IP, wired through the new
  weft-proto `rate_limit_pps` field down to the host-side NAT
  reconciler.
- **MicroVMDrawer Network tab** : aggregates networks in scope,
  floating IPs mapped to the VM, and (new) per-NIC ports surfaced
  via the new `ListPortsForVM` RPC. Surfaces MAC/IP/security-groups
  + portqos Mbps caps. Read-only mirror of `weft network diag <vm>`.
- **NetworksPage DHCPv4 info card** : appears when the selected
  network's `type=bridged` ; surfaces CIDR/gateway/DNS/lease-range
  hint. The host-side `weft-agent` auto-runs a DHCPv4 server on the
  bridge.

### Fixed

- **CreateNetworkModal type picker** : was `nat`/`overlay`/`wireguard`
  (legacy names that no longer matched the data plane) ; now
  `nat`/`bridged`/`isolated`/`mesh`, with descriptions in the tooltip
  and a bridged-mode hint about the DHCP auto-managed server.

## [0.3.1] - 2026-06-14

### Added

- **Three-portal split (user / tenant / infra) on isolated listeners
  + bundles** — `weft-webui` now runs up to three HTTP listeners in
  a single process, each with its own huma router and its own SPA
  bundle. Hard isolation : the corresponding endpoint is **not
  registered** on a listener that shouldn't serve it, so a probe to
  `:8080/api/hosts` or `:8080/api/plugins` returns a plain `404` —
  no "you're not allowed" signal, no half-handled request.

  - `PortalUser` (`--addr`, default `:8080`) : own-scope reads +
    writes only. No `/api/quotas`, `/api/audit-log`, `/api/hosts`,
    `/api/plugins`, `/api/federation/*`, `/api/registries/*`
    mutations, `/api/network-topology`, `/api/azs`, `/api/racks`.
  - `PortalTenant` (`--tenant-addr`, empty) : user surface plus
    tenant-wide views (`/api/tenants/{name}/projects|members`,
    `/api/quotas`, `/api/audit-log` tenant-scoped, plugins
    read-only).
  - `PortalInfra` (`--infra-addr`, empty) : superset — every
    cluster-wide op + `/metrics`. WireGuard-mesh-only ; never
    `0.0.0.0`.

  Each portal's SPA shell statically imports only the pages it
  serves : the user bundle is 2.35 kB (gzip 1.16 kB), the tenant
  bundle is 3.03 kB (gzip 1.30 kB), the infra bundle is 81.77 kB
  (gzip 23.64 kB) — the infra bundle pulls in Plugins / Federation /
  Inventory / NetworkTopology that the smaller portals don't ship.
  Tree-shaking verified at build time via `du -h
  web/dist/{user,tenant,infra}/`.

  - **Backward compatibility** : when only `--addr` is set (no
    `--tenant-addr` / `--infra-addr`), the binary boots in legacy
    single-listener mode — `UserAddr` serves the full surface
    (everything `PortalInfra` would expose). Keeps `task run` /
    `go run .` working on a one-host dev box without learning the
    new flags.
  - **Deprecation** : `--admin-addr` (`WEBUI_ADMIN_ADDR`) is an
    alias for `--tenant-addr` (`WEBUI_TENANT_ADDR`) ; a one-line
    deprecation log fires at boot when the old name is used.

  Frontend changes :
  - `web/{user,tenant,infra}/index.html` — three HTML entry points,
    Vite multi-page build emits `dist/{user,tenant,infra}/index.html`
    + a shared `dist/assets/` chunk pool.
  - `web/src/portals/{UserApp,TenantApp,InfraApp}.svelte` — three
    shell components ; the user + tenant shells statically import
    only the pages relevant to their portal so the bundle stays
    small.
  - `web/src/portals/{user,tenant,infra}.ts` — entry-point mounts.

  Backend changes :
  - `internal/server/portals.go` — `Portal` enum, `Portal.Scope()`,
    `Portal.AssetSubdir()`, `newPortalRouter(deps, p)`,
    `assetsForPortal()` (two-FS view : per-portal index.html + shared
    `/assets/*` pool).
  - `Scope` bitmask gains a `ScopeTenant` bit so the tenant portal
    can register tenant-admin endpoints without pulling in the
    cluster-admin surface ; `ScopeBoth` now covers all three bits.
  - `internal/server/spa.go` — two-FS handler ; serves
    per-portal `index.html` from `Static`, hashed assets from
    `SharedAssets` so the absolute `/assets/<hash>.js` references
    resolve to the dedupped pool.
  - `main.go` runs one goroutine per non-empty listen address ; a
    failure on any listener tears the others down.
  - `internal/config/config.go` adds `TenantAddr` + `InfraAddr` +
    `ResolveAdminAlias()` + `LegacySingleListener()`.

  Example invocation :

  ```sh
  weft-webui --addr :8080 --tenant-addr :8088 --infra-addr :8089 \
    --weft-socket /tmp/weft.sock
  ```

- **MicroVM Metrics tab in `MicroVMDrawer`** — new "Metrics" tab next
  to Summary, surfacing live time-series for CPU%, memory%, network
  rx/tx, and disk read/write, plus uptime + current memory usage. A
  per-VM 90-sample ring buffer (`web/src/lib/microvmMetrics.ts`)
  polls `GET /api/microvms/{name}/metrics` every 5 s ; the panel
  (`MicroVMMetricsPanel.svelte`) renders four stacked
  `TimeSeriesChart.svelte` instances. The chart component is a
  zero-dependency canvas-2D line plot tuned for ≤ 90 points + 1-3
  series (smaller than uPlot + tree-shake-friendly). The panel
  surfaces a "mock data" badge while the server returns synthetic
  curves.

  Server endpoint : `GET /api/microvms/{name}/metrics?project=…` →
  `MetricsSnapshot { sampled_at_unix, cpu_percent, mem_used_mib,
  mem_total_mib, net_rx_bps, net_tx_bps, disk_read_bps,
  disk_write_bps, uptime_seconds, mock }`. Until a real
  `GetMicroVMMetrics` RPC lands on weft-proto, the handler synthesises
  a deterministic curve (FNV-32 of the VM name as phase offset, sine
  modulation per channel) and stamps `mock: true`. `wclient.GetMicroVMMetrics`
  returns `codes.Unimplemented` ; `tryLiveMetrics` calls it through
  `IsUnimplemented(err)` so the live → synth swap is one line the
  day the RPC lands.

  Notes / follow-up :
  - **proto bump** : `weft.proto` needs `GetMicroVMMetrics(name,
    project) → MicroVMMetricsResponse` with the same field set as
    `MetricsSnapshot`. Wire in `wclient.GetMicroVMMetrics`, remove
    the synth call in `tryLiveMetrics`.
  - **weft-agent /metrics scrape** : the per-VM gauge can be read
    from the agent's existing Prometheus surface (`weft_agent_vm_*`
    families) rather than a new RPC — call site is then a small
    Prom client + label match keyed by VM uuid. Decision deferred to
    the proto-bump PR.
  - Pure-Svelte chart : 60 lines of canvas-2D, no npm dep added.

- **Bucket form : `endpoint` / `region` / `access_key_id` /
  `secret_access_key` / `policy` fields surfaced** in the SPA's
  Object Storage create-bucket modal. Required by `live.CreateBucket`
  (proto v0.9.0) for the daemon to register the bucket against the
  S3 backend ; the `secret_access_key` is encrypted server-side and
  never returned via the API (the API response carries everything
  except the secret). Validation : when any S3 wiring field is set,
  all four must be present and `endpoint` must be an `https://` URL ;
  an entirely empty wiring set falls through to the mock-only path
  for backward compat with pre-v0.9 callers. The mock store mirrors
  `endpoint` / `region` / `access_key_id` (NOT the secret) so the
  dashboard's bucket list can echo the wiring without a round-trip.

- **mock stores : opaque `uuid` field on Bucket / Share /
  SchedulingRule / SSHKey rows** (data-model migration follow-up to
  commit `a7276c4`). Every mock row now carries a stable opaque
  `uuid` (computed via `mockUUID(...)`) alongside its `name`, so
  live-first handlers can resolve name → uuid for the proto v0.9.0
  UUID-keyed RPCs without changing the SPA path layout. Helpers
  `bucketUUID`, `sharesDB.shareUUID`, `schedulingDB.ruleUUID`,
  `sshKeyUUID` mirror the `findAZByUUID` / `findRackByUUID` style
  used by inventory. The `uuid` is surfaced in the `/api/resources/*`
  row projections and in the `APISSHKey` typed body so the SPA can
  pick it up when needed ; existing name-as-path-segment URLs are
  unchanged.

- **wclient : VolumeProperty + Share (Get/Resize) + Bucket +
  SSHKeyCatalogue + SchedulingRule + RegistryRemote RPC wrappers
  (proto v0.9.0)**. Twenty-two new methods on `*Client` covering the
  Tier 4-6 control-plane surface — free-form volume metadata, S3
  bucket lifecycle + policies, cluster-wide SSH-key catalogue,
  scheduling rules and OCI registry remotes :
  - **VolumeProperty** (3) : `GetVolumeProperty(ctx, volumeUUID, key)`,
    `SetVolumeProperty(ctx, volumeUUID, key, value)`,
    `DeleteVolumeProperty(ctx, volumeUUID, key)`. Empty value is
    preserved (distinct from absence).
  - **Share extended** (2) : `GetShare(ctx, uuid)`,
    `ResizeShare(ctx, uuid, newSizeGiB)`. Project / name lookup
    + lifecycle calls (`ListShares`, `CreateShare`, `DeleteShare`)
    already shipped pre-v0.8.0. Shrinks are rejected server-side
    (`FailedPrecondition`).
  - **Bucket** (6) : `ListBuckets(ctx, projectUUID)`,
    `GetBucket(ctx, uuid)` (includes `secret_access_key` + `policy`
    that the list response omits),
    `CreateBucket(ctx, projectUUID, name, endpoint, region, accessKeyID, secretAccessKey, policy)`
    (chains a `SetBucketPolicy` when a non-empty policy is passed
    — the proto's `CreateBucketRequest` has no policy field),
    `DeleteBucket(ctx, uuid)`, `GetBucketPolicy(ctx, uuid)`,
    `SetBucketPolicy(ctx, uuid, policy)`.
  - **SSHKeyCatalogue** (4) : `ListSSHKeyCatalogue(ctx)`,
    `AddSSHKeyCatalogue(ctx, name, publicKey, comment)` (fingerprint
    + UUID computed server-side ; idempotent on (name, fingerprint)),
    `RemoveSSHKeyCatalogue(ctx, uuid)`,
    `ImportSSHKeyCatalogue(ctx, blob)` (one entry per
    authorized_keys line ; returns `(imported, skipped)`).
  - **SchedulingRule** (4) : `ListSchedulingRules(ctx, projectUUID)`,
    `CreateSchedulingRule(ctx, projectUUID, name, selector, targetCount, antiAffinity)`,
    `UpdateSchedulingRule(ctx, uuid, name, selector, targetCount, antiAffinity)`
    (partial PATCH ; empty string / `targetCount == -1` keep
    current),
    `DeleteSchedulingRule(ctx, uuid)`. The proto is cluster-wide ;
    `projectUUID` and `name` (for Update) are accepted on the Go
    surface for caller symmetry but dropped on the wire.
  - **RegistryRemote** (4) : `ListRegistryRemotes(ctx)`,
    `SetRegistryRemote(ctx, name, endpoint, insecure, credentialSecretRef)`
    (upsert on name),
    `DeleteRegistryRemote(ctx, uuid)`,
    `SearchRegistryRemote(ctx, query)` (server-side stub today ;
    the wrapper projects `repositories[]` → `[]map[string]any{ {"repository": ..., "registry_name": ...} }`
    so the dashboard's catalogue renderer round-trips).

  Every method follows the existing `defer measured(...) + dial() +
  rpcCtx(withBearer(ctx)) + Request{...}` shape. Row projections
  (`shareRow`, `bucketRow`, `sshKeyCatalogueRow`,
  `schedulingRuleRow`, `registryRemoteRow`) mirror the JSON keys
  the webui already expects.

### Changed

- **api : `POST /api/buckets` now reaches `live.CreateBucket` first**
  (proto v0.9.0 follow-up to commit `a489222`, which skipped this RPC
  because the SPA form surfaced only the bucket name). The handler
  now accepts `endpoint` / `region` / `access_key_id` /
  `secret_access_key` / `policy` in the request body, gates on the
  same `live != nil` + `Unimplemented` fallback pattern as the other
  bucket lifecycle handlers, mirrors the server-minted UUID into the
  mock store on success (sans secret) and emits a `bucket.create`
  audit event with the operator-visible wiring. On `Unimplemented`
  or no daemon, the mock-only branch is preserved verbatim so pre-v0.9
  callers that POST `{ name }` alone still work. Response body adds
  `uuid` + the echoed wiring (still no secret) ; `BucketNameResp`
  shape stays backward-compatible (`omitempty` on the new fields).

- **api : SchedulingRule mutations now prefer `live`
  (weft-agent) over `liveNet` (weft-network) per the openweft
  pull-model**. `POST /api/scheduling-rules`,
  `PATCH /api/scheduling-rules/{name}` and
  `DELETE /api/scheduling-rules/{name}` route through
  `live.CreateSchedulingRule` / `UpdateSchedulingRule` /
  `DeleteSchedulingRule` first — weft-agent owns the rule
  catalogue state and weft-network observes changes through its
  reconcile loop. On `Unimplemented` the create/delete paths fall
  back to `liveNet.*SchedulingRule` (for staged rollouts where
  only weft-network has the RPC), then to the in-memory mock.
  `PATCH` falls straight from `live` to the mock since liveNet
  has no Update RPC of its own. The mock store is mirrored on
  every successful live write so `/api/resources/scheduling-rules`
  stays in sync. Name → uuid resolution goes through
  `schedulingDB.ruleUUID` (added in commit `a489222`). The compact
  AZ/Rack/Host axes are joined into the proto's `anti_affinity`
  wire field via a new `composeAntiAffinity` helper. Reads
  (`GET /api/scheduling-rules` and the resource-row projection)
  keep using `liveNet` for the enriched compliance view ; a
  follow-up will merge `live.ListSchedulingRules` with the
  `scheduling-rule.compliant` SSE events from `/api/events` so
  the table can drop liveNet without losing the observed-count
  column.

- **api : live-first migration for Bucket / Share / SSH-key
  catalogue handlers (v0.9.0 Tier 4-6, follow-up to commit
  `a7276c4`)**. Now that the mock rows carry a stable opaque `uuid`
  (see Added), the UUID-keyed RPCs introduced in `a7276c4` are
  wired through "live-first → mock fallback" on every mutation /
  read where the proto exists :
  - **Bucket** :
    - `DELETE /api/buckets/{name}` — `bucketUUID(name)` →
      `live.DeleteBucket(uuid)` ; mock cascade (bucket row +
      policy) runs unconditionally so the affordance stays
      idempotent.
    - `GET /api/buckets/{name}/policy` —
      `live.GetBucketPolicy(uuid)` ; wire form is a single JSON
      string which the handler decodes into the SPA's typed
      `BucketPolicy`. Decode-fail or `Unimplemented` falls back
      to the mock store so the editor never sees a 502 for a
      known-good bucket.
    - `PUT /api/buckets/{name}/policy` —
      `live.SetBucketPolicy(uuid, json)` ; an empty statement
      list serialises to `""` (clear). Mock mirrors the same
      payload so dual-mode dashboards stay consistent.
  - **Share** :
    - `PUT /api/shares/{name}` (resize) —
      `live.ResizeShare(uuid, sizeGB)`. The `read_only` flag is
      mock-only (proto's `ResizeShareRequest` carries size
      only) — a follow-up extends the proto.
    - `DELETE /api/shares/{name}` — `live.DeleteShare(uuid)`.
    - `POST /api/shares` (existing live-first create) now
      mirrors the server-returned UUID into the mock store so
      the subsequent Resize / Delete paths resolve through
      `sharesDB.shareUUID` without re-querying the agent.
  - **SSHKeyCatalogue** :
    - `GET /api/ssh-keys` — `live.ListSSHKeyCatalogue(ctx)` ;
      every row is mirrored into the mock so name-keyed Set /
      Delete carries the server-side UUID + authoritative
      fingerprint.
    - `POST /api/ssh-keys` —
      `live.AddSSHKeyCatalogue(name, publicKey, comment)`. The
      server-side fingerprint overwrites the client-side
      compute ; the UUID is stamped from the response.
    - `DELETE /api/ssh-keys/{name}` —
      `live.RemoveSSHKeyCatalogue(uuid)` with the name resolved
      through `sshKeyUUID(ctx, name)`.
    - `POST /api/ssh-keys/import` —
      `live.ImportSSHKeyCatalogue(blob)` pushes the
      authorized_keys blob whole ; the per-line mirror still
      runs locally so the dashboard's per-key drawer stays
      populated.

  Every successful mutation keeps emitting `Audit(...)` events
  through the existing per-handler taps ; non-`codes.Unimplemented`
  gRPC errors return `502 Bad Gateway` ; `codes.Unimplemented`
  falls through to the existing mock branch so an older agent
  build keeps serving.

  **Remaining follow-ups** :
  - **`POST /api/buckets` (create)** — the proto's
    `CreateBucketRequest` wants `endpoint` + `region` +
    `access_key_id` + `secret_access_key` per bucket, but the SPA
    form only collects `name` today ; live wiring lands once the
    form surfaces those fields.
  - **`SchedulingRule` Create / Update / Delete** — already on
    `liveNet.*SchedulingRule` (weft-network controller). The
    weft-agent proto v0.9.0 ships a parallel control-plane surface
    on `live.*SchedulingRule` ; the arbitration between the two
    daemons lands in a follow-up before the dual surface goes live.
    The mock rows now carry a `uuid` so either side can be wired
    without a second migration.
  - **`POST /api/shares` (CreateShare)** — `ReadOnly` flag stays
    mock-only because the proto's `CreateShareRequest` doesn't
    carry it ; ditto `ResizeShare`.

- **api : live-first migration for the v0.9.0 Tier 4-6 surface**.
  Four handlers rewired to "live-first → fallback local store",
  matching the pattern in `api_inventory.go` (AZ + Rack, commit
  `e465bc4`) and `api_subnets.go` / `api_networking.go` (Subnet +
  LB + DNS, commit `8c564c6`) :
  - `POST /api/volumes/{key}/properties` and
    `DELETE /api/volumes/{key}/properties/{prop_key}` —
    `live.SetVolumeProperty` / `live.DeleteVolumeProperty`.
    The `key` path segment doubles as the volume UUID on the live
    side (the mock indexes by name ; UUID-shaped keys already flow
    from new-volume creation).
  - `POST /api/registries/remotes` — `live.SetRegistryRemote`
    (upserts on name). The `insecure` flag is derived from the URL
    scheme (`http://` → insecure) ; `credentialSecretRef` stays
    empty until the SPA form gains a secret-store ref picker.
  - `DELETE /api/registries/remotes/{name}` — Name → UUID
    resolved through `live.ListRegistryRemotes` (a cluster-wide
    call, typically < 10 rows), then `live.DeleteRegistryRemote`
    with the UUID. Local store cleanup runs unconditionally so the
    affordance stays idempotent.

  Every successful mutation emits an `Audit(...)` event tagged with
  the resource kind. Non-`codes.Unimplemented` gRPC errors return
  `502 Bad Gateway` ; `codes.Unimplemented` falls through to the
  existing local branch so an older agent build keeps serving
  reads.

  **Deferred to follow-ups** :
  - **Bucket / Share / SchedulingRule local stores are
    name-keyed** ; the proto's `Delete` / `Get` / `SetBucketPolicy`
    / `ResizeShare` / `DeleteShare` RPCs are UUID-keyed. Migrating
    the local mock to carry an opaque `uuid` field next to `name`
    is a data-model change that lands in a follow-up before live
    wiring on those routes.
  - **SSHKeyCatalogue** : the existing mock is name-keyed and
    builds the fingerprint client-side ; the live RPC owns
    UUIDs + server-computed fingerprints + a different on-disk
    schema. The migration pairs with a SPA refresh that uses UUIDs
    as the path segment.
  - **CreateBucket / CreateShare** : the local mock seeds with
    project-as-tenant rules, but the proto's CreateBucket wants
    endpoint + region + access keys per bucket — the form does not
    surface those today. A follow-up wires the form, then the
    handler.
  - **CreateSchedulingRule / UpdateSchedulingRule** already live
    on `liveNet` (weft-network's `CreateSchedulingRule`). The
    weft-agent proto adds a parallel control-plane surface ; the
    handler split lands once the two daemons converge on a single
    rules store.

- **api : live-first migration for the v0.8.0 network plane
  (Subnet + LoadBalancer + DNSZone + DNSRecord)**. Twelve handlers
  rewired to "live-first → fallback local store", matching the
  pattern already in `api_inventory.go` for AZ + Rack
  (commit `e465bc4`) :
  - `POST/DELETE /api/networks/{key}/subnets[/{uuid}]` —
    upsert now routes through `live.CreateSubnet` /
    `live.UpdateSubnet` ; delete routes through
    `live.DeleteSubnet`. Mock store
    (`subnetsByNetwork` in `subnets.go`) mirrored on success
    so the dashboard's NetworkDrawer keeps polling without an
    extra round-trip.
  - `POST/PUT/DELETE /api/loadbalancers[/{uuid}]` —
    `live.CreateLoadBalancer` / `live.UpdateLoadBalancer` /
    `live.DeleteLoadBalancer` with `resourceByID["loadbalancers"]`
    mirrored via new `appendLoadBalancerRow` /
    `updateLoadBalancerRow` / `deleteLoadBalancerRow` helpers in
    `networks.go`. New `PUT` route exposes the listener-patch
    affordance. Cascade refusal (FloatingIPs still mapped) surfaces
    as `409 Conflict` carrying the `blocked_by_fips` count. On
    `codes.Unimplemented` the existing weft-network controller
    path (`liveNet`) takes over so staged rollouts don't break.
  - `POST/PUT/DELETE /api/dns-zones[/{uuid}]` —
    `live.CreateDNSZone` / `live.UpdateDNSZone` /
    `live.DeleteDNSZone`. New `appendDNSZoneRow` helper in
    `dns_mock.go` mirrors creations. Cascade refusal (records
    remain) surfaces as `409 Conflict` with `blocked_by_records`.
    The PUT keeps mock-only fields (role / backend / push_target /
    enabled) flowing through the local store so the editor stays
    rich until the proto catches up.
  - `POST/PUT/DELETE /api/dns-records[/{uuid}]` —
    `live.CreateDNSRecord` / `live.UpdateDNSRecord` /
    `live.DeleteDNSRecord`. New `appendDNSRecordRow` helper.
    The PUT keeps `name`/`type` field mutations local because
    proto v0.8.0's `UpdateDNSRecordRequest` only carries
    value + ttl + priority (the proto's contract is "delete +
    recreate to rename or change record class").

  Every successful mutation emits an `Audit(...)` event tagged
  with the resource kind. Non-`codes.Unimplemented` gRPC errors
  return `502 Bad Gateway` ; `codes.Unimplemented` falls through
  to the existing branch so an older agent build still serves the
  request from the local catalogue. List (`GET`) handlers stay
  on the local store path and ship in a follow-up.

### Added

- **wclient : Subnet + LoadBalancer + DNSZone + DNSRecord RPC
  wrappers (proto v0.8.0)**. Twenty new methods on `*Client`
  covering the network plane now that subnets, L4/L7 load
  balancers and authoritative DNS are first-class control-plane
  RPCs :
  - **Subnet** (5) : `ListSubnets(ctx, networkUUID)`,
    `GetSubnet`, `CreateSubnet(ctx, networkUUID, cidr, name, description, gateway, dnsServers)`,
    `UpdateSubnet(...)` (partial PATCH ; `clearDNSServers=true`
    drops the list, proto3 has no nil/empty distinction on the
    wire), `DeleteSubnet`. `cidr` is immutable — delete + recreate
    to renumber.
  - **LoadBalancer** (6) : `ListLoadBalancers`, `GetLoadBalancer`,
    `CreateLoadBalancer(ctx, projectUUID, name, listenAddr, protocol, backends)`,
    `UpdateLoadBalancer` (listener fields only),
    `SetLoadBalancerBackends(ctx, uuid, backends)` (atomic
    replace ; empty slice clears every member),
    `DeleteLoadBalancer(ctx, uuid) → (blockedFips, err)` —
    cascade refusal surfaces the FloatingIP count. Backends
    travel as `[]map[string]any{ {"address": ..., "weight": ...} }`,
    the same JSON shape the webui handlers already deal in.
  - **DNSZone** (5) : `ListDNSZones`, `GetDNSZone`,
    `CreateDNSZone(ctx, projectUUID, name, soaEmail, ttl)`,
    `UpdateDNSZone(ctx, uuid, soaEmail, ttl)` (`ttl == -1` keeps
    current — proto3 int32 sentinel),
    `DeleteDNSZone(ctx, uuid) → (blockedRecords, err)` — cascade
    refusal surfaces the blocking record count.
  - **DNSRecord** (4) : `ListDNSRecords(ctx, zoneUUID)`,
    `CreateDNSRecord(ctx, zoneUUID, name, recordType, value, ttl, priority)`,
    `UpdateDNSRecord` (value + ttl + priority only ; name +
    recordType are immutable in v0.8.0 — wrapper keeps the fuller
    signature for caller symmetry but the proto strips them),
    `DeleteDNSRecord`.

  Row projections (`subnetRow`, `loadBalancerRow`, `dnsZoneRow`,
  `dnsRecordRow`) mirror the `map[string]any` shape every other
  `List*` method on the client returns ; the dashboard's table
  renderer doesn't change.

  Migration scope : the handlers under
  `internal/server/api_subnets.go`, `api_networking.go` and the
  `dns_mock.go` editing layer still read + write the local
  `resourceByID["subnets"|"load-balancers"|"dns-zones"|"dns-records"]`
  store. Now that the wclient methods are in place, swapping each
  CRUD endpoint to live-first is mechanical ; it lands in a
  follow-up commit so this drop stays small and reviewable. CLI
  parity is already achieved : operators using
  `weft subnet create`, `weft lb create`, `weft dns-zone create`
  and `weft dns-record create` reach the same live registry the
  migrated handlers will read.

  `go.mod` : weft-proto bumped to v0.8.0 in lockstep with the
  weft core consumer. No vendor directory in this repo (build
  downloads modules normally).

- **wclient : AZ + Rack RPC wrappers (proto v0.7.0)**. Eight new
  methods on `*Client` covering the inventory hierarchy now that
  AZ + Rack are first-class control-plane RPCs :
  - `ListAZs(ctx)` → rows with code/name/region/status + derived
    rack + host counts.
  - `CreateAZ(ctx, code, name, region, status) → (uuid, created, err)`
    — idempotent insert mirrors the project pattern.
  - `UpdateAZ(ctx, uuid, name, region, status) error` — partial
    PATCH (empty strings keep current).
  - `DeleteAZ(ctx, uuid) → (blockedRacks, blockedHosts, err)` —
    cascade refusal surfaces the blocking counts.
  - `ListRacks(ctx, azUUID)`, `CreateRack`, `UpdateRack`,
    `DeleteRack` mirror the same contract.

  The handlers under `internal/server/api_inventory.go` still
  read + write the local `resourceByID["azs"|"racks"]` store ;
  migrating them to live-first requires a coordinated diff across
  all 20 CRUD endpoints and lands in a follow-up commit. The
  wclient methods are in place so that migration is mechanical.

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
