// tenants.go — in-memory model for tenants + their projects, members
// and per-project role assignments.
//
// This is the source-of-truth for the mock identity layer. When weft-agent
// grows the matching RPCs (CreateTenant, AddTenantMember, GrantRole, …)
// the same shapes will go on the wire ; until then the dashboard
// mutates this store and the rows for Tenants / Projects / Users /
// Groups are derived from it on each call.
//
// Authorization model (mirrored client-side for affordance gating, but
// enforced here for the API) :
//
//   - "cluster admin"  — group "admin" in the user's claims. Can create
//                        tenants and add users to a tenant's admin group.
//   - "tenant admin"   — listed in the tenant's `admins` set. Can add
//                        projects, add members, grant roles within
//                        THAT tenant. Delegated administration.
//   - regular member   — read-only on their tenants.
//
// Concurrency : one mutex guards the whole store. Read paths take a
// snapshot under the lock and release it before serialising, so the
// hot path stays O(N) without holding the lock during JSON encoding.
package server

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/openweft/weft-webui/internal/auth"
)

// Quotas carries every limit we currently track. The same struct
// describes a tenant cap (superadmin sets it) and a project cap
// (tenant admin sets it, must fit within the tenant's remaining).
// Zero means unlimited at the tenant level and 0-allowed at the
// project level — keep that in mind when summing.
//
// Counts (no Unit) :     vcpu, volumes, shares, buckets,
//                        floating_ips, projects (tenant-only)
// Capacities (GiB) :     ram_gib, volumes_gib, shares_gib,
//                        buckets_gib, registry_gib
type Quotas struct {
	VCPU        int `json:"vcpu"`
	RAMGiB      int `json:"ram_gib"`
	Volumes     int `json:"volumes"`
	VolumesGiB  int `json:"volumes_gib"`
	Shares      int `json:"shares"`
	SharesGiB   int `json:"shares_gib"`
	Buckets     int `json:"buckets"`
	BucketsGiB  int `json:"buckets_gib"`
	RegistryGiB int `json:"registry_gib"`
	FloatingIPs int `json:"floating_ips"`
	// Projects is only meaningful at the tenant level — caps how many
	// projects a tenant admin can carve out. Ignored on projectInfo.
	Projects int `json:"projects"`
}

// add returns the field-wise sum. Used to compute "what the projects
// of a tenant currently consume" before validating a new cap.
func (q Quotas) add(o Quotas) Quotas {
	return Quotas{
		VCPU: q.VCPU + o.VCPU, RAMGiB: q.RAMGiB + o.RAMGiB,
		Volumes: q.Volumes + o.Volumes, VolumesGiB: q.VolumesGiB + o.VolumesGiB,
		Shares: q.Shares + o.Shares, SharesGiB: q.SharesGiB + o.SharesGiB,
		Buckets: q.Buckets + o.Buckets, BucketsGiB: q.BucketsGiB + o.BucketsGiB,
		RegistryGiB: q.RegistryGiB + o.RegistryGiB,
		FloatingIPs: q.FloatingIPs + o.FloatingIPs,
		Projects:    q.Projects + o.Projects,
	}
}

// fits reports whether candidate ≤ cap on every dimension. Used in two
// places : project quota PUT ↔ (sum-of-other-projects + new) ↔ tenant
// cap. Returns the offending dimension name on failure.
func (q Quotas) fits(cap Quotas) (string, bool) {
	checks := []struct {
		name string
		got  int
		max  int
	}{
		{"vcpu", q.VCPU, cap.VCPU},
		{"ram_gib", q.RAMGiB, cap.RAMGiB},
		{"volumes", q.Volumes, cap.Volumes},
		{"volumes_gib", q.VolumesGiB, cap.VolumesGiB},
		{"shares", q.Shares, cap.Shares},
		{"shares_gib", q.SharesGiB, cap.SharesGiB},
		{"buckets", q.Buckets, cap.Buckets},
		{"buckets_gib", q.BucketsGiB, cap.BucketsGiB},
		{"registry_gib", q.RegistryGiB, cap.RegistryGiB},
		{"floating_ips", q.FloatingIPs, cap.FloatingIPs},
	}
	for _, c := range checks {
		if c.got > c.max {
			return c.name, false
		}
	}
	return "", true
}

