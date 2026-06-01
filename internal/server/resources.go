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
	ScopeUser  Scope = 1 << iota // visible on the user UI (project-scoped)
	ScopeAdmin                   // visible on the admin UI (cluster-wide)
)

// ScopeBoth is the default for project-scoped resources : the user
// sees their own, the admin sees a global view (weft-agent applies the filter).
const ScopeBoth = ScopeUser | ScopeAdmin

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
		ID: "users", Label: "Users", Section: "Identity", Scope: ScopeAdmin,
		Columns: cols("name", "Name", "email", "Email", "issuer", "Issuer",
			"memberships", "Memberships", "last_seen", "Last seen"),
	},
	{
		// Groups are tenant-scoped : the same name (`developers`) is a
		// different group in different tenants. Surface the tenant.
		ID: "groups", Label: "Groups", Section: "Identity", Scope: ScopeAdmin,
		Columns: cols("name", "Name", "tenant", "Tenant",
			"description", "Description", "members", "Members"),
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
		Rows: []map[string]any{
			row("name", "web-1", "image", "alpine:3.21", "status", "running", "cpu", 2, "mem_mb", 4096, "disk_gb", 10, "ip", "10.10.0.21", "project", "team-alpha", "host", "dc-a-r1-h2", "network", "tenant-net-1", "flavor", "small", "scheduling_rule", "web-tier"),
			row("name", "web-2", "image", "alpine:3.21", "status", "running", "cpu", 2, "mem_mb", 4096, "disk_gb", 10, "ip", "10.10.0.22", "project", "team-alpha", "host", "dc-b-r1-h1", "network", "tenant-net-1", "flavor", "small", "scheduling_rule", "web-tier"),
			// nb-1 uses gpu-small : the scheduler placed it on dc-c-r2-h1
			// because that's the host carrying an A100-40G in its pool.
			row("name", "nb-1", "image", "jupyter:cuda12", "status", "running", "cpu", 4, "mem_mb", 16384, "disk_gb", 32, "ip", "10.20.0.13", "project", "research", "host", "dc-c-r2-h1", "network", "tenant-net-2", "flavor", "gpu-small"),
			row("name", "ci-job-7f3", "image", "buildkit:latest", "status", "running", "cpu", 4, "mem_mb", 8192, "disk_gb", 30, "ip", "10.10.0.42", "project", "team-beta", "host", "dc-b-r1-h3", "network", "tenant-net-1", "flavor", "medium"),
			// model-train-1 demands gpu-large → pinned to the H100 pool.
			row("name", "model-train-1", "image", "pytorch:2.4-cuda12", "status", "running", "cpu", 32, "mem_mb", 262144, "disk_gb", 256, "ip", "10.20.0.30", "project", "research", "host", "dc-c-r3-h2", "network", "tenant-net-2", "flavor", "gpu-large"),
			// research-batch members (count=5, ready=4 → one missing).
			row("name", "batch-001", "image", "ubuntu:24.04", "status", "running", "cpu", 8, "mem_mb", 32768, "disk_gb", 64, "ip", "10.20.0.41", "project", "research", "host", "dc-c-r2-h2", "network", "tenant-net-2", "flavor", "large", "scheduling_rule", "research-batch"),
			row("name", "batch-002", "image", "ubuntu:24.04", "status", "running", "cpu", 8, "mem_mb", 32768, "disk_gb", 64, "ip", "10.20.0.42", "project", "research", "host", "dc-c-r2-h3", "network", "tenant-net-2", "flavor", "large", "scheduling_rule", "research-batch"),
			row("name", "batch-003", "image", "ubuntu:24.04", "status", "running", "cpu", 8, "mem_mb", 32768, "disk_gb", 64, "ip", "10.20.0.43", "project", "research", "host", "dc-a-r1-h1", "network", "tenant-net-2", "flavor", "large", "scheduling_rule", "research-batch"),
			row("name", "batch-004", "image", "ubuntu:24.04", "status", "running", "cpu", 8, "mem_mb", 32768, "disk_gb", 64, "ip", "10.20.0.44", "project", "research", "host", "dc-a-r2-h1", "network", "tenant-net-2", "flavor", "large", "scheduling_rule", "research-batch"),
		},
	},
	{
		ID: "instances", Label: "Instances (VM)", Section: "Compute",
		Columns: cols("name", "Name", "image", "Image", "flavor", "Flavor", "host", "Host", "network", "Network", "project", "Project", "status", "Status"),
		Rows: []map[string]any{
			row("name", "legacy-app", "image", "debian-12.qcow2", "flavor", "large", "host", "dc-a-r3-h1", "network", "tenant-net-1", "project", "team-beta", "status", "running"),
			row("name", "win-build", "image", "windows-2022.qcow2", "flavor", "xlarge", "host", "dc-b-r2-h2", "network", "tenant-net-2", "project", "team-beta", "status", "stopped"),
		},
	},

	// ---------- Storage ----------
	{
		// Mirrors VolumeInfo (name, size_gib, format, attached_to_uuid →
		// attached_to, project_uuid → project, created).
		ID: "volumes", Label: "Volumes", Section: "Storage",
		Columns: cols("name", "Name", "size_gib", "Size (GiB)", "format", "Format", "attached_to", "Attached to", "project", "Project", "created", "Created"),
		Rows: []map[string]any{
			row("name", "pg-data", "size_gib", 200, "format", "raw", "attached_to", "db-1", "project", "team-alpha", "created", "2026-04-14"),
			row("name", "cubefs-d0", "size_gib", 500, "format", "raw", "attached_to", "cubefs-data-0", "project", "team-beta", "created", "2026-04-02"),
			row("name", "scratch-1", "size_gib", 50, "format", "qcow2", "attached_to", "", "project", "team-alpha", "created", "2026-05-01"),
		},
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
		Rows: []map[string]any{
			row("name", "team-alpha-prod", "engine", "Vitess · MySQL 8.0",
				"shards", 8, "replicas", 3, "size_gb", 200,
				"project", "team-alpha", "status", "active", "created", "2026-04-10"),
			row("name", "team-alpha-staging", "engine", "Vitess · MySQL 8.0",
				"shards", 2, "replicas", 2, "size_gb", 50,
				"project", "team-alpha", "status", "active", "created", "2026-04-12"),
			row("name", "research-warehouse", "engine", "Vitess · MySQL 8.0",
				"shards", 16, "replicas", 3, "size_gb", 2048,
				"project", "research", "status", "active", "created", "2026-03-22"),
			row("name", "team-beta-orders", "engine", "Vitess · MySQL 8.0",
				"shards", 4, "replicas", 3, "size_gb", 120,
				"project", "team-beta", "status", "active", "created", "2026-05-02"),
			row("name", "scratch-1", "engine", "Vitess · MySQL 8.0",
				"shards", 1, "replicas", 1, "size_gb", 10,
				"project", "team-alpha", "status", "provisioning", "created", "2026-05-29"),
		},
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
		Rows: []map[string]any{
			row("name", "mgmt", "cidr", "10.0.0.0/16", "type", "wireguard", "gateway", "10.0.0.1", "created", "2026-03-01", "az", "DC-A"),
			row("name", "tenant-net-1", "cidr", "10.10.0.0/16", "type", "overlay", "gateway", "10.10.0.1", "created", "2026-03-10", "az", "DC-A"),
			row("name", "tenant-net-2", "cidr", "10.20.0.0/16", "type", "overlay", "gateway", "10.20.0.1", "created", "2026-03-12", "az", "DC-B"),
		},
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
		Rows: []map[string]any{
			row("name", "weft.internal", "role", "primary",
				"records", 9, "ttl_default", 60, "backend", "coredns",
				"push_target", "", "push_state", "",
				"project", "platform", "status", "active"),
			row("name", "acme.weft.internal", "role", "primary",
				"records", 5, "ttl_default", 60, "backend", "coredns",
				"push_target", "ns1.acme.example (bind9, tsig acme-key)",
				"push_state", "synced @ 2026-05-28 16:42:11",
				"project", "platform", "status", "active"),
			row("name", "team-alpha.acme.weft.internal", "role", "primary",
				"records", 4, "ttl_default", 30, "backend", "coredns",
				"push_target", "", "push_state", "",
				"project", "team-alpha", "status", "active"),
			row("name", "research.globex.weft.internal", "role", "primary",
				"records", 2, "ttl_default", 30, "backend", "coredns",
				"push_target", "ns1.globex.example (bind9, tsig globex-key)",
				"push_state", "failed: SERVFAIL from ns1.globex.example",
				"project", "research", "status", "active"),
			// One forward example so the dropdown shows the three roles.
			row("name", "corp.example", "role", "forward",
				"records", 0, "ttl_default", 0, "backend", "coredns",
				"push_target", "", "push_state", "",
				"project", "platform", "status", "active"),
		},
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
		Rows: []map[string]any{
			// Platform service discovery — the SRVs that anchor `weft agent`.
			row("name", "_weft._tcp", "zone", "weft.internal", "type", "SRV",
				"value", "0 33 7443 weft-a.weft.internal.", "ttl", 60, "source", "static"),
			row("name", "_weft._tcp", "zone", "weft.internal", "type", "SRV",
				"value", "0 33 7443 weft-b.weft.internal.", "ttl", 60, "source", "static"),
			row("name", "_weft._tcp", "zone", "weft.internal", "type", "SRV",
				"value", "0 33 7443 weft-c.weft.internal.", "ttl", 60, "source", "static"),
			row("name", "weft-a", "zone", "weft.internal", "type", "A",
				"value", "10.0.0.10", "ttl", 60, "source", "static"),
			// Tenant-level glue.
			row("name", "@", "zone", "acme.weft.internal", "type", "NS",
				"value", "ns.weft.internal.", "ttl", 3600, "source", "static"),
			// Project-level, auto-reconciled from the live microVM list.
			row("name", "web-1", "zone", "team-alpha.acme.weft.internal", "type", "A",
				"value", "10.10.0.21", "ttl", 30, "source", "auto"),
			row("name", "db", "zone", "team-alpha.acme.weft.internal", "type", "CNAME",
				"value", "db-1.team-alpha.acme.weft.internal.", "ttl", 60, "source", "static"),
			// Public ingress, fed from the LoadBalancer table.
			row("name", "web", "zone", "acme.weft.internal", "type", "A",
				"value", "203.0.113.20", "ttl", 60, "source", "auto"),
		},
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
		Rows: []map[string]any{
			row("name", "edge-dca", "type", "egress", "backend", "vyos",
				"networks", "tenant-net-1, mgmt", "external", "AS65010",
				"peer_state", "",
				"project", "platform", "status", "active"),
			row("name", "edge-dcb", "type", "egress", "backend", "vyos",
				"networks", "tenant-net-2, mgmt", "external", "AS65010",
				"peer_state", "",
				"project", "platform", "status", "active"),
			row("name", "peer-alpha-beta", "type", "peer", "backend", "wireguard",
				"networks", "tenant-net-1, tenant-net-2", "external", "—",
				"peer_state", "handshake 12s ago · rx 4.2 MiB · tx 1.8 MiB",
				"project", "team-alpha", "status", "active"),
		},
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
		Rows: []map[string]any{
			row("uuid", "lb01-0000-0000-0000-101010101010",
				"name", "web-prod", "mode", "L7", "address", "203.0.113.20", "port", 443,
				"backends", "web-1, web-2", "az", "multi",
				"controller", "weft-network-dca",
				"project", "team-alpha", "status", "active"),
			row("uuid", "lb01-0000-0000-0000-202020202020",
				"name", "pg-rw", "mode", "L4", "address", "10.10.0.100", "port", 5432,
				"backends", "db-1, db-2", "az", "DC-A",
				"controller", "weft-network-dca",
				"project", "team-alpha", "status", "active"),
			row("uuid", "lb01-0000-0000-0000-303030303030",
				"name", "jupyter", "mode", "L7", "address", "203.0.113.21", "port", 443,
				"backends", "nb-1", "az", "DC-C",
				"controller", "weft-network-dcc",
				"project", "research", "status", "active"),
		},
	},
	{
		// FloatingIPs are now wired to weft-agent's allocate / release /
		// map / unmap RPCs (live-first with mock fallback on
		// Unimplemented). `uuid` is on the row so the row-action
		// dropdown can address each address by its real handle.
		ID: "floating-ips", Label: "Floating IPs", Section: "Network",
		Columns: cols("address", "Address", "network", "Network",
			"mapped_to", "Mapped to", "status", "Status"),
		Rows: []map[string]any{
			row("uuid", "fip-1010-1010-1010-101010101010",
				"address", "203.0.113.10", "network", "edge",
				"mapped_to", "web-1", "status", "active"),
			row("uuid", "fip-1010-1010-1010-202020202020",
				"address", "203.0.113.11", "network", "edge",
				"mapped_to", "", "status", "available"),
			row("uuid", "fip-1010-1010-1010-303030303030",
				"address", "203.0.113.12", "network", "edge",
				"mapped_to", "nb-1", "status", "active"),
		},
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
		Rows: []map[string]any{
			row("name", "default", "uuid", "a1c0e7d2-9f01-4811-b6a5-101010101010",
				"description", "Default deny-in / allow-out", "rules", 2, "enabled", true,
				"project", "team-alpha", "created", "2026-04-12"),
			row("name", "web", "uuid", "a1c0e7d2-9f01-4811-b6a5-202020202020",
				"description", "HTTP/HTTPS ingress", "rules", 3, "enabled", true,
				"project", "team-alpha", "created", "2026-04-14"),
			row("name", "db", "uuid", "a1c0e7d2-9f01-4811-b6a5-303030303030",
				"description", "Postgres from web only", "rules", 1, "enabled", true,
				"project", "team-beta", "created", "2026-04-20"),
		},
	},
	{
		// Hidden : flat rules view is redundant with the per-group rules
		// editor in SecurityGroupDrawer. Kept in the catalogue so the
		// rows are still fetchable by anyone who wants the firehose.
		ID: "security-rules", Label: "Security Rules", Section: "Network", Hidden: true,
		Columns: cols("group", "Group", "direction", "Direction", "protocol", "Protocol", "port_range", "Ports", "remote", "Remote"),
		Rows: []map[string]any{
			row("group", "web", "direction", "ingress", "protocol", "tcp", "port_range", "443", "remote", "0.0.0.0/0"),
			row("group", "web", "direction", "ingress", "protocol", "tcp", "port_range", "80", "remote", "0.0.0.0/0"),
			row("group", "db", "direction", "ingress", "protocol", "tcp", "port_range", "5432", "remote", "web"),
		},
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
		Rows:    []map[string]any{},
	},
	{
		// Availability Zones — datacenters / fault domains the cluster
		// spans. Hidden from the sidebar (Hidden=true) so it doesn't
		// duplicate the tree ; remains reachable via the tree's
		// "Open in panel" button or by direct ?active=azs deep-link.
		ID: "azs", Label: "Availability Zones", Section: "Admin", Scope: ScopeAdmin, Hidden: true,
		Columns: cols("code", "Code", "name", "Name", "region", "Region",
			"racks", "Racks", "hosts", "Hosts", "status", "Status"),
		Rows: []map[string]any{
			row("uuid", "az00-0000-0000-0000-aaaaaaaaaaaa",
				"code", "DC-A", "name", "Datacenter Alpha", "region", "eu-west-1",
				"racks", 3, "hosts", 6, "status", "active"),
			row("uuid", "az00-0000-0000-0000-bbbbbbbbbbbb",
				"code", "DC-B", "name", "Datacenter Bravo", "region", "eu-west-2",
				"racks", 2, "hosts", 4, "status", "active"),
			row("uuid", "az00-0000-0000-0000-cccccccccccc",
				"code", "DC-C", "name", "Datacenter Charlie", "region", "us-east-1",
				"racks", 3, "hosts", 5, "status", "active"),
		},
	},
	{
		// Racks live inside an AZ. Position = physical row/column ;
		// the isometric map uses it to lay racks out on the AZ
		// ground plane. Empty position means "first available slot".
		// Hidden from the sidebar — tree-only surface.
		ID: "racks", Label: "Racks", Section: "Admin", Scope: ScopeAdmin, Hidden: true,
		Columns: cols("code", "Code", "az", "AZ", "position", "Position",
			"hosts", "Hosts", "status", "Status"),
		Rows: []map[string]any{
			// DC-A : 3 racks
			row("uuid", "rk00-aaaa-0000-0000-000000000001", "code", "R1", "az", "DC-A", "position", "row1-col1", "hosts", 2, "status", "active"),
			row("uuid", "rk00-aaaa-0000-0000-000000000002", "code", "R2", "az", "DC-A", "position", "row1-col2", "hosts", 2, "status", "active"),
			row("uuid", "rk00-aaaa-0000-0000-000000000003", "code", "R3", "az", "DC-A", "position", "row2-col1", "hosts", 2, "status", "active"),
			// DC-B : 2 racks
			row("uuid", "rk00-bbbb-0000-0000-000000000001", "code", "R1", "az", "DC-B", "position", "row1-col1", "hosts", 2, "status", "active"),
			row("uuid", "rk00-bbbb-0000-0000-000000000002", "code", "R2", "az", "DC-B", "position", "row1-col2", "hosts", 2, "status", "active"),
			// DC-C : 3 racks
			row("uuid", "rk00-cccc-0000-0000-000000000001", "code", "R1", "az", "DC-C", "position", "row1-col1", "hosts", 1, "status", "active"),
			row("uuid", "rk00-cccc-0000-0000-000000000002", "code", "R2", "az", "DC-C", "position", "row1-col2", "hosts", 2, "status", "active"),
			row("uuid", "rk00-cccc-0000-0000-000000000003", "code", "R3", "az", "DC-C", "position", "row2-col1", "hosts", 2, "status", "active"),
		},
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
			"status", "Status", "last_seen", "Last seen"),
		Rows: []map[string]any{
			row("name", "dc-a-r1-h2", "az", "DC-A", "rack", "R1", "arch", "arm64", "hypervisor", "apple-vz",
				"gpu", "",
				"status", "active", "last_seen", "2026-05-27"),
			row("name", "dc-b-r1-h3", "az", "DC-B", "rack", "R1", "arch", "amd64", "hypervisor", "qemu-kvm",
				"gpu", "",
				"status", "active", "last_seen", "2026-05-27"),
			row("name", "dc-c-r2-h1", "az", "DC-C", "rack", "R2", "arch", "arm64", "hypervisor", "qemu-kvm",
				"gpu", "2×A100-40G",
				"status", "draining", "last_seen", "2026-05-26"),
			row("name", "dc-c-r3-h2", "az", "DC-C", "rack", "R3", "arch", "amd64", "hypervisor", "qemu-kvm",
				"gpu", "4×H100-80G",
				"status", "active", "last_seen", "2026-05-28"),
		},
	},
	{
		// Inventory map — the isometric placement view. Lives in the
		// Admin section alongside Inventory + Plugins ; dashboard
		// mounts a dedicated panel rather than the generic table.
		ID: "inventory-map", Label: "Map", Section: "Admin", Scope: ScopeAdmin,
		Columns: cols("placeholder", "—"),
		Rows:    []map[string]any{},
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
