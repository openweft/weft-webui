// editable_metadata.go — shared mock-friendly metadata store for
// resources whose drawer needs at least { description + rename }.
//
// Used by routers, floating-ips, scheduling-rules. Volumes + networks
// have their own typed stores because they carry extra fields beyond
// the common pair (volumes: mount_point/filesystem/properties ;
// networks: dns_servers).
//
// Live wiring routes the metadata PUT through the matching
// weft-network RPCs once they exist ; the mock layer preserves the
// affordance through staged rollouts.

package server

import "sync"

// EditableMetadata is the common shape every resource gets a free
// description on, server-stamped UpdatedAt/By.
type EditableMetadata struct {
	Description string `json:"description" doc:"Operator-supplied prose ; surfaced in the dashboard."`
	UpdatedAt   string `json:"updated_at"  doc:"RFC-3339 ; server-stamped" readOnly:"true"`
	UpdatedBy   string `json:"updated_by"  doc:"OIDC email of the last editor"      readOnly:"true"`
}

type metadataStore struct {
	mu   sync.Mutex
	byID map[string]EditableMetadata
}

func newMetadataStore() *metadataStore {
	return &metadataStore{byID: map[string]EditableMetadata{}}
}

func (s *metadataStore) get(key string) EditableMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byID[key]
}

func (s *metadataStore) set(key string, m EditableMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[key] = m
}

// rename moves the metadata entry from old key to new key. No-op if
// the entry doesn't exist at the old key.
func (s *metadataStore) rename(oldKey, newKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.byID[oldKey]; ok {
		s.byID[newKey] = m
		delete(s.byID, oldKey)
	}
}

// renameResourceRow updates the static seed row in resources.go's
// catalogue. Looks up by uuid OR name (some resources only have name).
// Returns false if no row matches.
func renameResourceRow(resID, lookupKey, newName string) bool {
	res, ok := resourceByID[resID]
	if !ok {
		return false
	}
	for i, row := range res.Rows {
		if str(row["uuid"]) == lookupKey || str(row["name"]) == lookupKey {
			res.Rows[i]["name"] = newName
			return true
		}
	}
	return false
}

// Per-resource stores. Seeded empty ; rows get a metadata entry on
// first edit. The seed remains in resources.go's row map (description
// column when present, otherwise blank).
var (
	routerMetadata        = newMetadataStore()
	floatingIPMetadata    = newMetadataStore()
	schedulingRuleMetadata = newMetadataStore()
)
