package server

// Resource registry — the single source of truth for what the dashboard
// exposes. Each entry drives both the mock API and (via /api/resources)
// the SvelteJS sidebar + tables, so adding a Weft object type later means
// adding one entry here. The mock rows stand in until the handlers are
// wired to the real weft gRPC API (weft-client / weft-proto).

// Column describes one table column of a resource.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Scope marks which persona is allowed to see a resource. Most types
// are visible to both ; cluster-wide ones (Hosts, Users, Tenants) are
// admin-only and never reach the user-facing port.
type Scope uint8

const (
	ScopeUser   Scope = 1 << iota // visible on the user portal (project-scoped)
	ScopeAdmin                    // visible on the infra portal (cluster-wide)
	ScopeTenant                   // visible on the tenant portal (tenant-admin)
)

// ScopeBoth is the default for project-scoped resources : the user
// sees their own, the tenant + infra portals see a tenant- /
// cluster-wide view (weft-agent applies the filter). Renamed
// semantically to "all portals" with the three-portal split but kept
// as ScopeBoth for source-compatibility.
const ScopeBoth = ScopeUser | ScopeTenant | ScopeAdmin

// Has reports whether s grants p.
func (s Scope) Has(p Scope) bool { return s&p != 0 }

// Resource is one Weft object type surfaced in the UI.
type Resource struct {
	ID      string           `json:"id"`      // url slug, e.g. "floating-ips"
	Label   string           `json:"label"`   // sidebar label, e.g. "Floating IPs"
	Section string           `json:"section"` // grouping, e.g. "Network"
	Columns []Column         `json:"columns"`
	Rows    []map[string]any `json:"-"` // served separately via /api/resources/{id}
	// Scope defaults to ScopeBoth (zero value treated as ScopeBoth via
	// resolveScope). Set explicitly to ScopeAdmin for cluster-only types.
	Scope Scope `json:"-"`
	// Hidden = excluded from the /api/resources catalogue listing
	// (so it doesn't show up in the sidebar) but rows still fetchable
	// via /api/resources/{id}/rows. Used for "data resources" the
	// dashboard merges into a custom page (e.g. dns-zones + dns-records
	// merged into a single DNS page).
	Hidden bool `json:"-"`
}

// resolveScope treats the zero value as ScopeBoth so existing entries
// don't have to set a field.
func resolveScope(s Scope) Scope {
	if s == 0 {
		return ScopeBoth
	}
	return s
}

func cols(pairs ...string) []Column {
	out := make([]Column, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Column{Key: pairs[i], Label: pairs[i+1]})
	}
	return out
}

func row(pairs ...any) map[string]any {
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i].(string)] = pairs[i+1]
	}
	return m
}

