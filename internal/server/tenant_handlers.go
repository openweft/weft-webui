// tenant_handlers.go — HTTP handlers for the tenant mutations.
//
// Authorisation policy (mirrored in the SPA for affordance gating) :
//
//   - cluster admin    creates tenants and elects tenant admins
//   - tenant admin     within their tenants : add projects, add
//                      members, grant per-project roles
//   - everyone else    read-only
//
// Each handler resolves the user via auth.UserFromContext (the auth
// middleware injects it) then defers to tenantsDB. Errors thread back
// through writeErr so the SPA gets a stable {error: string} shape.
package server

import (
	"net/http"

	"github.com/openweft/weft-webui/internal/auth"
)

// --- Cluster admin only ----------------------------------------------

// handleCreateTenant : POST /api/tenants  {name, domain}
func handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if !isClusterAdmin(u) {
		writeErr(w, errForbidden("cluster admin required"))
		return
	}
	var body struct {
		Name, Domain string
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if err := tenantsDB.createTenant(body.Name, body.Domain); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name})
}

// handleAddTenantAdmin : POST /api/tenants/{name}/admins  {email}
func handleAddTenantAdmin(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if !isClusterAdmin(u) {
		writeErr(w, errForbidden("cluster admin required"))
		return
	}
	var body struct{ Email string }
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if err := tenantsDB.addTenantAdmin(r.PathValue("name"), body.Email); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": body.Email})
}

// --- Tenant admin (delegated) ----------------------------------------

// handleAddTenantProject : POST /api/tenants/{name}/projects  {name}
//
// Two paths interleave :
//
//   - mock : tenantsDB.addProject mints a UUID, tracks the tenant ↔
//            project mapping in-memory.
//   - live : weft-agent's CreateProject is the source of truth ; we still
//            need the tenant ↔ project mapping locally because weft-agent
//            has no tenant model yet, so we run the mock path with
//            the daemon-issued UUID injected.
func handleAddTenantProject(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("name")
	u := auth.UserFromContext(r.Context())
	if !tenantsDB.isTenantAdmin(u, tenant) {
		writeErr(w, errForbidden("tenant admin required"))
		return
	}
	var body struct{ Name string }
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}

	var liveUUID string
	if live != nil {
		uuid, err := live.CreateProject(r.Context(), body.Name)
		if err != nil {
			writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
			return
		}
		liveUUID = uuid
	}

	p, err := tenantsDB.addProject(tenant, body.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if liveUUID != "" {
		tenantsDB.setProjectUUID(p.Name, liveUUID)
		p.UUID = liveUUID
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"name": p.Name, "uuid": p.UUID, "tenant": p.Tenant, "created": p.Created,
	})
}

// handleAddTenantMember : POST /api/tenants/{name}/members  {email, groups[]}
func handleAddTenantMember(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("name")
	u := auth.UserFromContext(r.Context())
	if !tenantsDB.isTenantAdmin(u, tenant) {
		writeErr(w, errForbidden("tenant admin required"))
		return
	}
	var body struct {
		Email  string
		Groups []string
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if err := tenantsDB.addMember(tenant, body.Email, body.Groups); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": body.Email, "groups": body.Groups})
}

// handleGrantProjectRole : POST /api/projects/{name}/roles  {email, role}
//
// The role string is webui-side metadata for now (weft-agent has membership
// but no per-project role enum yet) — we mirror it on the store and,
// when live, also call AddProjectMember so weft-agent sees the membership.
// Email→user UUID and name→project UUID resolutions go through the
// daemon so the lookups match what other clients see.
func handleGrantProjectRole(w http.ResponseWriter, r *http.Request) {
	projectName := r.PathValue("name")
	u := auth.UserFromContext(r.Context())

	tenant, ok := tenantsDB.projectTenant(projectName)
	if !ok {
		writeErr(w, errNotFound("project"))
		return
	}
	if !tenantsDB.isTenantAdmin(u, tenant) {
		writeErr(w, errForbidden("tenant admin required"))
		return
	}
	var body struct{ Email, Role string }
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}

	if live != nil {
		projUUID, err := live.ProjectUUIDByName(r.Context(), projectName)
		if err != nil {
			writeErr(w, &httpErr{http.StatusBadGateway, "live: lookup project: " + err.Error()})
			return
		}
		if projUUID == "" {
			writeErr(w, errNotFound("project (not in weft-agent)"))
			return
		}
		userUUID, err := live.UserUUIDByEmail(r.Context(), body.Email)
		if err != nil {
			writeErr(w, &httpErr{http.StatusBadGateway, "live: lookup user: " + err.Error()})
			return
		}
		if userUUID == "" {
			writeErr(w, errBadReq("user not found in weft-agent: "+body.Email))
			return
		}
		if err := live.AddProjectMember(r.Context(), projUUID, userUUID); err != nil {
			writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
			return
		}
	}

	if err := tenantsDB.grantRole(projectName, body.Email, body.Role); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": body.Email, "role": body.Role})
}

