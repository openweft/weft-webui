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
		ID: "projects", Label: "Projects", Section: "Identity",
		Columns: cols("name", "Name", "tenant", "Tenant", "description", "Description", "status", "Status"),
		Rows: []map[string]any{
			row("name", "team-alpha", "tenant", "acme", "description", "Frontend squad", "status", "active"),
			row("name", "team-beta", "tenant", "acme", "description", "Data platform", "status", "active"),
			row("name", "research", "tenant", "globex", "description", "ML notebooks", "status", "active"),
		},
	},
	{
		ID: "users", Label: "Users", Section: "Identity",
		Columns: cols("username", "Username", "email", "Email", "tenant", "Tenant", "role", "Role", "enabled", "Enabled"),
		Rows: []map[string]any{
			row("username", "yann", "email", "yann@acme.example", "tenant", "acme", "role", "admin", "enabled", true),
			row("username", "alice", "email", "alice@acme.example", "tenant", "acme", "role", "member", "enabled", true),
			row("username", "bob", "email", "bob@globex.example", "tenant", "globex", "role", "member", "enabled", false),
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
		ID: "networks", Label: "Networks", Section: "Network",
		Columns: cols("name", "Name", "cidr", "CIDR", "az", "AZ", "type", "Type", "status", "Status"),
		Rows: []map[string]any{
			row("name", "mgmt", "cidr", "10.0.0.0/16", "az", "DC-A", "type", "wireguard", "status", "active"),
			row("name", "tenant-net-1", "cidr", "10.10.0.0/16", "az", "DC-A", "type", "overlay", "status", "active"),
			row("name", "tenant-net-2", "cidr", "10.20.0.0/16", "az", "DC-B", "type", "overlay", "status", "active"),
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
		ID: "security-groups", Label: "Security Groups", Section: "Network",
		Columns: cols("name", "Name", "description", "Description", "rules", "Rules", "project", "Project"),
		Rows: []map[string]any{
			row("name", "default", "description", "Default deny-in / allow-out", "rules", 2, "project", "team-alpha"),
			row("name", "web", "description", "HTTP/HTTPS ingress", "rules", 3, "project", "team-alpha"),
			row("name", "db", "description", "Postgres from web only", "rules", 1, "project", "team-beta"),
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
		ID: "volumes", Label: "Volumes", Section: "Storage",
		Columns: cols("name", "Name", "size_gb", "Size (GB)", "backing", "Backing", "attached_to", "Attached to", "status", "Status"),
		Rows: []map[string]any{
			row("name", "pg-data", "size_gb", 200, "backing", "block (passthrough)", "attached_to", "db-1", "status", "in-use"),
			row("name", "cubefs-d0", "size_gb", 500, "backing", "file", "attached_to", "cubefs-data-0", "status", "in-use"),
			row("name", "scratch-1", "size_gb", 50, "backing", "file", "attached_to", "", "status", "available"),
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
		ID: "microvms", Label: "microVMs", Section: "Compute",
		Columns: cols("name", "Name", "image", "Image", "flavor", "Flavor", "host", "Host", "network", "Network", "project", "Project", "status", "Status"),
		Rows: []map[string]any{
			row("name", "web-1", "image", "alpine:3.21", "flavor", "small", "host", "dc-a-r1-h2", "network", "tenant-net-1", "project", "team-alpha", "status", "running"),
			row("name", "nb-1", "image", "jupyter:latest", "flavor", "small", "host", "dc-c-r2-h1", "network", "tenant-net-2", "project", "research", "status", "running"),
			row("name", "ci-job-7f3", "image", "buildkit:latest", "flavor", "medium", "host", "dc-b-r1-h3", "network", "tenant-net-1", "project", "team-beta", "status", "running"),
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
		ID: "hosts", Label: "Hosts", Section: "Admin",
		Columns: cols("name", "Name", "az", "AZ", "rack", "Rack", "arch", "Arch", "cpu", "CPU", "ram_gb", "RAM (GB)", "microvms", "microVMs", "status", "Status"),
		Rows: []map[string]any{
			row("name", "dc-a-r1-h2", "az", "DC-A", "rack", "R1", "arch", "arm64", "cpu", 64, "ram_gb", 512, "microvms", 18, "status", "up"),
			row("name", "dc-b-r1-h3", "az", "DC-B", "rack", "R1", "arch", "amd64", "cpu", 64, "ram_gb", 512, "microvms", 22, "status", "up"),
			row("name", "dc-c-r2-h1", "az", "DC-C", "rack", "R2", "arch", "arm64", "cpu", 96, "ram_gb", 768, "microvms", 11, "status", "draining"),
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
