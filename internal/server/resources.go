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
		ID: "flavors", Label: "Flavors", Section: "Compute",
		Columns: cols("name", "Name", "vcpu", "vCPU", "ram", "RAM", "ephemeral_gb", "Ephemeral (GB)"),
		Rows: []map[string]any{
			row("name", "small", "vcpu", 2, "ram", "4Gi", "ephemeral_gb", 8),
			row("name", "medium", "vcpu", 4, "ram", "8Gi", "ephemeral_gb", 16),
			row("name", "large", "vcpu", 8, "ram", "32Gi", "ephemeral_gb", 32),
			row("name", "xlarge", "vcpu", 16, "ram", "64Gi", "ephemeral_gb", 64),
		},
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
			row("name", "web-1", "image", "alpine:3.21", "status", "running", "cpu", 2, "mem_mb", 4096, "disk_gb", 10, "ip", "10.10.0.21", "project", "team-alpha", "host", "dc-a-r1-h2", "network", "tenant-net-1", "flavor", "small"),
			row("name", "nb-1", "image", "jupyter:latest", "status", "running", "cpu", 2, "mem_mb", 4096, "disk_gb", 20, "ip", "10.20.0.13", "project", "research", "host", "dc-c-r2-h1", "network", "tenant-net-2", "flavor", "small"),
			row("name", "ci-job-7f3", "image", "buildkit:latest", "status", "running", "cpu", 4, "mem_mb", 8192, "disk_gb", 30, "ip", "10.10.0.42", "project", "team-beta", "host", "dc-b-r1-h3", "network", "tenant-net-1", "flavor", "medium"),
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
		ID: "shares", Label: "Shares", Section: "Storage",
		Columns: cols("name", "Name", "backend", "Backend", "size_gb", "Size (GB)", "mounts", "Mounts", "status", "Status"),
		Rows: []map[string]any{
			row("name", "team-data", "backend", "cubefs", "size_gb", 2048, "mounts", 6, "status", "active"),
			row("name", "notebooks", "backend", "cubefs", "size_gb", 512, "mounts", 9, "status", "active"),
			row("name", "models", "backend", "cubefs", "size_gb", 4096, "mounts", 3, "status", "active"),
		},
	},
	{
		// Object storage (CubeFS S3). Rows = bucket summaries (see
		// objectstorage.go) ; the dashboard renders a custom browser view.
		ID: "buckets", Label: "Buckets", Section: "Storage",
		Columns: cols("name", "Name", "objects", "Objects", "size", "Size", "created", "Created"),
		Rows:    nil,
	},
	{
		// OCI registry. Rows come from the artifact store (registry.go)
		// so uploads round-trip ; the dashboard renders a custom view.
		// "registry" rather than "images" because the registry holds
		// any OCI artifact (containers, raw disks, charts, models…).
		ID: "registry", Label: "Registry", Section: "Storage",
		Columns: cols("repository", "Repository", "tag", "Tag", "type", "Type",
			"arch", "Architectures", "registry", "Registry", "size", "Size", "pushed", "Pushed"),
		Rows: nil,
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
		ID: "dns-zones", Label: "DNS Zones", Section: "Network",
		Columns: cols("name", "Name", "type", "Type",
			"records", "Records", "ttl_default", "Default TTL",
			"backend", "Backend", "project", "Project", "status", "Status"),
		Rows: []map[string]any{
			row("name", "weft.internal", "type", "authoritative",
				"records", 9, "ttl_default", 60, "backend", "coredns",
				"project", "platform", "status", "active"),
			row("name", "acme.weft.internal", "type", "authoritative",
				"records", 5, "ttl_default", 60, "backend", "coredns",
				"project", "platform", "status", "active"),
			row("name", "team-alpha.acme.weft.internal", "type", "authoritative",
				"records", 4, "ttl_default", 30, "backend", "coredns",
				"project", "team-alpha", "status", "active"),
			row("name", "research.globex.weft.internal", "type", "authoritative",
				"records", 2, "ttl_default", 30, "backend", "coredns",
				"project", "research", "status", "active"),
		},
	},
	{
		// DNS records inside the zones above. `name` is the leaf (or `@`
		// for the apex) ; `zone` is the parent. Records flagged `auto`
		// are reconciled by weft-network from the live VM list — operator
		// edits to those are clobbered on the next reconcile.
		ID: "dns-records", Label: "DNS Records", Section: "Network",
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
		// instances. Backed by Envoy on dedicated infra microVMs (one per
		// DC : envoy-dca / envoy-dcb / envoy-dcc, picked via SRV — same
		// shape as the weft agent endpoints).
		//
		// controller is the weft-network instance that currently owns the
		// xDS stream for this LB (etcd-elected leader). When the leader
		// fails over a replica takes the column ; the data-plane Envoys
		// don't notice.
		ID: "loadbalancers", Label: "Load Balancers", Section: "Network",
		Columns: cols("name", "Name", "mode", "Mode", "address", "VIP",
			"port", "Port", "backends", "Backends", "az", "AZ",
			"controller", "Controller",
			"project", "Project", "status", "Status"),
		Rows: []map[string]any{
			row("name", "web-prod", "mode", "L7", "address", "203.0.113.20", "port", 443,
				"backends", "web-1, web-2", "az", "multi",
				"controller", "weft-network-dca",
				"project", "team-alpha", "status", "active"),
			row("name", "pg-rw", "mode", "L4", "address", "10.10.0.100", "port", 5432,
				"backends", "db-1, db-2", "az", "DC-A",
				"controller", "weft-network-dca",
				"project", "team-alpha", "status", "active"),
			row("name", "jupyter", "mode", "L7", "address", "203.0.113.21", "port", 443,
				"backends", "nb-1", "az", "DC-C",
				"controller", "weft-network-dcc",
				"project", "research", "status", "active"),
		},
	},
	{
		ID: "floating-ips", Label: "Floating IPs", Section: "Network",
		Columns: cols("address", "Address", "network", "Network", "mapped_to", "Mapped to", "status", "Status"),
		Rows: []map[string]any{
			row("address", "203.0.113.10", "network", "edge", "mapped_to", "web-1", "status", "active"),
			row("address", "203.0.113.11", "network", "edge", "mapped_to", "", "status", "available"),
			row("address", "203.0.113.12", "network", "edge", "mapped_to", "nb-1", "status", "active"),
		},
	},
	{
		// Mirrors SecurityGroupInfo (name, description, rules count, project,
		// created).
		ID: "security-groups", Label: "Security Groups", Section: "Network",
		Columns: cols("name", "Name", "description", "Description", "rules", "Rules", "project", "Project", "created", "Created"),
		Rows: []map[string]any{
			row("name", "default", "description", "Default deny-in / allow-out", "rules", 2, "project", "team-alpha", "created", "2026-04-12"),
			row("name", "web", "description", "HTTP/HTTPS ingress", "rules", 3, "project", "team-alpha", "created", "2026-04-14"),
			row("name", "db", "description", "Postgres from web only", "rules", 1, "project", "team-beta", "created", "2026-04-20"),
		},
	},
	{
		ID: "security-rules", Label: "Security Rules", Section: "Network",
		Columns: cols("group", "Group", "direction", "Direction", "protocol", "Protocol", "port_range", "Ports", "remote", "Remote"),
		Rows: []map[string]any{
			row("group", "web", "direction", "ingress", "protocol", "tcp", "port_range", "443", "remote", "0.0.0.0/0"),
			row("group", "web", "direction", "ingress", "protocol", "tcp", "port_range", "80", "remote", "0.0.0.0/0"),
			row("group", "db", "direction", "ingress", "protocol", "tcp", "port_range", "5432", "remote", "web"),
		},
	},

	// ---------- Admin ----------
	{
		// Mirrors HostInfo (hostname → name, az, rack, architecture → arch,
		// hypervisor, state → status, last_seen). cpu/ram aren't on the
		// HostInfo proto — they live with the host's runtime stats and
		// would come from a separate RPC ; dropped from this view.
		ID: "hosts", Label: "Hosts", Section: "Admin", Scope: ScopeAdmin,
		Columns: cols("name", "Name", "az", "AZ", "rack", "Rack", "arch", "Arch", "hypervisor", "Hypervisor", "status", "Status", "last_seen", "Last seen"),
		Rows: []map[string]any{
			row("name", "dc-a-r1-h2", "az", "DC-A", "rack", "R1", "arch", "arm64", "hypervisor", "apple-vz", "status", "active", "last_seen", "2026-05-27"),
			row("name", "dc-b-r1-h3", "az", "DC-B", "rack", "R1", "arch", "amd64", "hypervisor", "qemu-kvm", "status", "active", "last_seen", "2026-05-27"),
			row("name", "dc-c-r2-h1", "az", "DC-C", "rack", "R2", "arch", "arm64", "hypervisor", "qemu-kvm", "status", "draining", "last_seen", "2026-05-26"),
		},
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
