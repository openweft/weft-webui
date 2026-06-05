// shares.go — in-memory store for the dashboard's view of CubeFS
// shares.
//
// weft doesn't have a CreateShare RPC : the CubeFS volume itself is
// provisioned out-of-band on the storage cluster, weft's
// PublishShareToProject just fans the resulting mount out to the
// project's VMs over its event bus. The webui's tenant-admin
// "Create share" affordance therefore records the *intent* — the
// share row appears here, and a future wrapper RPC (or direct CubeFS
// admin call) will turn that into a real volume.
//
// Authorisation is the same model as the rest of the tenant-scoped
// resources : a tenant admin (or cluster admin) can create / delete
// shares whose `project` lies in their tenants. Reads honour the
// session scope.
package server

import (
	"sort"
	"strings"
	"sync"
)

type Share struct {
	// UUID is the opaque handle proto v0.9.0 keys on (GetShare /
	// ResizeShare / DeleteShare all take a UUID). The mock continues
	// to look entries up by name — the field exists so the live-first
	// branch can resolve name → uuid before dialling the wclient.
	UUID     string
	Name     string
	Project  string
	Backend  string // currently always "cubefs"
	SizeGB   int64
	ReadOnly bool
	Mounts   int    // observed mounts (set by weft-network's reconciler later)
	Status   string // "active" | "provisioning" | "failed"
}

type shareStore struct {
	mu     sync.RWMutex
	shares map[string]*Share
}

var sharesDB = func() *shareStore {
	s := &shareStore{shares: make(map[string]*Share)}
	// Seed fixtures matching the rows resources.go used to hold inline.
	for _, sh := range []*Share{
		{Name: "team-data", Project: "team-alpha", Backend: "cubefs", SizeGB: 2048, Mounts: 6, Status: "active"},
		{Name: "notebooks", Project: "research", Backend: "cubefs", SizeGB: 512, Mounts: 9, Status: "active"},
		{Name: "models", Project: "research", Backend: "cubefs", SizeGB: 4096, ReadOnly: true, Mounts: 3, Status: "active"},
	} {
		sh.UUID = mockUUID("share", sh.Project, sh.Name)
		s.shares[sh.Name] = sh
	}
	return s
}()

// list filters by project when projectFilter != "" ; an empty filter
// returns everything (used by cluster admin / tenant-aggregate views).
func (s *shareStore) list(projectFilter string) []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.shares))
	for k := range s.shares {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		sh := s.shares[n]
		if projectFilter != "" && sh.Project != projectFilter {
			continue
		}
		out = append(out, shareToRow(sh))
	}
	return out
}

// listByTenant returns shares whose project belongs to the named
// tenant (per tenantsDB). Used when the session has a tenant but no
// project — the tenant-aggregate "show me everything in this tenant"
// view.
func (s *shareStore) listByTenant(tenant string) []map[string]any {
	projects := tenantsDB.projectsInTenant(tenant)
	if projects == nil {
		return []map[string]any{}
	}
	allowed := map[string]struct{}{}
	for _, p := range projects {
		allowed[p] = struct{}{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.shares))
	for k := range s.shares {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		sh := s.shares[n]
		if _, ok := allowed[sh.Project]; !ok {
			continue
		}
		out = append(out, shareToRow(sh))
	}
	return out
}

func shareToRow(sh *Share) map[string]any {
	return map[string]any{
		"uuid":     sh.UUID,
		"name":     sh.Name,
		"project":  sh.Project,
		"backend":  sh.Backend,
		"size_gb":  sh.SizeGB,
		"readonly": sh.ReadOnly,
		"mounts":   sh.Mounts,
		"status":   sh.Status,
	}
}

func (s *shareStore) create(sh *Share) error {
	sh.Name = strings.TrimSpace(sh.Name)
	if sh.Name == "" {
		return errBadReq("name is required")
	}
	if sh.Project == "" {
		return errBadReq("project is required")
	}
	if sh.SizeGB <= 0 {
		return errBadReq("size_gb must be > 0")
	}
	if sh.Backend == "" {
		sh.Backend = "cubefs"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.shares[sh.Name]; exists {
		return errConflict("share already exists")
	}
	if sh.UUID == "" {
		sh.UUID = mockUUID("share", sh.Project, sh.Name)
	}
	sh.Status = "provisioning" // becomes "active" once CubeFS reports back
	s.shares[sh.Name] = sh
	return nil
}

func (s *shareStore) delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.shares[name]; !ok {
		return errNotFound("share")
	}
	delete(s.shares, name)
	return nil
}

// resize bumps the size of an existing share. The CubeFS side
// owns actual capacity ; this mock store just updates the metadata
// the dashboard reads. ReadOnly is editable too — toggling re-fans
// the share to mounting VMs on the next reconcile (out of scope for
// the mock).
func (s *shareStore) resize(name string, sizeGB int64, readOnly bool) error {
	if sizeGB <= 0 {
		return errBadReq("size_gb must be > 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.shares[name]
	if !ok {
		return errNotFound("share")
	}
	if sizeGB < sh.SizeGB {
		return errBadReq("shrinking is not supported")
	}
	sh.SizeGB = sizeGB
	sh.ReadOnly = readOnly
	return nil
}

func (s *shareStore) shareProject(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sh, ok := s.shares[name]
	if !ok {
		return "", false
	}
	return sh.Project, true
}

// shareUUID resolves a share name to its opaque UUID handle. Used by
// live-first handlers that need the wclient's UUID-keyed RPCs while
// the SPA still addresses shares by name.
func (s *shareStore) shareUUID(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sh, ok := s.shares[name]
	if !ok {
		return "", false
	}
	return sh.UUID, true
}

// (Share lifecycle handlers moved to huma — see api_storage.go.)
