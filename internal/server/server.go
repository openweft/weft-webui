// Package server wires the weft-webui HTTP surface : a small JSON API
// the SvelteJS dashboard consumes, plus the embedded single-page app
// itself.
//
// New() takes a Deps struct holding everything resolved at startup —
// the slog.Logger, the embedded static FS, the (optional) gRPC live
// client, and the auth middleware that gates /api/*. Cross-cutting
// concerns (security headers, no-store on /api/, request logging) are
// applied as wrapping middleware ; per-route auth is enforced inside
// auth.Middleware.
package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/telemetry"
	"github.com/openweft/weft-webui/internal/wclient"
)

// live is the optional gRPC client to the weft daemon, set by New
// when the server is launched with a configured socket. Handlers
// switch on `live != nil` to choose between real and mock data.
//
// Both UserHandler and AdminHandler share the same gRPC client — weft-agent
// applies its own RBAC based on the forwarded bearer.
var live *wclient.Client

// metrics mirrors the Recorder from Deps so the mutation handlers
// (lifecycle.go) can record per-user action counters without having
// the struct threaded through every signature.
var metrics *telemetry.Recorder

// activePersona stores the persona served by the current handler so
// per-request helpers can branch on it without threading a parameter
// through every signature. Both handlers register themselves before
// returning. Concurrent reads are fine ; writes only happen during
// New / NewAdmin which run once at startup.
var (
	personaUser  = "user"
	personaAdmin = "admin"
)

// Deps carries everything the HTTP layer needs at construction time.
// Treat it as immutable post-New / NewAdmin ; concurrent reads only.
type Deps struct {
	Logger *slog.Logger
	Static fs.FS
	Live   *wclient.Client

	// Auth is the request-context provider for /api/*. Must be non-nil ;
	// in dev-mode it's configured with ModeNone + a synthetic user.
	Auth *auth.Middleware

	// OIDC is non-nil only when AuthMode=oidc — it owns /api/auth/*.
	OIDC *auth.OIDC

	// Metrics is optional ; when set, HTTP middleware records request
	// metrics and the admin handler exposes /metrics. Pass nil to
	// disable telemetry entirely (the gRPC client also reads this).
	Metrics *telemetry.Recorder

	// DevMode relaxes the CSP for Vite HMR + skips a few warnings.
	DevMode bool
}

// New returns the user-facing http.Handler — the public listener.
// Admin-only resources (hosts, users, tenants, groups) and /metrics
// are hidden ; the SPA is served from the same origin so the dashboard
// renders normally for a regular user.
func New(d Deps) http.Handler {
	return buildHandler(d, ScopeUser, personaUser, false)
}

// NewAdmin returns the admin-facing handler — must only be bound on a
// trusted interface (e.g. a WireGuard endpoint). Mounts /metrics and
// surfaces the cluster-wide resources (Hosts, Users, Tenants, Groups)
// in addition to everything a user sees.
func NewAdmin(d Deps) http.Handler {
	return buildHandler(d, ScopeAdmin, personaAdmin, true)
}

