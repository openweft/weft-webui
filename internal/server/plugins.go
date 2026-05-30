// plugins.go — runtime-mutable catalogue of *-as-a-service plugins.
//
// Each plugin contributes one or more resources to the dashboard
// (via the Resource.Plugin field) ; install state gates whether
// those resources appear in the sidebar. Superadmin drives the
// install / enable flow from the Plugins panel.
//
// The mock store ships a curated "marketplace" of candidate plugins,
// none installed by default. Live wiring would replace the in-memory
// map with an etcd-backed catalogue ; the resource-filtering logic
// in api_misc.go stays unchanged.

package server

import "sync"

type Plugin struct {
	ID            string   `json:"id"             doc:"Stable plugin slug, e.g. 'vitess-dbaas'"`
	Name          string   `json:"name"           doc:"Display name"`
	Vendor        string   `json:"vendor"         doc:"Vendor / maintainer (e.g. 'openweft', 'planetscale', 'redis')"`
	Version       string   `json:"version"        doc:"Plugin version (semver)"`
	Description   string   `json:"description"    doc:"One-paragraph description ; shown in the install detail pane"`
	Section       string   `json:"section"        doc:"Sidebar section the plugin contributes to (e.g. 'Database')"`
	Resources     []string `json:"resources"      doc:"Resource IDs this plugin contributes — they appear in /api/resources once installed+enabled"`
	InstallStatus string   `json:"install_status" doc:"'available' | 'installed'" enum:"available,installed"`
	Enabled       bool     `json:"enabled"        doc:"When installed, can be temporarily disabled without uninstalling"`
	InstalledAt   string   `json:"installed_at,omitempty" doc:"RFC-3339 ; set when InstallStatus transitions to installed"`
	InstalledBy   string   `json:"installed_by,omitempty" doc:"OIDC email of the installing admin"`
}

var (
	pluginsMu sync.Mutex
	pluginsByID = seedPlugins()
)

