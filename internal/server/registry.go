package server

import (
	"fmt"
	"strings"
	"sync"
)

// OCI registry artifacts. Operator-uploaded blobs are stored here ;
// the dashboard's upload flow appends via registryAdd. The list is
// served read-only via registryList. The mock seed has been removed
// for production use — the dashboard shows an empty table until
// operators push real artifacts. The full migration to a real OCI
// registry backend (zot, weft-network reconciler) replaces this
// in-memory store with oras/containerd calls — the HTTP shapes stay
// the same.
//
// "artifact" rather than "image" : container images, raw multi-arch
// disks, Helm charts, model weights — all OCI-wrapped blobs.
var (
	registryMu        sync.Mutex
	registryArtifacts = []map[string]any{}
)

func registryList() []map[string]any {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]map[string]any, len(registryArtifacts))
	copy(out, registryArtifacts)
	return out
}

func registryCount() int {
	registryMu.Lock()
	defer registryMu.Unlock()
	return len(registryArtifacts)
}

// registryAdd prepends so the newest upload shows first.
func registryAdd(r map[string]any) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registryArtifacts = append([]map[string]any{r}, registryArtifacts...)
}

// (handleRegistryUpload moved to huma — see api_misc.go. The badUpload
// helper is gone too ; huma errors replace the ad-hoc 400 envelope.)

// ---- Remote-registry catalogue (proxy / replication) ----------------
//
// A "remote" is another OCI registry this cluster federates with —
// either as a pull-through proxy (cache the upstream, serve from
// local) or as a replication target (push everything we have so the
// remote stays in sync). Mock store ; live wiring would back this
// with zot's sync config or a separate replication daemon.

type RegistryRemote struct {
	Name      string `json:"name"        doc:"Stable identifier (slug)" minLength:"1" maxLength:"64"`
	URL       string `json:"url"         doc:"OCI registry endpoint (https://…)" minLength:"1" maxLength:"512"`
	Kind      string `json:"kind"        doc:"'proxy' (pull-through cache) or 'replica' (push mirror)" enum:"proxy,replica"`
	Enabled   bool   `json:"enabled"     doc:"Sync paused when false"`
	Username  string `json:"username,omitempty"   doc:"Basic-auth user (optional)"`
	LastSync  string `json:"last_sync,omitempty"  doc:"RFC-3339 timestamp of the last successful sync"`
	UpdatedAt string `json:"updated_at"           doc:"RFC-3339"`
	UpdatedBy string `json:"updated_by,omitempty" doc:"Email of the last editor"`
}

var (
	registryRemotesMu sync.Mutex
	registryRemotes   = []RegistryRemote{
		{
			Name: "docker-hub", URL: "https://registry-1.docker.io",
			Kind: "proxy", Enabled: true, LastSync: "2026-05-30T06:15:00Z",
			UpdatedAt: "2026-05-12T10:21:00Z", UpdatedBy: "alice@weft.local",
		},
		{
			Name: "ghcr-public", URL: "https://ghcr.io",
			Kind: "proxy", Enabled: true, LastSync: "2026-05-30T05:42:00Z",
			UpdatedAt: "2026-04-28T14:05:00Z", UpdatedBy: "alice@weft.local",
		},
		{
			Name: "dc-b-mirror", URL: "https://zot.dc-b.weft.local",
			Kind: "replica", Enabled: false, LastSync: "2026-05-29T23:00:00Z",
			Username: "replication", UpdatedAt: "2026-05-20T09:11:00Z", UpdatedBy: "bob@weft.local",
		},
	}
)

func registryRemotesList() []RegistryRemote {
	registryRemotesMu.Lock()
	defer registryRemotesMu.Unlock()
	out := make([]RegistryRemote, len(registryRemotes))
	copy(out, registryRemotes)
	return out
}

func registryRemoteFind(name string) (RegistryRemote, bool) {
	registryRemotesMu.Lock()
	defer registryRemotesMu.Unlock()
	for _, r := range registryRemotes {
		if r.Name == name {
			return r, true
		}
	}
	return RegistryRemote{}, false
}

// registryRemoteUpsert insert-or-replaces by Name. The caller stamps
// UpdatedAt + UpdatedBy.
func registryRemoteUpsert(r RegistryRemote) {
	registryRemotesMu.Lock()
	defer registryRemotesMu.Unlock()
	for i, existing := range registryRemotes {
		if existing.Name == r.Name {
			// Preserve LastSync — the sync engine owns that field.
			r.LastSync = existing.LastSync
			registryRemotes[i] = r
			return
		}
	}
	registryRemotes = append(registryRemotes, r)
}

