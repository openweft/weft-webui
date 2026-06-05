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

// ---- LoadBalancer row helpers ----------------------------------
//
// The Load Balancers resource (resourceByID["loadbalancers"]) is the
// catalogue the dashboard tree + map polls ; the v0.8.0 live wiring
// keeps it in sync by mirroring every successful create / update /
// delete here. Mock-mode (no live, or live returns Unimplemented)
// mutates the same store so the affordance survives staged rollouts.

var lbStoreMu sync.Mutex

func appendLoadBalancerRow(row map[string]any) {
	lbStoreMu.Lock()
	defer lbStoreMu.Unlock()
	if lb, ok := resourceByID["loadbalancers"]; ok {
		lb.Rows = append(lb.Rows, row)
	}
}

func updateLoadBalancerRow(uuid string, patch func(map[string]any)) bool {
	lbStoreMu.Lock()
	defer lbStoreMu.Unlock()
	lb, ok := resourceByID["loadbalancers"]
	if !ok {
		return false
	}
	for _, row := range lb.Rows {
		if str(row["uuid"]) == uuid {
			patch(row)
			return true
		}
	}
	return false
}

func deleteLoadBalancerRow(uuid string) bool {
	lbStoreMu.Lock()
	defer lbStoreMu.Unlock()
	lb, ok := resourceByID["loadbalancers"]
	if !ok {
		return false
	}
	for i, row := range lb.Rows {
		if str(row["uuid"]) == uuid {
			lb.Rows = append(lb.Rows[:i], lb.Rows[i+1:]...)
			return true
		}
	}
	return false
}

func findLoadBalancerRow(uuid string) (map[string]any, bool) {
	lbStoreMu.Lock()
	defer lbStoreMu.Unlock()
	lb, ok := resourceByID["loadbalancers"]
	if !ok {
		return nil, false
	}
	for _, row := range lb.Rows {
		if str(row["uuid"]) == uuid {
			return row, true
		}
	}
	return nil, false
}