func seedPlugins() map[string]*Plugin {
	return map[string]*Plugin{
		"vitess-dbaas": {
			ID: "vitess-dbaas", Name: "Vitess Database-as-a-Service",
			Vendor: "openweft", Version: "1.0.0",
			Description: "Sharded MySQL via a Vitess cluster. Adds a Database section with keyspace management, shard topology, and HA replica orchestration. Once installed, project admins can provision MySQL-compatible databases through the dashboard.",
			Section:   "Database",
			Resources: []string{"databases"},
			InstallStatus: "available",
			Enabled:       false,
		},
		"valkey-cache": {
			ID: "valkey-cache", Name: "Valkey Cache-as-a-Service",
			Vendor: "Linux Foundation", Version: "8.0.0",
			Description: "Managed Valkey instances (the BSD-licensed Redis fork, post-7.4 license change). Persistence, cluster mode, per-project tenancy. Contributes a 'Cache' section to the sidebar.",
			Section:   "Cache",
			Resources: []string{"valkey-instances"},
			InstallStatus: "available",
			Enabled:       false,
		},
		"kafka-streaming": {
			ID: "kafka-streaming", Name: "Kafka Streaming",
			Vendor: "openweft", Version: "1.2.0",
			Description: "Event streaming via Apache Kafka. Provisions topics, manages consumer groups, and exposes a connector registry. Adds a 'Streaming' section.",
			Section:   "Streaming",
			Resources: []string{"kafka-topics", "kafka-consumer-groups", "kafka-connectors"},
			InstallStatus: "available",
			Enabled:       false,
		},
		"iceberg-lake": {
			ID: "iceberg-lake", Name: "Apache Iceberg Lakehouse",
			Vendor: "openweft", Version: "0.5.0-beta",
			Description: "Iceberg-table data lakehouse over the cluster's object storage. Adds a 'Lakehouse' section with catalogs, namespaces, and table maintenance (compaction, snapshot expiration).",
			Section:   "Lakehouse",
			Resources: []string{"iceberg-catalogs", "iceberg-tables"},
			InstallStatus: "available",
			Enabled:       false,
		},
		"clickhouse-analytics": {
			ID: "clickhouse-analytics", Name: "ClickHouse Analytics",
			Vendor: "clickhouse", Version: "24.10",
			Description: "Column-oriented analytics database. Provisions ClickHouse clusters with replicated MergeTree, exposes a query workbench, and surfaces per-table stats.",
			Section:   "Analytics",
			Resources: []string{"clickhouse-clusters", "clickhouse-tables"},
			InstallStatus: "available",
			Enabled:       false,
		},
		// ---- Storage backends — gate shares + buckets ----
		//
		// Cluster always has block storage (volumes, via local
		// reflink/FICLONE — built-in). Shared filesystems (shares)
		// and object buckets (buckets) are provided by a backend
		// plugin. Pick CubeFS (default, full-featured), Ceph
		// (LGPL alternative), or a object-only Garage for clusters
		// that don't need POSIX. All libre licenses.
		"cubefs-storage": {
			ID: "cubefs-storage", Name: "CubeFS Storage",
			Vendor: "CNCF", Version: "3.4.0",
			Description: "Distributed POSIX filesystem + S3 object storage. Backs both Shares (POSIX RWX) and Buckets (S3) panels. Apache 2.0. The cluster's default — drained when uninstalled.",
			Section:   "Storage",
			Resources: []string{"shares", "buckets"},
			InstallStatus: "installed",
			Enabled:       true,
			InstalledAt:   "2026-04-01T10:00:00Z",
			InstalledBy:   "alice@weft.local",
		},
		"ceph-storage": {
			ID: "ceph-storage", Name: "Ceph Storage",
			Vendor: "Ceph Foundation", Version: "19.2.0",
			Description: "Ceph (LGPL-2.1) as the unified storage layer. RADOSFS for shares, RGW for S3 buckets. Heavier than CubeFS but more mature operationally. Pick this OR cubefs-storage.",
			Section:   "Storage",
			Resources: []string{"shares", "buckets"},
			InstallStatus: "available",
			Enabled:       false,
		},
		"garage-buckets": {
			ID: "garage-buckets", Name: "Garage Object Storage",
			Vendor: "Deuxfleurs", Version: "1.0.1",
			Description: "Garage (AGPL-3) — geo-distributed S3 object storage in Rust. Contributes only Buckets ; pair with a separate POSIX-shares plugin if you need RWX shares. Lightweight, designed for slow consumer-grade hardware.",
			Section:   "Storage",
			Resources: []string{"buckets"},
			InstallStatus: "available",
			Enabled:       false,
		},

		// ---- Registry backends — gate registries ----
		"zot-registry": {
			ID: "zot-registry", Name: "zot Registry",
			Vendor: "Project Zot", Version: "2.1.0",
			Description: "OCI-native distribution (Apache 2.0). Cluster default — the local zot.dc-* instances surface their artifacts here. Lightweight, OCI 1.1 + spec-compliant garbage collection.",
			Section:   "Storage",
			Resources: []string{"registries"},
			InstallStatus: "installed",
			Enabled:       true,
			InstalledAt:   "2026-04-01T10:00:00Z",
			InstalledBy:   "alice@weft.local",
		},
		"harbor-registry": {
			ID: "harbor-registry", Name: "Harbor Registry",
			Vendor: "CNCF", Version: "2.12.0",
			Description: "Harbor (Apache 2.0) — full-featured OCI registry with vulnerability scanning (Trivy), content trust (Notary v2 / Cosign), per-project RBAC, and webhook fan-out. Heavier than zot ; pick when scanning + RBAC are first-class needs.",
			Section:   "Storage",
			Resources: []string{"registries"},
			InstallStatus: "available",
			Enabled:       false,
		},

		// Load-balancing is provided by a plugin too — pick Envoy
		// (high-perf, gRPC + xDS) or Caddy (built-in auto-HTTPS via
		// ACME). The dashboard exposes the Load Balancers section
		// only when one of these two is installed. Seeded as
		// installed+enabled by default so existing seed LB rows
		// stay visible during the migration ; an operator who wants
		// to switch flavours uninstalls one + installs the other.
		"envoy-lb": {
			ID: "envoy-lb", Name: "Envoy Load Balancer",
			Vendor: "openweft", Version: "1.32.0",
			Description: "Layer-4/7 load balancing with Envoy. gRPC + HTTP/3 native, xDS-driven config, per-listener TLS. Ideal for high-throughput east-west traffic.",
			Section:   "Network",
			Resources: []string{"loadbalancers"},
			InstallStatus: "installed",
			Enabled:       true,
			InstalledAt:   "2026-04-01T10:00:00Z",
			InstalledBy:   "alice@weft.local",
		},
		"caddy-lb": {
			ID: "caddy-lb", Name: "Caddy Load Balancer",
			Vendor: "openweft", Version: "2.8.0",
			Description: "Layer-7 load balancing with Caddy. Built-in auto-HTTPS via ACME, HTTP/2 + HTTP/3, ergonomic config. Ideal for public ingress without a separate cert-manager.",
			Section:   "Network",
			Resources: []string{"loadbalancers"},
			InstallStatus: "available",
			Enabled:       false,
		},
	}
}

func listPlugins() []*Plugin {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	out := make([]*Plugin, 0, len(pluginsByID))
	for _, p := range pluginsByID {
		// shallow copy so the caller can't mutate the store via the pointer
		cp := *p
		out = append(out, &cp)
	}
	return out
}

func findPlugin(id string) (*Plugin, bool) {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	p, ok := pluginsByID[id]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

func mutatePlugin(id string, f func(*Plugin)) (*Plugin, bool) {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	p, ok := pluginsByID[id]
	if !ok {
		return nil, false
	}
	f(p)
	cp := *p
	return &cp, true
}

// isResourceGateOpen returns true iff a resource id should be listed
// in /api/resources. A resource is "plugin-gated" when at least one
// plugin declares it in its Resources slice ; in that case the gate
// is open as soon as ANY contributing plugin is installed+enabled
// (lets the operator pick between alternative implementations, e.g.
// envoy-lb vs caddy-lb both contribute "loadbalancers"). A resource
// no plugin contributes to is built-in — always open.
func isResourceGateOpen(resourceID string) bool {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	gated := false
	for _, p := range pluginsByID {
		for _, r := range p.Resources {
			if r != resourceID {
				continue
			}
			gated = true
			if p.InstallStatus == "installed" && p.Enabled {
				return true
			}
		}
	}
	// gated == false → no plugin lists this resource → built-in, open.
	// gated == true  → at least one plugin lists it, but none is
	// currently installed + enabled → closed.
	return !gated
}