// buildHandler is the common assembly. persona drives metrics labels
// and the resource-registry filter ; exposeMetrics decides whether
// /metrics is mounted on this listener.
func buildHandler(d Deps, scope Scope, persona string, exposeMetrics bool) http.Handler {
	live = d.Live
	metrics = d.Metrics
	mux := http.NewServeMux()

	// --- Public routes (no auth) ---
	mux.HandleFunc("GET /api/healthz", handleHealthz)
	mux.HandleFunc("GET /api/readyz", handleReadyz)

	// --- Auth routes (no auth) ---
	if d.OIDC != nil {
		mux.HandleFunc("GET /api/auth/login", d.OIDC.LoginHandler)
		mux.HandleFunc("GET /api/auth/callback", d.OIDC.CallbackHandler)
		mux.HandleFunc("GET /api/auth/logout", d.OIDC.LogoutHandler)
		mux.HandleFunc("POST /api/auth/logout", d.OIDC.LogoutHandler)
	} else {
		// Dev mode : provide stubs so the frontend's logout button still works.
		mux.HandleFunc("GET /api/auth/login", devLogin)
		mux.HandleFunc("POST /api/auth/logout", devLogout)
	}

	// --- Auth-protected routes ---
	// /api/me lives in this package (not in auth) because the role
	// flags depend on the tenant store, which sits in this layer.
	mux.HandleFunc("GET /api/me", handleMe)
	mux.HandleFunc("POST /api/session/scope", d.Auth.SetScopeHandler)

	// Resource catalogue + rows : filtered by scope so the user-facing
	// listener never even acknowledges that hosts/users/tenants exist.
	mux.HandleFunc("GET /api/resources", scopedResources(scope))
	mux.HandleFunc("GET /api/resources/{id}", scopedResourceRows(scope))
	mux.HandleFunc("GET /api/summary", scopedSummary(scope))
	mux.HandleFunc("POST /api/registry/upload", handleRegistryUpload)

	// --- Tenant / identity mutations -----------------------------
	// Cluster-admin only — mounted on the admin listener.
	if scope == ScopeAdmin {
		mux.HandleFunc("POST /api/tenants", handleCreateTenant)
		mux.HandleFunc("POST /api/tenants/{name}/admins", handleAddTenantAdmin)
	}
	// Tenant-admin (delegated). Mounted on both listeners : tenant
	// admins typically work from the user UI ; cluster admins reach
	// the same handlers through the admin port. The handler enforces
	// the per-tenant check.
	mux.HandleFunc("GET /api/tenants/{name}", handleTenantDetail)
	mux.HandleFunc("POST /api/tenants/{name}/projects", handleAddTenantProject)
	mux.HandleFunc("POST /api/tenants/{name}/members", handleAddTenantMember)
	mux.HandleFunc("POST /api/projects/{name}/roles", handleGrantProjectRole)

	// Quotas. Reads are member-gated ; writes are role-gated inside
	// the handler (PUT /api/tenants/.../quota → cluster_admin ;
	// PUT /api/projects/.../quota → tenant_admin).
	mux.HandleFunc("GET /api/tenants/{name}/quota", handleGetTenantQuota)
	mux.HandleFunc("PUT /api/tenants/{name}/quota", handleSetTenantQuota)
	mux.HandleFunc("GET /api/projects/{name}/quota", handleGetProjectQuota)
	mux.HandleFunc("PUT /api/projects/{name}/quota", handleSetProjectQuota)

	// --- Resource lifecycle (live gRPC only) ---------------------
	// Row-action / create-modal endpoints. Each handler short-circuits
	// to 503 when no daemon is wired, so a mock-mode operator can't
	// silently mutate something that isn't there.
	mux.HandleFunc("POST /api/microvms", handleCreateVM)
	mux.HandleFunc("POST /api/microvms/{name}/start", handleStartVM)
	mux.HandleFunc("POST /api/microvms/{name}/stop", handleStopVM)
	mux.HandleFunc("DELETE /api/microvms/{name}", handleDeleteVM)
	mux.HandleFunc("GET /api/microvms/{name}/status", handleVMStatus)
	mux.HandleFunc("GET /api/microvms/{name}/timings", handleVMTimings)
	mux.HandleFunc("GET /api/microvms/{name}/logs", handleVMLogs)
	mux.HandleFunc("POST /api/volumes", handleCreateVolume)
	mux.HandleFunc("DELETE /api/volumes/{uuid}", handleDeleteVolume)
	mux.HandleFunc("POST /api/volumes/{uuid}/attach", handleAttachVolume)
	mux.HandleFunc("POST /api/volumes/{uuid}/detach", handleDetachVolume)
	mux.HandleFunc("POST /api/networks", handleCreateNetwork)
	mux.HandleFunc("DELETE /api/networks/{uuid}", handleDeleteNetwork)
	mux.HandleFunc("POST /api/security-groups", handleCreateSecurityGroup)
	mux.HandleFunc("DELETE /api/security-groups/{uuid}", handleDeleteSecurityGroup)
	mux.HandleFunc("GET /api/security-groups/{uuid}/rules", handleGetSecurityGroupRules)
	mux.HandleFunc("PUT /api/security-groups/{uuid}/rules", handleSetSecurityGroupRules)
	mux.HandleFunc("POST /api/floating-ips", handleAllocateFloatingIP)
	mux.HandleFunc("DELETE /api/floating-ips/{uuid}", handleReleaseFloatingIP)
	mux.HandleFunc("POST /api/floating-ips/{uuid}/map", handleMapFloatingIP)
	mux.HandleFunc("POST /api/floating-ips/{uuid}/unmap", handleUnmapFloatingIP)

	// Scheduling rules (mock store ; no daemon RPC yet).
	mux.HandleFunc("POST /api/scheduling-rules", handleCreateSchedulingRule)
	mux.HandleFunc("DELETE /api/scheduling-rules/{name}", handleDeleteSchedulingRule)

	// Shares (mock store ; tenant-admin gated inside the handler).
	mux.HandleFunc("POST /api/shares", handleCreateShare)
	mux.HandleFunc("DELETE /api/shares/{name}", handleDeleteShare)

	// Flavors catalogue — exposed on BOTH listeners so the user UI's
	// CreateVMModal can offer the flavor picker even when the user UI
	// hides the read-only sidebar entry. Same data the admin's
	// /api/resources/flavors returns.
	mux.HandleFunc("GET /api/flavors", handleListFlavors)

	// Object storage (CubeFS S3)
	mux.HandleFunc("POST /api/buckets", handleCreateBucket)
	mux.HandleFunc("DELETE /api/buckets/{name}", handleDeleteBucket)
	mux.HandleFunc("GET /api/buckets/{name}/objects", handleListObjects)
	mux.HandleFunc("POST /api/buckets/{name}/objects", handleUploadObject)
	mux.HandleFunc("GET /api/buckets/{name}/object", handleGetObject)

	// Shares (CubeFS POSIX filesystems)
	mux.HandleFunc("GET /api/shares/{name}/objects", handleListShareObjects)
	mux.HandleFunc("POST /api/shares/{name}/objects", handleUploadShareObject)
	mux.HandleFunc("GET /api/shares/{name}/object", handleGetShareObject)

	// Network topology + quotas. Topology exposes host-placement info
	// for every node so it's admin-only ; the user listener returns
	// 404 so a stale SPA build never accidentally reveals which host
	// runs a VM.
	if scope == ScopeAdmin {
		mux.HandleFunc("GET /api/network-topology", handleNetworkTopology)
	} else {
		mux.HandleFunc("GET /api/network-topology", notFound)
	}
	mux.HandleFunc("GET /api/quotas", handleQuotas)

	// /metrics is admin-only — never expose the user's TSDB to the
	// public listener. The auth middleware (below) still applies, so
	// an unauthenticated scraper hits 401 even on the admin port. For
	// Prometheus, configure a static bearer or run the scraper inside
	// the WireGuard endpoint.
	if exposeMetrics && d.Metrics != nil {
		mux.Handle("GET /metrics", d.Metrics.Handler())
	}

	// SPA (everything else)
	mux.Handle("/", spaHandler(d.Static))

	// Middleware chain : panic → log → metrics → request-id →
	// security-headers → json-defaults → auth → mux. Outer-most wraps
	// run first. Metrics sits outside auth so 401s are counted too.
	var h http.Handler = mux
	h = d.Auth.Wrap(h)
	h = withJSONDefaults(h)
	h = withSecurityHeaders(d.DevMode, h)
	h = withRequestID(h)
	h = withMetrics(d.Metrics, persona, h)
	h = withLogging(d.Logger, h)
	h = withPanicRecovery(d.Logger, h)
	return h
}

