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

// Resource is one Weft object type surfaced in the UI.
type Resource struct {
	ID      string           `json:"id"`      // url slug, e.g. "floating-ips"
	Label   string           `json:"label"`   // sidebar label, e.g. "Floating IPs"
	Section string           `json:"section"` // grouping, e.g. "Network"
	Columns []Column         `json:"columns"`
	Rows    []map[string]any `json:"-"` // served separately via /api/resources/{id}
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
	{
		ID: "tenants", Label: "Tenants", Section: "Identity",
		Columns: cols("name", "Name", "domain", "Domain", "projects", "Projects", "status", "Status"),
		Rows: []map[string]any{
			row("name", "acme", "domain", "acme.example", "projects", 4, "status", "active"),
			row("name", "globex", "domain", "globex.example", "projects", 2, "status", "active"),
			row("name", "initech", "domain", "initech.example", "projects", 1, "status", "disabled"),
		},
	},
	{
		// Mirrors the vzd ProjectInfo proto (name/uuid/created) so live and
		// mock data share one shape ; see internal/wclient.ListProjects.
		ID: "projects", Label: "Projects", Section: "Identity",
		Columns: cols("name", "Name", "uuid", "UUID", "created", "Created"),
		Rows: []map[string]any{
			row("name", "team-alpha", "uuid", "1c5d8a9e-7c11-4d2a-9c5e-aab742c0a112", "created", "2026-04-12"),
			row("name", "team-beta", "uuid", "2d3e9b7c-8e22-4ab3-9b0e-bbe853d1b223", "created", "2026-04-18"),
			row("name", "research", "uuid", "3f6abcd2-9f33-4ec4-8a1f-ccf964e2c334", "created", "2026-05-03"),
		},
	},
	{
		// Mirrors UserInfo (display_name → name, email, oidc_issuer → issuer,
		// groups, last_seen). The ResourceTable bolds the "name" key.
		ID: "users", Label: "Users", Section: "Identity",
		Columns: cols("name", "Name", "email", "Email", "issuer", "Issuer", "groups", "Groups", "last_seen", "Last seen"),
		Rows: []map[string]any{
			row("name", "Yannick", "email", "yann@acme.example", "issuer", "dex", "groups", "admins, developers", "last_seen", "2026-05-27"),
			row("name", "Alice", "email", "alice@acme.example", "issuer", "dex", "groups", "developers", "last_seen", "2026-05-26"),
			row("name", "Bob", "email", "bob@globex.example", "issuer", "dex", "groups", "viewers", "last_seen", "2026-05-12"),
		},
	},
	{
		ID: "groups", Label: "Groups", Section: "Identity",
		Columns: cols("name", "Name", "description", "Description", "members", "Members"),
		Rows: []map[string]any{
			row("name", "admins", "description", "Platform operators", "members", 3),
			row("name", "developers", "description", "Read/write on team projects", "members", 12),
			row("name", "viewers", "description", "Read-only", "members", 27),
		},
	},

	// ---------- Network ----------
	{
		// Graphical mesh map (custom SVG view). Rows are unused ; the sidebar
		// badge shows the number of networks (see rowCount). Served by
		// /api/network-topology.
		ID: "topology", Label: "Topology", Section: "Network",
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
		// OCI image registry. Rows come from the images store (see images.go)
		// so uploads round-trip ; the dashboard renders a custom view.
		ID: "images", Label: "Images", Section: "Storage",
		Columns: cols("repository", "Repository", "tag", "Tag", "type", "Type",
			"arch", "Architectures", "registry", "Registry", "size", "Size", "pushed", "Pushed"),
		Rows: nil,
	},

	// ---------- Compute ----------
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

	// ---------- Admin ----------
	{
		// Mirrors HostInfo (hostname → name, az, rack, architecture → arch,
		// hypervisor, state → status, last_seen). cpu/ram aren't on the
		// HostInfo proto — they live with the host's runtime stats and
		// would come from a separate RPC ; dropped from this view.
		ID: "hosts", Label: "Hosts", Section: "Admin",
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