// registry is ordered; sections are rendered in first-seen order.
var registry = []Resource{
	// ---------- Identity ----------
	//
	// Identity resources (tenants/projects/users/groups) are driven by
	// tenantsDB (tenants.go) so relationships stay consistent. Rows here
	// are nil — handlers in server.go call into the store on each
	// request. Tenants is dual-scope : the user UI filters to the
	// caller's memberships, the admin UI surfaces all of them.
	{
		ID: "tenants", Label: "Tenants", Section: "Identity", Scope: ScopeBoth,
		Columns: cols("name", "Name", "domain", "Domain",
			"projects", "Projects", "members", "Members", "admins", "Admins",
			"status", "Status"),
	},
	{
		// Mirrors the weft-agent ProjectInfo proto (name/uuid/created) so live
		// and mock data share one shape ; see internal/wclient.ListProjects.
		// The tenant column is the webui's addition — weft-agent carries the
		// tenant UUID on the project proto.
		ID: "projects", Label: "Projects", Section: "Identity",
		Columns: cols("name", "Name", "tenant", "Tenant", "uuid", "UUID", "created", "Created"),
	},
	{
		// Memberships column packs the per-tenant group list as
		// `tenant:group1,group2 / tenant2:groupN` — the existing table
		// layout stays compact, and a future detail drawer can show the
		// structured form.
		// Groups are tenant-scoped : the same name (`developers`) is a
		// different group in different tenants. Surface the tenant.
		// Listed BEFORE Users so the sidebar reads Tenants → Projects →
		// Groups → Users (the natural read order : a group is a
		// membership bucket the user lives inside).
		ID: "groups", Label: "Groups", Section: "Identity", Scope: ScopeAdmin,
		Columns: cols("name", "Name", "tenant", "Tenant",
			"description", "Description", "members", "Members"),
	},
	{
		ID: "users", Label: "Users", Section: "Identity", Scope: ScopeAdmin,
		Columns: cols("name", "Name", "email", "Email", "issuer", "Issuer",
			"memberships", "Memberships", "last_seen", "Last seen"),
	},

	// ---------- Compute ----------
	{
		// Flavor = compute envelope. Empty `gpu` means "no GPU required" ;
		// a non-empty value pins the matching microVM to a host that
		// physically carries the matching model (see the `gpu` column
		// on Hosts). The scheduler honours the match like any other
		// placement constraint — a flavor that requests `1×A100-40G`
		// won't land on a host with `0×` or `2×L4-24G`.
		//
		// Scope = Admin : only the superadmin defines flavors (they're
		// cluster-wide artefacts). The user UI still needs the catalogue
		// inside CreateVMModal — it gets that via the parallel
		// /api/flavors endpoint (handleListFlavors) which is exposed on
		// both listeners. Hiding the sidebar entry on the user UI
		// declutters Compute without breaking the create flow.
		ID: "flavors", Label: "Flavors", Section: "Compute", Scope: ScopeAdmin,
		Columns: cols("name", "Name", "vcpu", "vCPU", "ram", "RAM",
			"ephemeral_gb", "Ephemeral (GB)", "gpu", "GPU"),
		// Rows nil — flavors live in flavorsCatalogue (flavors.go) so
		// the swap to weft-agent's ListFlavors RPC (etcd-backed,
		// cluster-wide) is a single-implementation switch. The
		// handleResourceRows + handleListFlavors paths both project
		// through flavorRows() / flavorsCatalogue.Get().
	},
	{
		// Provisioning scripts — named, reusable sh bodies pickable
		// from CreateVMModal. Same model + same migration path as
		// flavors (in-memory catalogue today, etcd-backed when the
		// proto extension lands ; see internal/server/scripts.go).
		ID: "scripts", Label: "Scripts", Section: "Compute", Scope: ScopeAdmin,
		Columns: cols("name", "Name", "description", "Description",
			"lines", "Lines", "updated_at", "Updated", "updated_by", "By"),
		// Rows nil — served via scriptRows() from the catalogue.
	},
	{
		// SSH keys catalogue — operator-managed named keys (manual
		// entry or imported from GitHub / GitLab / Forgejo). VMs
		// reference them by name from the drawer.
		//
		// Visible on BOTH ports : every user needs to push their
		// own keys (laptops, yubikeys, gh:<self> imports). Write is
		// gated server-side on tenant_admin (or cluster_admin) ;
		// regular users see a read-only view + can request access.
		// Section "Identity" because keys are identity material,
		// not compute primitives — and that's where users
		// instinctively look.
		ID: "ssh-keys", Label: "SSH Keys", Section: "Identity",
		Columns: cols("name", "Name", "description", "Description",
			"fingerprint", "Fingerprint", "source", "Source",
			"source_account", "Account", "updated_at", "Updated", "updated_by", "By"),
		// Rows nil — served via sshKeyRows() from the catalogue.
	},
	{
		// Scheduling rules — declarative constraints the weft scheduler
		// honours when picking hosts for the matched workloads. Placed
		// above microVMs because the rule is conceptually upstream : it
		// shapes where new VMs will land before they exist.
		//
		// Each rule carries :
		//   count       desired replicas of the matching VMs
		//   selector    label expression matching the scheduled VMs
		//   placement   az / rack / host directives (same | different |
		//               <name>) drawn from the proximity hierarchy
		//               AZ ⊃ Rack ⊃ Host
		//
		// Status reflects observed compliance :
		//   compliant      ready == desired AND placement honoured
		//   drifting       ready < desired (replicas missing or wrong host)
		//   unschedulable  no host pool satisfies the directives
		//
		// "Scheduling Rules" rather than "Policies" so it doesn't collide
		// with Security Group / RBAC / Quota policies elsewhere in the UI.
		ID: "scheduling-rules", Label: "Scheduling Rules", Section: "Compute",
		Columns: cols("name", "Name", "count", "Count",
			"placement", "Placement", "selector", "Selector",
			"project", "Project", "status", "Status"),
		// Rows nil — served from schedulingDB (scheduling.go) so the
		// table can mutate. Seed fixtures live in the store.
	},
	{
		// Mirrors VMInfo (name, image, state → status, cpu, mem_mb, disk_gb,
		// ip, project). flavor/host/network stay on mock rows (not as columns)
		// so the topology view still finds the network attachment.
		ID: "microvms", Label: "microVMs", Section: "Compute",
		Columns: cols("name", "Name", "image", "Image", "status", "Status", "cpu", "CPU", "mem_mb", "Memory (MB)", "disk_gb", "Disk (GB)", "ip", "IP", "project", "Project"),
			},
	{
		ID: "instances", Label: "Instances (VM)", Section: "Compute",
		Columns: cols("name", "Name", "image", "Image", "flavor", "Flavor", "host", "Host", "network", "Network", "project", "Project", "status", "Status"),
			},

	// ---------- Storage ----------
	{
		// Mirrors VolumeInfo (name, size_gib, format, attached_to_uuid →
		// attached_to, project_uuid → project, created).
		ID: "volumes", Label: "Volumes", Section: "Storage",
		Columns: cols("name", "Name", "size_gib", "Size (GiB)", "format", "Format", "backend", "Backend", "attached_to", "Attached to", "project", "Project", "created", "Created"),
			},
	{
		// Shares are served from sharesDB (shares.go) so a tenant admin's
		// "Create share" round-trips. Rows stay nil here ; the switch in
		// handleResourceRows hits the store.
		ID: "shares", Label: "Shares", Section: "Storage",
		Columns: cols("name", "Name", "project", "Project", "backend", "Backend",
			"size_gb", "Size (GB)", "readonly", "Read-only", "mounts", "Mounts", "status", "Status"),
	},
	{
		// Object storage (CubeFS S3). Rows = bucket summaries (see
		// objectstorage.go) ; the dashboard renders a custom browser view.
		ID: "buckets", Label: "Buckets", Section: "Storage",
		Columns: cols("name", "Name", "objects", "Objects", "size", "Size", "created", "Created"),
		Rows:    nil,
	},
	{
		// iRODS collections — hierarchical data-management surface
		// contributed by the irods-data-management plugin (see
		// project_catalogue_irods_forgejo). Each row = one collection :
		// zone-qualified path, owner, resource (storage backend), object
		// count + total size. Empty until the plugin is installed and
		// at least one collection is created.
		ID: "irods-collections", Label: "iRODS Collections", Section: "Storage",
		Columns: cols("path", "Path", "owner", "Owner", "zone", "Zone",
			"resource", "Resource", "objects", "Objects", "size", "Size", "created", "Created"),
		Rows: nil,
	},
	{
		// OCI registries. Rows come from the artifact store (registry.go)
		// so uploads round-trip ; the dashboard renders a custom view
		// with two tabs (artifacts + remotes for proxy / replication).
		// Plural to match the other storage sub-sections (Volumes,
		// Shares, Buckets).
		ID: "registries", Label: "Registries", Section: "Storage",
		Columns: cols("repository", "Repository", "tag", "Tag", "type", "Type",
			"arch", "Architectures", "registry", "Registry", "size", "Size", "pushed", "Pushed"),
		Rows: nil,
	},

	// ---------- Database ----------
	{
		// DBaaS exposed by the integrated Vitess cluster. One row per
		// user-facing keyspace (logical database) — the underlying
		// shards / tablets stay an implementation detail until the
		// dashboard grows a per-keyspace drawer with shard topology.
		//
		// `engine` carries the MySQL-flavour version Vitess targets ;
		// `shards` reflects the sharding count (1 = unsharded) ;
		// `replicas` is the per-shard replica count (HA fan-out).
		// Gated by the vitess-dbaas plugin — the section only shows
		// up in the sidebar once the superadmin installs + enables
		// the plugin from the Admin → Plugins panel. The gating
		// logic is in plugins.go : a plugin's Resources slice
		// declares which resource ids it contributes ; built-in
		// resources (no plugin contributes them) are always open.
		ID: "databases", Label: "Databases", Section: "Database",
		Columns: cols("name", "Name", "engine", "Engine",
			"shards", "Shards", "replicas", "Replicas",
			"size_gb", "Size (GB)", "project", "Project",
			"status", "Status", "created", "Created"),
			},

	// ---------- Network ----------
	{
		// Graphical mesh map (custom SVG view). Rows are unused ; the sidebar
		// badge shows the number of networks (see rowCount). Served by
		// /api/network-topology.
		//
		// Admin-only : the node payload exposes host placement (which
		// host runs which microVM), which is infrastructure info that
		// project users don't need and shouldn't see.
		ID: "topology", Label: "Topology", Section: "Network", Scope: ScopeAdmin,
		Columns: nil,
		Rows:    nil,
	},
	{
		// Mirrors NetworkInfo (name, cidr, type, gateway, created). The "az"
		// field stays on mock rows (not as a column) because the topology
		// view reads it from the registry to label hubs.
		ID: "networks", Label: "Networks", Section: "Network",
		Columns: cols("name", "Name", "cidr", "CIDR", "type", "Type", "gateway", "Gateway", "created", "Created"),
			},
	{
		// DNS zones served by the per-DC CoreDNS microVMs. The platform
		// owns the root (`weft.internal`) ; each tenant carves a
		// subdomain (`<tenant>.weft.internal`) and each project carves
		// one below it. Backed by either CoreDNS file plugin (zone
		// transfers) or its etcd plugin (live writes from weft-network's
		// reconciler when VMs come up).
		//
		// `role` distinguishes the zone's place in the DNS hierarchy :
		//   primary    weft is authoritative ; can serve + push out
		//   secondary  weft is a slave ; receives AXFR/IXFR from a master
		//   forward    weft only forwards queries upstream
		//
		// `push_target` + `push_state` describe outbound RFC-2136 NS
		// updates : weft-network can push the primary zones to an
		// external BIND so the operator's existing public DNS keeps
		// the in-cluster names without manual edits. Empty target =
		// no external push (zone stays internal). push_state is one of
		// `idle`, `pushing`, `synced @<ts>`, `failed: <reason>`.
		// Hidden : the sidebar surfaces a single "DNS" entry (below)
		// that fetches zones + records and renders them in a unified
		// master-detail view. Rows still served via /api/resources/dns-zones/rows
		// for the dashboard's own consumption.
		ID: "dns-zones", Label: "DNS Zones", Section: "Network", Hidden: true,
		Columns: cols("name", "Name", "role", "Role",
			"records", "Records", "ttl_default", "Default TTL",
			"backend", "Backend", "push_target", "Push target",
			"push_state", "Push state",
			"project", "Project", "status", "Status"),
			},
	{
		// DNS records inside the zones above. `name` is the leaf (or `@`
		// for the apex) ; `zone` is the parent. Records flagged `auto`
		// are reconciled by weft-network from the live VM list — operator
		// edits to those are clobbered on the next reconcile.
		// Hidden : merged into the "DNS" sidebar entry via DNSPage.
		ID: "dns-records", Label: "DNS Records", Section: "Network", Hidden: true,
		Columns: cols("name", "Name", "zone", "Zone", "type", "Type",
			"value", "Value", "ttl", "TTL", "source", "Source"),
			},
	{
		// Unified DNS sidebar entry. The page renders zones on the
		// left + records of the selected zone on the right (custom
		// component in App.svelte). Row count = number of zones so
		// the sidebar badge is meaningful.
		ID: "dns", Label: "DNS", Section: "Network",
		Columns: nil,
		Rows:    nil,
	},
	{
		// Routers stitch meshes together (inter-tenant peering, mesh ↔ outside)
		// or expose a mesh to the public Internet. backend is the data-plane
		// realisation : "wireguard" for mesh ↔ mesh peering, "vyos" / "frr"
		// for outbound NAT/BGP.
		//
		// peer_state surfaces the live handshake info for WG peers (set by
		// weft-network's peer subsystem) ; egress routers leave it blank
		// since the BGP session is reported elsewhere.
		ID: "routers", Label: "Routers", Section: "Network",
		Columns: cols("name", "Name", "type", "Type", "backend", "Backend",
			"networks", "Networks", "external", "External",
			"peer_state", "Peer state",
			"project", "Project", "status", "Status"),
			},
	{
		// Load balancers : programmable L4/L7 in front of microVMs and
		// instances. The data plane is Caddy embedded in weft-agent
		// (sub-module weft-agent/proxy/) — one Caddy per host, no
		// separate infra microVM, no plugin.
		//
		// controller is the weft-network instance that currently owns
		// the reconcile stream for this LB (etcd-elected leader). When
		// the leader fails over a replica takes the column ; the local
		// Caddys keep serving from their last applied config.
		ID: "loadbalancers", Label: "Load Balancers", Section: "Network",
		Columns: cols("name", "Name", "mode", "Mode", "address", "VIP",
			"port", "Port", "backends", "Backends", "az", "AZ",
			"controller", "Controller",
			"project", "Project", "status", "Status"),
			},
	{
		// FloatingIPs are now wired to weft-agent's allocate / release /
		// map / unmap RPCs (live-first with mock fallback on
		// Unimplemented). `uuid` is on the row so the row-action
		// dropdown can address each address by its real handle.
		ID: "floating-ips", Label: "Floating IPs", Section: "Network",
		Columns: cols("address", "Address", "network", "Network",
			"mapped_to", "Mapped to", "status", "Status"),
			},
	{
		// Mirrors SecurityGroupInfo (uuid, name, description, rules count,
		// project, created). `uuid` is on the row so the row-action
		// dropdown's Delete can hit /api/security-groups/{uuid}.
		// Label "Security" (singular) — the per-group rules editor lives
		// inside the SG drawer (Rules tab), so the separate "Security
		// Rules" entry below is Hidden to avoid a redundant flat view.
		ID: "security-groups", Label: "Security", Section: "Network",
		Columns: cols("name", "Name", "description", "Description",
			"rules", "Rules", "enabled", "Enabled",
			"project", "Project", "created", "Created"),
			},
	{
		// Hidden : flat rules view is redundant with the per-group rules
		// editor in SecurityGroupDrawer. Kept in the catalogue so the
		// rows are still fetchable by anyone who wants the firehose.
		ID: "security-rules", Label: "Security Rules", Section: "Network", Hidden: true,
		Columns: cols("group", "Group", "direction", "Direction", "protocol", "Protocol", "port_range", "Ports", "remote", "Remote"),
			},

	// ---------- Admin > Inventory ----------
	//
	// Three-level hierarchy : AZ → Rack → Host (multi-hypervisor) →
	// microVM. Sidebar surfaces TWO entries under Admin :
	//   - "Inventory" (inventory-tree) : the primary CRUD surface,
	//     a collapsible tree with right-pane details.
	//   - "Map" (inventory-map) : the isometric placement viz,
	//     read-only.
	// The flat tables (azs / racks / hosts) stay reachable for power
	// users via the tree's "Open in panel" links, but they're hidden
	// from the sidebar to keep the surface tight.
	{
		// Inventory tree — the primary placement editor + canonical
		// "Inventory" sidebar entry. Hidden from /api/resources
		// catalogue listing semantics aren't needed (we want it in
		// the sidebar) but it has no Rows of its own ; the dashboard
		// mounts InventoryTreePage rather than the generic table.
		ID: "inventory-tree", Label: "Inventory", Section: "Admin", Scope: ScopeAdmin,
		Columns: cols("placeholder", "—"),
			},
	{
		// Availability Zones — datacenters / fault domains the cluster
		// spans. Hidden from the sidebar (Hidden=true) so it doesn't
		// duplicate the tree ; remains reachable via the tree's
		// "Open in panel" button or by direct ?active=azs deep-link.
		ID: "azs", Label: "Availability Zones", Section: "Admin", Scope: ScopeAdmin, Hidden: true,
		Columns: cols("code", "Code", "name", "Name", "region", "Region",
			"racks", "Racks", "hosts", "Hosts", "status", "Status"),
			},
	{
		// Racks live inside an AZ. Position = physical row/column ;
		// the isometric map uses it to lay racks out on the AZ
		// ground plane. Empty position means "first available slot".
		// Hidden from the sidebar — tree-only surface.
		ID: "racks", Label: "Racks", Section: "Admin", Scope: ScopeAdmin, Hidden: true,
		Columns: cols("code", "Code", "az", "AZ", "position", "Position",
			"height_u", "Height (U)", "hosts", "Hosts", "status", "Status"),
			},
	{
		// Mirrors HostInfo (hostname → name, az, rack, architecture → arch,
		// hypervisor, state → status, last_seen). cpu/ram aren't on the
		// HostInfo proto — they live with the host's runtime stats and
		// would come from a separate RPC ; dropped from this view.
		//
		// `gpu` advertises the host's physical GPU complement using the
		// same notation the Flavor table uses. Empty means no GPU. The
		// scheduler matches Flavor.gpu against Hosts.gpu — a host with
		// "2×A100-40G" can land any number of microVMs requesting an
		// "A100-40G" until the count is exhausted.
		ID: "hosts", Label: "Hosts", Section: "Admin", Scope: ScopeAdmin, Hidden: true,
		Columns: cols("name", "Name", "az", "AZ", "rack", "Rack",
			"arch", "Arch", "hypervisor", "Hypervisor", "gpu", "GPU",
			"position_u", "U", "height_u", "Size (U)",
			"status", "Status", "last_seen", "Last seen"),
			},
	{
		// Inventory map — the isometric placement view. Lives in the
		// Admin section alongside Inventory + Plugins ; dashboard
		// mounts a dedicated panel rather than the generic table.
		ID: "inventory-map", Label: "Map", Section: "Admin", Scope: ScopeAdmin,
		Columns: cols("placeholder", "—"),
			},
	{
		// Audit log — read-only browser over /api/audit-log. No rows
		// here ; the dashboard mounts AuditLogPage which tails the
		// configured JSONL file directly.
		ID: "audit-log", Label: "Audit log", Section: "Admin", Scope: ScopeAdmin,
		Columns: cols("placeholder", "—"),
			},
	{
		// Plugin registry — *-as-a-service modules the cluster can
		// host (Database / Streaming / Cache / Object lake …). Rows
		// are served from pluginsByID via the /api/plugins endpoints
		// so install / enable round-trip ; columns here drive the
		// catalogue listing only.
		ID: "plugins", Label: "Plugins", Section: "Admin", Scope: ScopeAdmin,
		Columns: cols("id", "ID", "name", "Name", "vendor", "Vendor",
			"version", "Version", "section", "Section",
			"install_status", "Install", "enabled", "Enabled"),
		Rows: nil, // populated dynamically — see api_plugins.go
	},
}

// resourceByID indexes the registry for quick lookup.
var resourceByID = func() map[string]*Resource {
	m := make(map[string]*Resource, len(registry))
	for i := range registry {
		m[registry[i].ID] = &registry[i]
	}
	return m
}()