// --- Detail (read) ---------------------------------------------------

// handleTenantDetail : GET /api/tenants/{name}
//
// Returns the full tenant view (projects + members + groups + roles)
// in one call so the SPA's drill-down doesn't need to chain requests.
// A user who isn't a member gets 404 (not 403 — same don't-acknowledge
// principle as the admin-only resources).
func handleTenantDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	u := auth.UserFromContext(r.Context())

	detail, ok := tenantsDB.tenantDetail(name)
	if !ok {
		writeErr(w, errNotFound("tenant"))
		return
	}
	if !isClusterAdmin(u) {
		// Filter : member-or-404.
		if !tenantsDB.isMember(u, name) {
			writeErr(w, errNotFound("tenant"))
			return
		}
	}
	// Annotate the response with the caller's effective role so the
	// SPA knows which affordances to render. Saves a round-trip.
	detail["caller"] = map[string]any{
		"email":         emailOf(u),
		"cluster_admin": isClusterAdmin(u),
		"tenant_admin":  tenantsDB.isTenantAdmin(u, name),
	}
	writeJSON(w, http.StatusOK, detail)
}

func emailOf(u *auth.User) string {
	if u == nil {
		return ""
	}
	return u.Email
}

// --- Quotas ----------------------------------------------------------

// handleGetTenantQuota : GET /api/tenants/{name}/quota
// Returns {cap, allocated, remaining{dim → {used,cap,free}}}.
func handleGetTenantQuota(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	u := auth.UserFromContext(r.Context())
	if !tenantsDB.isMember(u, name) {
		writeErr(w, errNotFound("tenant"))
		return
	}
	v, ok := tenantsDB.tenantQuotaView(name)
	if !ok {
		writeErr(w, errNotFound("tenant"))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSetTenantQuota : PUT /api/tenants/{name}/quota  {<Quotas>}
// Cluster-admin only. The handler rejects a cap that's below current
// allocation so a tenant admin doesn't end up with negative remaining.
func handleSetTenantQuota(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if !isClusterAdmin(u) {
		writeErr(w, errForbidden("cluster admin required"))
		return
	}
	var body Quotas
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if err := tenantsDB.setTenantQuota(r.PathValue("name"), body); err != nil {
		writeErr(w, err)
		return
	}
	v, _ := tenantsDB.tenantQuotaView(r.PathValue("name"))
	writeJSON(w, http.StatusOK, v)
}

// handleGetProjectQuota : GET /api/projects/{name}/quota
func handleGetProjectQuota(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	u := auth.UserFromContext(r.Context())
	tenant, ok := tenantsDB.projectTenant(name)
	if !ok {
		writeErr(w, errNotFound("project"))
		return
	}
	if !tenantsDB.isMember(u, tenant) {
		writeErr(w, errNotFound("project"))
		return
	}
	v, ok := tenantsDB.projectQuotaView(name)
	if !ok {
		writeErr(w, errNotFound("project"))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSetProjectQuota : PUT /api/projects/{name}/quota  {<Quotas>}
// Tenant-admin of the project's tenant (cluster admins pass implicitly).
// Validated against the tenant cap : sum(other projects) + new ≤ cap.
func handleSetProjectQuota(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	u := auth.UserFromContext(r.Context())
	tenant, ok := tenantsDB.projectTenant(name)
	if !ok {
		writeErr(w, errNotFound("project"))
		return
	}
	if !tenantsDB.isTenantAdmin(u, tenant) {
		writeErr(w, errForbidden("tenant admin required"))
		return
	}
	var body Quotas
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if err := tenantsDB.setProjectQuota(name, body); err != nil {
		writeErr(w, err)
		return
	}
	v, _ := tenantsDB.projectQuotaView(name)
	writeJSON(w, http.StatusOK, v)
}