// scopedResources returns only the registry entries visible to scope.
func scopedResources(scope Scope) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		out := make([]resourceMeta, 0, len(registry))
		for i := range registry {
			res := &registry[i]
			if !resolveScope(res.Scope).Has(scope) {
				continue
			}
			out = append(out, resourceMeta{
				ID: res.ID, Label: res.Label, Section: res.Section,
				Columns: res.Columns, Count: rowCount(res),
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// scopedResourceRows refuses to serve admin-only resources from the
// user listener (404, not 403 — don't acknowledge their existence).
func scopedResourceRows(scope Scope) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		res, ok := resourceByID[id]
		if !ok || !resolveScope(res.Scope).Has(scope) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown resource"})
			return
		}
		handleResourceRows(w, r)
	}
}

func scopedSummary(scope Scope) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		type item struct {
			ID    string `json:"id"`
			Label string `json:"label"`
			Count int    `json:"count"`
		}
		out := make([]item, 0, len(registry))
		for i := range registry {
			res := &registry[i]
			if !resolveScope(res.Scope).Has(scope) {
				continue
			}
			out = append(out, item{ID: res.ID, Label: res.Label, Count: rowCount(res)})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleReadyz returns 200 only if the daemon-backed dependencies are
// reachable. In mock mode we always say ready. In live mode we'd ping
// the gRPC client — for now treat "client configured" as ready ; a
// dedicated Ping RPC can replace this trivially.
func handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if live == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "mock"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "live"})
}