// Tenant is one identity boundary. Projects live in exactly one tenant,
// users can be members of several with different group sets per tenant.
type Tenant struct {
	Name    string
	Domain  string
	Status  string
	Admins  map[string]struct{} // user emails in the tenant-admin group
	Members map[string][]string // email → groups within this tenant
	// Projects in this tenant. Order is preserved (creation order).
	Projects []string
	// Groups defined within this tenant. Beyond "admins" each tenant
	// can carve its own (developers, viewers, …). "admins" is implicit.
	Groups map[string]string // group name → description
	// Quotas is the cluster-admin-set hard cap. Sum of all project
	// Quotas + the tenant's direct consumption must stay ≤ this.
	Quotas Quotas
}

// projectInfo is the per-project record kept alongside tenants for
// quick lookup and to carry role assignments (user→role).
type projectInfo struct {
	Name    string
	UUID    string
	Created string
	Tenant  string
	Roles   map[string]string // email → role ("owner" | "editor" | "viewer")
	Quotas  Quotas            // distributed share of the tenant cap
}

type tenantStore struct {
	mu       sync.RWMutex
	tenants  map[string]*Tenant       // name → tenant
	projects map[string]*projectInfo  // name → project
	users    map[string]*userIdentity // email → user
}

type userIdentity struct {
	Email       string
	Name        string
	Issuer      string
	LastSeen    string
	// Memberships are derived from tenants[*].Members on every read so
	// the source of truth stays the tenant struct.
}

// tenantsDB is the package-global store. Seeded in init() with the
// same fixtures the registry used to hold inline.
var tenantsDB *tenantStore

func init() {
	tenantsDB = newTenantStore()
	tenantsDB.seed()
}

func newTenantStore() *tenantStore {
	return &tenantStore{
		tenants:  make(map[string]*Tenant),
		projects: make(map[string]*projectInfo),
		users:    make(map[string]*userIdentity),
	}
}

// defaultTenantQuota is the cap a freshly created tenant inherits —
// generous enough to be useful in dev, finite enough to exercise the
// hard-cap path. Cluster admins reset it via PUT /api/tenants/{n}/quota.
var defaultTenantQuota = Quotas{
	VCPU: 128, RAMGiB: 1024,
	Volumes: 64, VolumesGiB: 4096,
	Shares: 32, SharesGiB: 8192,
	Buckets: 32, BucketsGiB: 4096,
	RegistryGiB: 512,
	FloatingIPs: 16,
	Projects:    16,
}

