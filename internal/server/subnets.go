// subnets.go — per-network subnet sub-resource (mock store).
//
// A subnet carves a CIDR out of the parent network, names a gateway,
// and toggles whether the data plane should hand out leases. Live
// wiring would route to weft-network's CreateSubnet / DeleteSubnet
// once exposed ; this file backs the dashboard's NetworkDrawer
// Subnets tab through staged rollouts.

package server

import "sync"

type Subnet struct {
	UUID      string `json:"uuid"       doc:"Server-stamped" readOnly:"true"`
	Name      string `json:"name"       doc:"Operator-chosen handle, unique within the network" minLength:"1" maxLength:"128"`
	CIDR      string `json:"cidr"       doc:"e.g. 10.10.0.0/24"            minLength:"1" maxLength:"64"`
	Gateway   string `json:"gateway"    doc:"Gateway IP (optional ; first usable host if empty)"`
	Enabled   bool   `json:"enabled,omitempty" doc:"Disabled subnets stay in the catalogue but the DHCP pool is parked. Missing = enabled."`
	UpdatedAt string `json:"updated_at" doc:"RFC-3339 ; server-stamped" readOnly:"true"`
	UpdatedBy string `json:"updated_by" doc:"OIDC email of the last editor"      readOnly:"true"`
}

var (
	subnetsMu sync.Mutex
	// keyed by network name (the row primary key on the mock side).
	subnetsByNetwork = seedSubnets()
)

func seedSubnets() map[string][]Subnet {
	now := "2026-05-20T14:00:00Z"
	return map[string][]Subnet{
		"mgmt": {
			{UUID: "subnet-mgmt-0", Name: "control-plane",
				CIDR: "10.0.0.0/24", Gateway: "10.0.0.1", Enabled: true,
				UpdatedAt: now, UpdatedBy: "alice@weft.local"},
		},
		"tenant-net-1": {
			{UUID: "subnet-tn1-0", Name: "web-tier",
				CIDR: "10.10.0.0/24", Gateway: "10.10.0.1", Enabled: true,
				UpdatedAt: now, UpdatedBy: "alice@weft.local"},
			{UUID: "subnet-tn1-1", Name: "db-tier",
				CIDR: "10.10.1.0/24", Gateway: "10.10.1.1", Enabled: true,
				UpdatedAt: now, UpdatedBy: "alice@weft.local"},
		},
	}
}

func listSubnets(networkKey string) []Subnet {
	subnetsMu.Lock()
	defer subnetsMu.Unlock()
	out := make([]Subnet, len(subnetsByNetwork[networkKey]))
	copy(out, subnetsByNetwork[networkKey])
	return out
}

// upsertSubnet inserts or updates by uuid (or name if uuid is empty).
// Returns the saved subnet + whether it was an update.
func upsertSubnet(networkKey string, s Subnet) (Subnet, bool) {
	subnetsMu.Lock()
	defer subnetsMu.Unlock()
	list := subnetsByNetwork[networkKey]
	for i, existing := range list {
		if (s.UUID != "" && existing.UUID == s.UUID) ||
			(s.UUID == "" && existing.Name == s.Name) {
			// Preserve uuid on update.
			s.UUID = existing.UUID
			list[i] = s
			subnetsByNetwork[networkKey] = list
			return s, true
		}
	}
	if s.UUID == "" {
		s.UUID = mockUUID("subnet", networkKey, s.Name)
	}
	subnetsByNetwork[networkKey] = append(list, s)
	return s, false
}

func deleteSubnet(networkKey, uuid string) bool {
	subnetsMu.Lock()
	defer subnetsMu.Unlock()
	list := subnetsByNetwork[networkKey]
	for i, s := range list {
		if s.UUID == uuid {
			subnetsByNetwork[networkKey] = append(list[:i], list[i+1:]...)
			return true
		}
	}
	return false
}