// resourceMeta is the registry entry minus the row data.
type resourceMeta struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Section string   `json:"section"`
	Columns []Column `json:"columns"`
	Count   int      `json:"count"`
}

// liveServe runs a live-mode list callback and writes the result,
// surfacing any gRPC error as a 502 with the message.
func liveServe(w http.ResponseWriter, _ *http.Request, fn func() ([]map[string]any, error)) {
	rows, err := fn()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "live: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// rowCount returns the live count for a resource.
func rowCount(res *Resource) int {
	switch res.ID {
	case "registry":
		return registryCount()
	case "buckets":
		return bucketsCount()
	case "topology":
		return len(resourceByID["networks"].Rows)
	}
	return len(res.Rows)
}

// scopeFromRequest returns the (tenant, project) the user has selected
// via /api/session/scope (cascading topbar). Either or both can be
// empty :
//
//   - tenant="" project=""    cluster-wide / no filter (cluster admin
//                             only when the listener serves it)
//   - tenant="acme" project="" tenant-aggregate : sum every project of
//                             the tenant. The mock filters by tenant
//                             membership ; the live gRPC path will
//                             accept this when weft-agent adds the param.
//   - tenant="acme" project="X" project-scoped : full filter.
//
// Query params (?tenant= / ?project=) override the session for
// scripting convenience.
func scopeFromRequest(r *http.Request) (tenant, project string) {
	if u := auth.UserFromContext(r.Context()); u != nil {
		tenant, project = u.Tenant, u.Project
	}
	if q := r.URL.Query().Get("tenant"); q != "" {
		tenant = q
	}
	if q := r.URL.Query().Get("project"); q != "" {
		project = q
	}
	return
}

// projectFromRequest preserves the old single-return helper for the
// gRPC client call sites that only know about projects. When the
// session also carries a tenant, weft-agent will get it via metadata in a
// future revision ; for now we just pass the project name.
func projectFromRequest(r *http.Request) string {
	_, p := scopeFromRequest(r)
	return p
}

func handleResourceRows(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, ok := resourceByID[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown resource"})
		return
	}
	switch id {
	case "registry":
		writeJSON(w, http.StatusOK, registryList())
		return
	case "buckets":
		writeJSON(w, http.StatusOK, bucketSummaries())
		return
	case "tenants":
		// Try live first ; fall back to the in-memory store when the
		// daemon returns Unimplemented (the RPC just landed in proto,
		// the agent side will catch up). The store keeps the user-UI
		// membership filter on the fallback path ; live mode already
		// enforces RBAC via the bearer.
		if live != nil {
			rows, err := live.ListTenants(r.Context())
			if err == nil {
				writeJSON(w, http.StatusOK, rows)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "live: " + err.Error()})
				return
			}
		}
		filter := ""
		if u := auth.UserFromContext(r.Context()); u != nil && !isClusterAdmin(u) {
			filter = u.Email
		}
		writeJSON(w, http.StatusOK, tenantsDB.listTenants(filter))
		return
	case "groups":
		// Store-only (weft-agent has no ListGroups yet).
		writeJSON(w, http.StatusOK, tenantsDB.listGroups())
		return
	case "scheduling-rules":
		// Store-only (weft-agent has no ListSchedulingRules yet). Project
		// filter mirrors the topbar scope ; cluster admin sees all.
		filter := ""
		if u := auth.UserFromContext(r.Context()); u != nil && !isClusterAdmin(u) {
			_, filter = scopeFromRequest(r)
		}
		writeJSON(w, http.StatusOK, schedulingDB.list(filter))
		return
	case "shares":
		// Live-first ; fall back to the mock store on Unimplemented.
		// Scope filtering applies on both paths.
		tenant, project := scopeFromRequest(r)
		if live != nil {
			rows, err := live.ListShares(r.Context(), project)
			if err == nil {
				if project == "" && tenant != "" {
					// Re-filter to the tenant's projects since the live
					// list returned everything we can see.
					allowed := map[string]struct{}{}
					for _, p := range tenantsDB.projectsInTenant(tenant) {
						allowed[p] = struct{}{}
					}
					out := rows[:0]
					for _, r2 := range rows {
						if p, ok := r2["project"].(string); ok {
							if _, in := allowed[p]; in {
								out = append(out, r2)
							}
						}
					}
					rows = out
				}
				writeJSON(w, http.StatusOK, rows)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "live: " + err.Error()})
				return
			}
		}
		if project != "" {
			writeJSON(w, http.StatusOK, sharesDB.list(project))
			return
		}
		if tenant != "" {
			writeJSON(w, http.StatusOK, sharesDB.listByTenant(tenant))
			return
		}
		writeJSON(w, http.StatusOK, sharesDB.list(""))
		return
	case "projects":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListProjects(r.Context()) })
			return
		}
		// Mock path : carry the tenant column from the store, filtered
		// by membership (user-UI) AND by the cascading topbar tenant
		// selection.
		filter := ""
		if u := auth.UserFromContext(r.Context()); u != nil && !isClusterAdmin(u) {
			filter = u.Email
		}
		tenant, _ := scopeFromRequest(r)
		writeJSON(w, http.StatusOK, tenantsDB.listProjects(filter, tenant))
		return
	case "microvms":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListVMs(r.Context(), projectFromRequest(r)) })
			return
		}
	case "networks":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListNetworks(r.Context(), projectFromRequest(r)) })
			return
		}
	case "hosts":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListHosts(r.Context(), "") })
			return
		}
	case "volumes":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListVolumes(r.Context(), projectFromRequest(r)) })
			return
		}
	case "users":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListUsers(r.Context()) })
			return
		}
		// Mock path : memberships column comes from the store.
		writeJSON(w, http.StatusOK, tenantsDB.listUsers())
		return
	case "security-groups":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListSecurityGroups(r.Context(), projectFromRequest(r)) })
			return
		}
	case "floating-ips":
		// Live-first with Unimplemented fallback to the registry's
		// inline mock rows (the table still surfaces something useful
		// while the agent catches up with AllocateFloatingIP).
		if live != nil {
			rows, err := live.ListFloatingIPs(r.Context(), projectFromRequest(r))
			if err == nil {
				writeJSON(w, http.StatusOK, rows)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "live: " + err.Error()})
				return
			}
		}
	}
	rows := res.Rows
	if rows == nil {
		rows = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, applyScopeFilter(rows, r))
}