func (s *tenantStore) seed() {
	// Tenants.
	for _, t := range []*Tenant{
		{Name: "acme", Domain: "acme.example", Status: "active",
			Quotas: Quotas{VCPU: 96, RAMGiB: 512, Volumes: 32, VolumesGiB: 2048,
				Shares: 16, SharesGiB: 4096, Buckets: 24, BucketsGiB: 2048,
				RegistryGiB: 256, FloatingIPs: 8, Projects: 10}},
		{Name: "globex", Domain: "globex.example", Status: "active",
			Quotas: Quotas{VCPU: 32, RAMGiB: 192, Volumes: 12, VolumesGiB: 512,
				Shares: 8, SharesGiB: 1024, Buckets: 8, BucketsGiB: 512,
				RegistryGiB: 64, FloatingIPs: 4, Projects: 5}},
		{Name: "initech", Domain: "initech.example", Status: "disabled",
			Quotas: defaultTenantQuota},
	} {
		t.Admins = map[string]struct{}{}
		t.Members = map[string][]string{}
		t.Groups = map[string]string{
			"admins":     "Tenant operators",
			"developers": "Read/write on tenant projects",
			"viewers":    "Read-only",
		}
		s.tenants[t.Name] = t
	}

	// Users.
	users := []*userIdentity{
		{Email: "yann@acme.example", Name: "Yannick", Issuer: "dex", LastSeen: "2026-05-27"},
		{Email: "alice@acme.example", Name: "Alice", Issuer: "dex", LastSeen: "2026-05-26"},
		{Email: "bob@globex.example", Name: "Bob", Issuer: "dex", LastSeen: "2026-05-12"},
		{Email: "dev@weft.local", Name: "dev", Issuer: "local", LastSeen: "2026-05-28"},
	}
	for _, u := range users {
		s.users[u.Email] = u
	}

	// Memberships : Yannick admins acme + developers globex ;
	// Alice developers in acme ; Bob viewers in globex ;
	// the dev user admins every tenant so the dev UI exercises every action.
	s.tenants["acme"].Members["yann@acme.example"] = []string{"admins", "developers"}
	s.tenants["acme"].Admins["yann@acme.example"] = struct{}{}
	s.tenants["acme"].Members["alice@acme.example"] = []string{"developers"}
	s.tenants["globex"].Members["yann@acme.example"] = []string{"developers"}
	s.tenants["globex"].Members["bob@globex.example"] = []string{"viewers"}
	for name := range s.tenants {
		s.tenants[name].Members["dev@weft.local"] = []string{"admins"}
		s.tenants[name].Admins["dev@weft.local"] = struct{}{}
	}

	// Projects.
	projects := []*projectInfo{
		{Name: "team-alpha", UUID: "1c5d8a9e-7c11-4d2a-9c5e-aab742c0a112", Created: "2026-04-12", Tenant: "acme",
			Quotas: Quotas{VCPU: 32, RAMGiB: 192, Volumes: 8, VolumesGiB: 512,
				Shares: 4, SharesGiB: 1024, Buckets: 4, BucketsGiB: 256,
				RegistryGiB: 64, FloatingIPs: 2}},
		{Name: "team-beta", UUID: "2d3e9b7c-8e22-4ab3-9b0e-bbe853d1b223", Created: "2026-04-18", Tenant: "acme",
			Quotas: Quotas{VCPU: 16, RAMGiB: 96, Volumes: 4, VolumesGiB: 256,
				Shares: 2, SharesGiB: 512, Buckets: 2, BucketsGiB: 128,
				RegistryGiB: 32, FloatingIPs: 1}},
		{Name: "research", UUID: "3f6abcd2-9f33-4ec4-8a1f-ccf964e2c334", Created: "2026-05-03", Tenant: "globex",
			Quotas: Quotas{VCPU: 16, RAMGiB: 96, Volumes: 6, VolumesGiB: 256,
				Shares: 4, SharesGiB: 512, Buckets: 4, BucketsGiB: 256,
				RegistryGiB: 32, FloatingIPs: 2}},
	}
	for _, p := range projects {
		p.Roles = map[string]string{}
		s.projects[p.Name] = p
		s.tenants[p.Tenant].Projects = append(s.tenants[p.Tenant].Projects, p.Name)
	}
	s.projects["team-alpha"].Roles["yann@acme.example"] = "owner"
	s.projects["team-alpha"].Roles["alice@acme.example"] = "editor"
}

// ---- Read-only views (used by the resource rows handlers) ----

// listTenants returns rows for the tenants resource. When forEmail is
// non-empty the result is filtered to the tenants that user is a member
// of — the user listener uses this so a user never sees tenants they
// don't belong to.
func (s *tenantStore) listTenants(forEmail string) []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.tenants))
	for _, t := range sortedTenants(s.tenants) {
		if forEmail != "" {
			if _, ok := t.Members[forEmail]; !ok {
				continue
			}
		}
		out = append(out, map[string]any{
			"name":     t.Name,
			"domain":   t.Domain,
			"projects": len(t.Projects),
			"members":  len(t.Members),
			"admins":   len(t.Admins),
			"status":   t.Status,
		})
	}
	return out
}

// listProjects mirrors the registry shape (name/uuid/created) and adds
// the tenant column.
//
//   - forEmail != ""  → filter to projects whose tenant the user
//                       belongs to (user-UI view of "their" projects).
//   - tenantFilter != "" → only projects of that tenant (cascading
//                       topbar selection). Combines with forEmail.
func (s *tenantStore) listProjects(forEmail, tenantFilter string) []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.projects))
	for _, p := range sortedProjects(s.projects) {
		if tenantFilter != "" && p.Tenant != tenantFilter {
			continue
		}
		if forEmail != "" {
			t := s.tenants[p.Tenant]
			if t == nil {
				continue
			}
			if _, ok := t.Members[forEmail]; !ok {
				continue
			}
		}
		out = append(out, map[string]any{
			"name":    p.Name,
			"tenant":  p.Tenant,
			"uuid":    p.UUID,
			"created": p.Created,
		})
	}
	return out
}

