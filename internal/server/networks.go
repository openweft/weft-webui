// networks.go — per-network mutable metadata mock store.
//
// The static network row in resources.go carries name / cidr / type
// / gateway / created (daemon-owned). This file adds the operator-
// editable layer : free-form description, DNS servers, and tags
// — same pattern as volumes.go.
//
// Mock-mode only ; live wiring routes to weft-agent's SetNetworkDNS,
// RenameNetwork (proto), and a future SetNetworkDescription.

package server

import "sync"

type NetworkMetadata struct {
	Description string   `json:"description" doc:"Operator-supplied prose ; surfaced in the dashboard."`
	DNSServers  []string `json:"dns_servers" doc:"DNS resolvers handed to instances on this network. Empty = inherit cluster default."`
	UpdatedAt   string   `json:"updated_at"  doc:"RFC-3339 ; server-stamped" readOnly:"true"`
	UpdatedBy   string   `json:"updated_by"  doc:"OIDC email of the last editor"      readOnly:"true"`
}

var (
	networkMetadataMu   sync.Mutex
	networkMetadataByID = seedNetworkMetadata()
)

func seedNetworkMetadata() map[string]NetworkMetadata {
	now := "2026-05-20T14:00:00Z"
	return map[string]NetworkMetadata{
		"mgmt": {
			Description: "Cluster-management overlay — control-plane RPCs only.",
			DNSServers:  []string{"10.0.0.53"},
			UpdatedAt:   now, UpdatedBy: "alice@weft.local",
		},
		"tenant-net-1": {
			Description: "Default tenant overlay for team-alpha workloads.",
			DNSServers:  []string{"10.10.0.53", "1.1.1.1"},
			UpdatedAt:   now, UpdatedBy: "alice@weft.local",
		},
	}
}

func getNetworkMetadata(key string) NetworkMetadata {
	networkMetadataMu.Lock()
	defer networkMetadataMu.Unlock()
	m := networkMetadataByID[key]
	if m.DNSServers == nil {
		m.DNSServers = []string{}
	}
	return m
}

func setNetworkMetadataStore(key string, m NetworkMetadata) {
	networkMetadataMu.Lock()
	defer networkMetadataMu.Unlock()
	networkMetadataByID[key] = m
}

// renameNetworkRow updates the static row + carries the metadata
// along with it. Mock-only ; same shape as renameVolumeRow.
func renameNetworkRow(oldName, newName string) bool {
	res, ok := resourceByID["networks"]
	if !ok {
		return false
	}
	found := false
	for i, row := range res.Rows {
		if row["name"] == oldName {
			res.Rows[i]["name"] = newName
			found = true
			break
		}
	}
	if !found {
		return false
	}
	networkMetadataMu.Lock()
	defer networkMetadataMu.Unlock()
	if m, ok := networkMetadataByID[oldName]; ok {
		networkMetadataByID[newName] = m
		delete(networkMetadataByID, oldName)
	}
	return true
}