func registryRemoteDelete(name string) bool {
	registryRemotesMu.Lock()
	defer registryRemotesMu.Unlock()
	for i, r := range registryRemotes {
		if r.Name == name {
			registryRemotes = append(registryRemotes[:i], registryRemotes[i+1:]...)
			return true
		}
	}
	return false
}

// ---- Remote catalog mock (search target) ------------------------
//
// Static per-remote corpus the mock search filters against. A real
// proxy would proxy to the upstream catalog API ; this lets the
// dashboard demo the search affordance against a representative set
// without network calls.

type remoteSearchEntry struct {
	repo, tag, kind, arches, size, pushed string
}

var remoteCatalogs = map[string][]remoteSearchEntry{
	"docker-hub": {
		{"library/alpine", "3.21", "container", "amd64, arm64, riscv64", "7.8 MiB", "2d ago"},
		{"library/alpine", "3.20", "container", "amd64, arm64", "7.6 MiB", "1w ago"},
		{"library/debian", "12-slim", "container", "amd64, arm64", "75 MiB", "5d ago"},
		{"library/debian", "trixie", "container", "amd64, arm64", "80 MiB", "1d ago"},
		{"library/ubuntu", "24.04", "container", "amd64, arm64", "78 MiB", "3d ago"},
		{"library/ubuntu", "22.04", "container", "amd64, arm64", "78 MiB", "2w ago"},
		{"library/postgres", "16", "container", "amd64, arm64", "440 MiB", "4d ago"},
		{"library/postgres", "17", "container", "amd64, arm64", "445 MiB", "6h ago"},
		{"library/redis", "7", "container", "amd64, arm64", "120 MiB", "1d ago"},
		{"library/nginx", "1.27", "container", "amd64, arm64", "190 MiB", "3d ago"},
		{"library/nginx", "alpine", "container", "amd64, arm64", "40 MiB", "3d ago"},
		{"library/python", "3.13", "container", "amd64, arm64", "1.1 GiB", "5d ago"},
		{"library/golang", "1.23", "container", "amd64, arm64", "850 MiB", "2d ago"},
		{"library/node", "22-alpine", "container", "amd64, arm64", "120 MiB", "3d ago"},
		{"library/busybox", "latest", "container", "amd64, arm64, ppc64le, s390x", "5 MiB", "1w ago"},
	},
	"ghcr-public": {
		{"openweft/weft", "v0.5.0", "container", "amd64, arm64", "62 MiB", "3d ago"},
		{"openweft/weft-microvm-agent", "v0.5.0", "container", "amd64, arm64", "48 MiB", "3d ago"},
		{"dexidp/dex", "v2.40.0", "container", "amd64, arm64", "92 MiB", "1w ago"},
		{"project-zot/zot-linux-amd64", "v2.1.0", "container", "amd64", "55 MiB", "4d ago"},
		{"prometheus-operator/prometheus", "v2.55.0", "container", "amd64, arm64", "210 MiB", "1d ago"},
		{"grafana/grafana", "11.3.0", "container", "amd64, arm64", "330 MiB", "2d ago"},
		{"cilium/cilium", "v1.16.4", "container", "amd64, arm64", "180 MiB", "1d ago"},
		{"etcd-io/etcd", "v3.5.16", "container", "amd64, arm64", "98 MiB", "1w ago"},
		{"nats-io/nats-server", "2.10.21", "container", "amd64, arm64", "32 MiB", "5d ago"},
	},
	"dc-b-mirror": {
		// Replicas mirror the local cluster's artifacts ; for the
		// mock we surface a subset matching what's in zot.dc-a so
		// the operator sees realistic replication content.
		{"library/alpine", "3.21", "container", "amd64, arm64", "7.8 MiB", "3d ago"},
		{"team-alpha/web", "v1.4.2", "container", "amd64, arm64", "52 MiB", "5h ago"},
		{"weft/cloud-boot", "uefi", "raw", "amd64, arm64, riscv64, loongarch64", "18 MiB", "1d ago"},
	},
}

// searchRemoteCatalog returns entries whose repository contains the
// query (case-insensitive). Empty query returns the first 12 entries
// as a "featured" subset.
func searchRemoteCatalog(remoteName, q string) []RemoteSearchHit {
	corpus := remoteCatalogs[remoteName]
	q = strings.ToLower(strings.TrimSpace(q))
	out := make([]RemoteSearchHit, 0, len(corpus))
	for _, e := range corpus {
		if q != "" && !strings.Contains(strings.ToLower(e.repo), q) {
			continue
		}
		out = append(out, RemoteSearchHit{
			Repository: e.repo, Tag: e.tag, Type: e.kind,
			Arches: e.arches, Size: e.size, Pushed: e.pushed,
		})
		if q == "" && len(out) >= 12 {
			break
		}
	}
	return out
}

func humanSize(n int64) string {
	if n <= 0 {
		return "—"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