// projectsInTenant returns the project names that belong to `tenant`.
// Returns nil for an unknown tenant.
func (s *tenantStore) projectsInTenant(tenant string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[tenant]
	if !ok {
		return nil
	}
	out := make([]string, len(t.Projects))
	copy(out, t.Projects)
	return out
}

// listUsers returns the cluster-wide user table. memberships is
// rendered as "tenant:group1,group2 / tenant2:..." so the existing
// table layout stays compact.
func (s *tenantStore) listUsers() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.users))
	for _, u := range sortedUsers(s.users) {
		out = append(out, map[string]any{
			"name":        u.Name,
			"email":       u.Email,
			"issuer":      u.Issuer,
			"memberships": s.formatMemberships(u.Email),
			"last_seen":   u.LastSeen,
		})
	}
	return out
}

// formatMemberships renders the per-tenant group list. Called under
// the read lock by the caller.
func (s *tenantStore) formatMemberships(email string) string {
	var parts []string
	for _, t := range sortedTenants(s.tenants) {
		if g, ok := t.Members[email]; ok {
			parts = append(parts, t.Name+":"+strings.Join(g, ","))
		}
	}
	return strings.Join(parts, " / ")
}

// listGroups : groups are defined per-tenant. We surface them as
// (tenant, group) pairs so the cluster-admin view shows the full set.
func (s *tenantStore) listGroups() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0)
	for _, t := range sortedTenants(s.tenants) {
		// Count members per group.
		counts := map[string]int{}
		for _, groups := range t.Members {
			for _, g := range groups {
				counts[g]++
			}
		}
		for _, name := range sortedKeys(t.Groups) {
			out = append(out, map[string]any{
				"name":        name,
				"tenant":      t.Name,
				"description": t.Groups[name],
				"members":     counts[name],
			})
		}
	}
	return out
}

// tenantDetail is the structure returned by GET /api/tenants/{name} —
// everything the tenant-detail UI needs to render in one call.
func (s *tenantStore) tenantDetail(name string) (map[string]any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[name]
	if !ok {
		return nil, false
	}
	type member struct {
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
		Admin  bool     `json:"admin"`
	}
	type project struct {
		Name    string            `json:"name"`
		UUID    string            `json:"uuid"`
		Created string            `json:"created"`
		Roles   map[string]string `json:"roles"`
	}
	members := make([]member, 0, len(t.Members))
	for _, email := range sortedKeys(t.Members) {
		_, isAdmin := t.Admins[email]
		nm := email
		if u, ok := s.users[email]; ok && u.Name != "" {
			nm = u.Name
		}
		members = append(members, member{
			Email: email, Name: nm,
			Groups: t.Members[email], Admin: isAdmin,
		})
	}
	projs := make([]project, 0, len(t.Projects))
	for _, pn := range t.Projects {
		p := s.projects[pn]
		if p == nil {
			continue
		}
		// Copy roles so callers can't mutate the store.
		r := make(map[string]string, len(p.Roles))
		for k, v := range p.Roles {
			r[k] = v
		}
		projs = append(projs, project{Name: p.Name, UUID: p.UUID, Created: p.Created, Roles: r})
	}
	groups := make([]map[string]any, 0, len(t.Groups))
	for _, g := range sortedKeys(t.Groups) {
		groups = append(groups, map[string]any{"name": g, "description": t.Groups[g]})
	}
	return map[string]any{
		"name":     t.Name,
		"domain":   t.Domain,
		"status":   t.Status,
		"projects": projs,
		"members":  members,
		"groups":   groups,
	}, true
}

// ---- Mutations ----