// applyScopeFilter narrows a mock row set by the session's (tenant,
// project) selection. Rows that don't carry a `project` field pass
// through untouched (cluster-wide resources : hosts, tenants, …).
//
//   - project set     → exact match on row["project"]
//   - project empty   → row["project"] must belong to the selected
//                       tenant (mock aggregate view).
//   - tenant empty    → no narrowing — cluster admin's "(all)" choice.
//
// Live-mode handlers don't go through this path : weft-agent applies its own
// filters via the bearer token and the project parameter.
func applyScopeFilter(rows []map[string]any, r *http.Request) []map[string]any {
	tenant, project := scopeFromRequest(r)
	if tenant == "" && project == "" {
		return rows
	}
	// Build a quick "is this project in the tenant ?" lookup once.
	tenantProjects := map[string]struct{}{}
	if tenant != "" {
		for _, p := range tenantsDB.projectsInTenant(tenant) {
			tenantProjects[p] = struct{}{}
		}
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rp, hasProject := row["project"].(string)
		if !hasProject || rp == "" {
			// Resource isn't project-scoped (or row doesn't carry it).
			// Don't drop it — the registry includes infra-flavoured
			// rows like flavors that are visible everywhere.
			out = append(out, row)
			continue
		}
		if project != "" {
			if rp != project {
				continue
			}
		} else if tenant != "" {
			if _, ok := tenantProjects[rp]; !ok {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

// devLogin / devLogout — stubs that make the SPA's auth helpers work
// in dev mode without an IdP. Login bounces home (synthetic user is
// always present) ; logout returns 204.
// handleMe returns the current user's profile + the two role flags
// the SPA uses to gate affordances and pick a topbar badge :
//
//   - cluster_admin : OIDC group claim is "admin"/"admins" (the
//                     auth.MeHandler equivalent of "superadmin")
//   - tenant_admin  : the email is in at least one Tenant.Admins set,
//                     even when cluster_admin is false. Cluster admins
//                     pass the implicit check elsewhere ; here we
//                     report the *raw* state so the SPA can render a
//                     distinct "ADMIN" badge for delegated tenant
//                     admins who are not cluster admins.
//
// Returning 401 when there's no session lets api.ts trigger the
// /api/auth/login redirect without a separate code path.
func handleMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no session", "login": "/api/auth/login"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sub":           u.Subject,
		"email":         u.Email,
		"name":          u.Name,
		"groups":        u.Groups,
		"tenant":        u.Tenant,
		"project":       u.Project,
		"initials":      u.Initials(),
		"dev":           u.DevMode,
		"cluster_admin": isClusterAdmin(u),
		"tenant_admin":  tenantsDB.isAnyTenantAdmin(u.Email),
		// scopes drives the cascading topbar selector — one entry per
		// tenant the user belongs to, each with its projects.
		"scopes": tenantsDB.userScopes(u),
	})
}

// handleListFlavors returns the flavor catalogue. Sourced from the
// registry's flavors entry so it stays in sync with what the admin
// sidebar shows — only the access path differs.
func handleListFlavors(w http.ResponseWriter, _ *http.Request) {
	res, ok := resourceByID["flavors"]
	if !ok {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}
	rows := res.Rows
	if rows == nil {
		rows = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, rows)
}

func devLogin(w http.ResponseWriter, r *http.Request) {
	rt := r.URL.Query().Get("return_to")
	if rt == "" {
		rt = "/"
	}
	http.Redirect(w, r, rt, http.StatusFound)
}

func devLogout(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// notFound is mounted on routes that exist on the admin listener but
// must not be acknowledged on the user one — same shape as the
// "unknown resource" branch so probes can't distinguish "endpoint not
// here" from "endpoint here but you're not allowed".
func notFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
