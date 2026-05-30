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
// Both UserHandler and AdminHandler share the same gRPC client — vzd
// applies its own RBAC based on the forwarded bearer.
var live *wclient.Client

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
	mux.HandleFunc("GET /api/me", d.Auth.MeHandler)
	mux.HandleFunc("POST /api/session/project", d.Auth.SetProjectHandler)

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

// projectFromRequest reads the session's selected project (set by
// /api/session/project), falling back to a query parameter for
// convenience.
func projectFromRequest(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil && u.Project != "" {
		return u.Project
	}
	return r.URL.Query().Get("project")
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
		// Store-only (vzd has no ListTenants yet). User listener filters
		// to the caller's tenants ; admin sees all.
		filter := ""
		if u := auth.UserFromContext(r.Context()); u != nil && !isClusterAdmin(u) {
			filter = u.Email
		}
		writeJSON(w, http.StatusOK, tenantsDB.listTenants(filter))
		return
	case "groups":
		// Store-only (vzd has no ListGroups yet).
		writeJSON(w, http.StatusOK, tenantsDB.listGroups())
		return
	case "projects":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListProjects(r.Context()) })
			return
		}
		// Mock path : carry the tenant column from the store.
		filter := ""
		if u := auth.UserFromContext(r.Context()); u != nil && !isClusterAdmin(u) {
			filter = u.Email
		}
		writeJSON(w, http.StatusOK, tenantsDB.listProjects(filter))
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
	}
	rows := res.Rows
	if rows == nil {
		rows = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// devLogin / devLogout — stubs that make the SPA's auth helpers work
// in dev mode without an IdP. Login bounces home (synthetic user is
// always present) ; logout returns 204.
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