func (s *tenantStore) createTenant(name, domain string) error {
	name, domain = strings.TrimSpace(name), strings.TrimSpace(domain)
	if name == "" {
		return errBadReq("name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tenants[name]; exists {
		return errConflict("tenant already exists")
	}
	s.tenants[name] = &Tenant{
		Name: name, Domain: domain, Status: "active",
		Admins:  map[string]struct{}{},
		Members: map[string][]string{},
		Groups: map[string]string{
			"admins":     "Tenant operators",
			"developers": "Read/write on tenant projects",
			"viewers":    "Read-only",
		},
		Quotas: defaultTenantQuota,
	}
	return nil
}

// addTenantAdmin promotes a user (creating a stub user record if
// needed) into the tenant's admin group.
func (s *tenantStore) addTenantAdmin(tenant, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return errBadReq("email is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tenant]
	if !ok {
		return errNotFound("tenant")
	}
	if _, ok := s.users[email]; !ok {
		s.users[email] = &userIdentity{Email: email, Name: email, Issuer: "dex", LastSeen: ""}
	}
	t.Admins[email] = struct{}{}
	// Ensure the email is also a member, with the admins group.
	t.Members[email] = ensureContains(t.Members[email], "admins")
	return nil
}

func (s *tenantStore) addProject(tenant, projectName string) (*projectInfo, error) {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return nil, errBadReq("project name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tenant]
	if !ok {
		return nil, errNotFound("tenant")
	}
	if _, exists := s.projects[projectName]; exists {
		return nil, errConflict("project already exists")
	}
	p := &projectInfo{
		Name: projectName,
		UUID: pseudoUUID(projectName),
		Created: today(),
		Tenant:  tenant,
		Roles:   map[string]string{},
	}
	s.projects[projectName] = p
	t.Projects = append(t.Projects, projectName)
	return p, nil
}

func (s *tenantStore) addMember(tenant, email string, groups []string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return errBadReq("email is required")
	}
	if len(groups) == 0 {
		groups = []string{"viewers"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tenant]
	if !ok {
		return errNotFound("tenant")
	}
	for _, g := range groups {
		if _, ok := t.Groups[g]; !ok {
			return errBadReq("unknown group: " + g)
		}
	}
	if _, ok := s.users[email]; !ok {
		s.users[email] = &userIdentity{Email: email, Name: email, Issuer: "dex"}
	}
	t.Members[email] = uniqStrings(groups)
	if contains(groups, "admins") {
		t.Admins[email] = struct{}{}
	}
	return nil
}

// grantRole assigns one role to one user on one project. Role values
// are free-form here ; the server will validate against weft-agent's enum once
// wired.
func (s *tenantStore) grantRole(projectName, email, role string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	role = strings.TrimSpace(role)
	if email == "" || role == "" {
		return errBadReq("email and role are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[projectName]
	if !ok {
		return errNotFound("project")
	}
	t := s.tenants[p.Tenant]
	if t == nil {
		return errNotFound("tenant")
	}
	if _, ok := t.Members[email]; !ok {
		return errBadReq("user is not a member of the project's tenant")
	}
	p.Roles[email] = role
	return nil
}

// ---- Quotas ----

// tenantQuotaView returns the tenant's hard cap, the sum of its
// project quotas, and the per-dimension "remaining" for the tenant
// admin. Tenant-level Projects (count of projects) is computed live
// from len(t.Projects) so it never drifts from reality.
func (s *tenantStore) tenantQuotaView(name string) (map[string]any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[name]
	if !ok {
		return nil, false
	}
	allocated := Quotas{Projects: len(t.Projects)}
	for _, pn := range t.Projects {
		if p := s.projects[pn]; p != nil {
			allocated = allocated.add(p.Quotas)
		}
	}
	return map[string]any{
		"cap":       t.Quotas,
		"allocated": allocated, // sum of children + live project count
		"remaining": remainingMap(t.Quotas, allocated),
	}, true
}

// projectQuotaView returns the project's quota + the parent tenant's
// remaining (so the SPA can draw a "tenant : 32/96 used" bar without
// a second round-trip).
func (s *tenantStore) projectQuotaView(name string) (map[string]any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[name]
	if !ok {
		return nil, false
	}
	t := s.tenants[p.Tenant]
	if t == nil {
		return nil, false
	}
	// allocated_by_siblings = sum(other projects) ; the project itself
	// is excluded so the SPA can model "if I bump my quota to X, do I
	// still fit ?"
	siblings := Quotas{}
	for _, otherName := range t.Projects {
		if otherName == name {
			continue
		}
		if other := s.projects[otherName]; other != nil {
			siblings = siblings.add(other.Quotas)
		}
	}
	return map[string]any{
		"project":         p.Quotas,
		"tenant_cap":      t.Quotas,
		"siblings_total":  siblings,
		"tenant_remaining": remainingMap(t.Quotas, siblings.add(p.Quotas)),
	}, true
}

// setTenantQuota validates and replaces a tenant's cap. Rejected if
// the new cap is lower than the current allocated total on any
// dimension — operator must shrink projects first.
func (s *tenantStore) setTenantQuota(name string, q Quotas) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[name]
	if !ok {
		return errNotFound("tenant")
	}
	allocated := Quotas{Projects: len(t.Projects)}
	for _, pn := range t.Projects {
		if p := s.projects[pn]; p != nil {
			allocated = allocated.add(p.Quotas)
		}
	}
	if dim, ok := allocated.fits(q); !ok {
		return errBadReq("new cap is below current allocation on `" + dim + "` ; shrink the projects first")
	}
	t.Quotas = q
	return nil
}

// setProjectQuota validates against the parent tenant's remaining
// (sum-of-other-projects + new must ≤ tenant cap).
func (s *tenantStore) setProjectQuota(name string, q Quotas) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[name]
	if !ok {
		return errNotFound("project")
	}
	t := s.tenants[p.Tenant]
	if t == nil {
		return errNotFound("tenant")
	}
	candidate := Quotas{Projects: len(t.Projects)}
	for _, otherName := range t.Projects {
		other := s.projects[otherName]
		if other == nil {
			continue
		}
		if otherName == name {
			candidate = candidate.add(q)
		} else {
			candidate = candidate.add(other.Quotas)
		}
	}
	if dim, ok := candidate.fits(t.Quotas); !ok {
		return errBadReq("over tenant cap on `" + dim + "`")
	}
	p.Quotas = q
	return nil
}

// remainingMap projects each capacity into a {got, max, free} triplet
// for the SPA's progress bars. Keeps the same field order as Quotas
// so the UI doesn't need to know about the dimension list.
func remainingMap(cap, used Quotas) map[string]map[string]int {
	pairs := []struct {
		name string
		u, c int
	}{
		{"vcpu", used.VCPU, cap.VCPU},
		{"ram_gib", used.RAMGiB, cap.RAMGiB},
		{"volumes", used.Volumes, cap.Volumes},
		{"volumes_gib", used.VolumesGiB, cap.VolumesGiB},
		{"shares", used.Shares, cap.Shares},
		{"shares_gib", used.SharesGiB, cap.SharesGiB},
		{"buckets", used.Buckets, cap.Buckets},
		{"buckets_gib", used.BucketsGiB, cap.BucketsGiB},
		{"registry_gib", used.RegistryGiB, cap.RegistryGiB},
		{"floating_ips", used.FloatingIPs, cap.FloatingIPs},
		{"projects", used.Projects, cap.Projects},
	}
	out := make(map[string]map[string]int, len(pairs))
	for _, p := range pairs {
		out[p.name] = map[string]int{"used": p.u, "cap": p.c, "free": p.c - p.u}
	}
	return out
}

// userScopes returns the tenants a user is a member of, each with the
// list of projects in that tenant. Drives the cascading topbar
// selector in the SPA : tenant → project. Cluster admins get every
// tenant ; everyone else only their memberships.
func (s *tenantStore) userScopes(u *auth.User) []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := isClusterAdmin(u)
	out := make([]map[string]any, 0)
	for _, t := range sortedTenants(s.tenants) {
		if !all {
			if u == nil {
				return out
			}
			if _, ok := t.Members[u.Email]; !ok {
				continue
			}
		}
		// Copy project names ; ListUserTenants is called on every page
		// load so avoid aliasing the store's slice.
		projs := make([]string, len(t.Projects))
		copy(projs, t.Projects)
		out = append(out, map[string]any{
			"name":     t.Name,
			"domain":   t.Domain,
			"status":   t.Status,
			"projects": projs,
		})
	}
	return out
}

// ---- Authorisation helpers ----

// isClusterAdmin reads the OIDC groups claim. The mock dev user gets
// {"admin", "dev"} so dev-mode unlocks everything.
func isClusterAdmin(u *auth.User) bool {
	if u == nil {
		return false
	}
	for _, g := range u.Groups {
		if g == "admin" || g == "admins" {
			return true
		}
	}
	return false
}

// isAnyTenantAdmin reports whether u is the admin of at least one
// tenant (any group named "admins" is implicit). Cluster admins are
// NOT promoted to a "yes" here — the caller decides how to combine
// the two roles. We want a clean separation between
//
//   - cluster_admin : group claim "admin"/"admins" on the OIDC token
//   - tenant_admin  : at least one Tenant.Admins[u.Email] hit
//
// so the SPA can show a "SUPERADMIN" badge for the former and a
// plain "ADMIN" badge for the latter without conflating the two.
func (s *tenantStore) isAnyTenantAdmin(email string) bool {
	if email == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tenants {
		if _, ok := t.Admins[email]; ok {
			return true
		}
	}
	return false
}

// isMember reports whether u is in the tenant's member set (any group).
// Cluster admins pass implicitly so they can read every tenant detail.
func (s *tenantStore) isMember(u *auth.User, tenant string) bool {
	if u == nil {
		return false
	}
	if isClusterAdmin(u) {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[tenant]
	if !ok {
		return false
	}
	_, ok = t.Members[u.Email]
	return ok
}

// setProjectUUID overrides the pseudo-UUID minted by addProject with
// the real one returned by weft-agent's CreateProject. Idempotent — calling
// with a missing project is a no-op.
func (s *tenantStore) setProjectUUID(name, uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.projects[name]; ok {
		p.UUID = uuid
	}
}

// projectTenant returns the tenant a project belongs to.
func (s *tenantStore) projectTenant(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[name]
	if !ok {
		return "", false
	}
	return p.Tenant, true
}

// isTenantAdmin returns true when u is in the tenant's admin set, OR
// is a cluster admin (cluster admins can do anything a tenant admin can).
func (s *tenantStore) isTenantAdmin(u *auth.User, tenant string) bool {
	if u == nil {
		return false
	}
	if isClusterAdmin(u) {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[tenant]
	if !ok {
		return false
	}
	_, ok = t.Admins[u.Email]
	return ok
}

// ---- HTTP shape errors ----

type httpErr struct {
	code int
	msg  string
}

func (e *httpErr) Error() string { return e.msg }
func errBadReq(m string) error   { return &httpErr{http.StatusBadRequest, m} }
func errNotFound(m string) error { return &httpErr{http.StatusNotFound, m + " not found"} }
func errConflict(m string) error { return &httpErr{http.StatusConflict, m} }
func errForbidden(m string) error { return &httpErr{http.StatusForbidden, m} }

func writeErr(w http.ResponseWriter, err error) {
	if he, ok := err.(*httpErr); ok {
		writeJSON(w, he.code, map[string]string{"error": he.msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// ---- small helpers ----

func sortedTenants(m map[string]*Tenant) []*Tenant {
	out := make([]*Tenant, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedProjects(m map[string]*projectInfo) []*projectInfo {
	out := make([]*projectInfo, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedUsers(m map[string]*userIdentity) []*userIdentity {
	out := make([]*userIdentity, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ensureContains(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func uniqStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := in[:0]
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// pseudoUUID returns a deterministic UUID-shaped string derived from
// seed. Mock store only — weft-agent assigns the real ones. Two FNV-64 hashes
// with different offsets fill the 32 hex nibbles, matched against the
// 8-4-4-4-12 layout so the value looks like a real UUID in the UI.
func pseudoUUID(seed string) string {
	const fnvPrime = uint64(1099511628211)
	hash := func(off uint64) uint64 {
		h := off
		for _, c := range seed {
			h ^= uint64(c)
			h *= fnvPrime
		}
		return h
	}
	hi := hash(1469598103934665603) // FNV offset basis
	lo := hash(0xCBF29CE484222325 ^ 0x9E3779B97F4A7C15)

	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	// Layout : 8-4-4-4-12 hex nibbles + 4 hyphens = 36 bytes.
	hyphen := map[int]bool{8: true, 13: true, 18: true, 23: true}
	idx := 0
	emit := func(nibble byte) {
		for hyphen[idx] {
			out[idx] = '-'
			idx++
		}
		out[idx] = hex[nibble&0xF]
		idx++
	}
	for s := 0; s < 64; s += 4 {
		emit(byte(hi >> uint(60-s)))
	}
	for s := 0; s < 64; s += 4 {
		emit(byte(lo >> uint(60-s)))
	}
	return string(out)
}

func today() string {
	// Stable in dev (deterministic mock) ; production hits ProjectInfo.created.
	return "2026-05-28"
}
