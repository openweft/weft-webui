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
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/openweft/weft-webui/internal/audit"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/ratelimit"
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

// liveNet is the optional client to the sibling weft-network
// controller (Routers, Load Balancers, DNS, Scheduling Rules).
// Independent socket / process from weft-agent ; nil when the
// operator hasn't set --weft-network-socket.
var liveNet *wclient.NetworkClient

// metrics mirrors the Recorder from Deps so the mutation handlers
// (lifecycle.go) can record per-user action counters without having
// the struct threaded through every signature.
var metrics *telemetry.Recorder

// auditLogger mirrors Deps.Audit so the mutation handlers can write
// audit events without threading the logger through every signature.
// nil collapses to audit.NopLogger via the Audit() helper.
var auditLogger audit.Logger = audit.NopLogger{}

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
	Logger  *slog.Logger
	Static  fs.FS
	Live    *wclient.Client
	// LiveNet is the optional sibling controller client (Routers /
	// LBs / DNS / Scheduling Rules). nil = fall back to mock stores
	// for those resources.
	LiveNet *wclient.NetworkClient

	// Auth is the request-context provider for /api/*. Must be non-nil ;
	// in dev-mode it's configured with ModeNone + a synthetic user.
	Auth *auth.Middleware

	// OIDC is non-nil only when AuthMode=oidc — it owns /api/auth/*.
	OIDC *auth.OIDC

	// Metrics is optional ; when set, HTTP middleware records request
	// metrics and the admin handler exposes /metrics. Pass nil to
	// disable telemetry entirely (the gRPC client also reads this).
	Metrics *telemetry.Recorder

	// Audit is optional ; when nil, audit events are dropped (the
	// helper collapses to audit.NopLogger). Wire a FileLogger via
	// audit.NewFileLogger to persist admin-classified actions.
	Audit audit.Logger

	// RateLimit is optional ; when non-nil its Middleware wraps the
	// /api/* mux (after auth so we have a session identity to key
	// off). When nil, no limiting — safe for tests and embedded
	// dev runs, NOT recommended in production. Pin defaults in
	// main.go via ratelimit.NewLimiter(ratelimit.Options{}).
	RateLimit *ratelimit.Limiter

	// DevMode relaxes the CSP for Vite HMR + skips a few warnings.
	DevMode bool

	// PolicyStrict flips the bucket-policy evaluator's no-match
	// fallback from allow → deny (AWS-aligned default-deny when a
	// policy exists at all). Propagated to a package-global so
	// evaluatePolicy in objectstorage.go can read it from any handler
	// without threading the flag down every call. See cmd/Deps wiring
	// in New() / NewAdmin().
	PolicyStrict bool
}

// policyStrict is the process-wide read of Deps.PolicyStrict. Set
// once in New()/NewAdmin() so handlers don't need a closure dance.
// See the field comment on Deps.PolicyStrict for semantics.
var policyStrict bool

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
	liveNet = d.LiveNet
	metrics = d.Metrics
	if d.Audit != nil {
		auditLogger = d.Audit
	} else {
		auditLogger = audit.NopLogger{}
	}
	policyStrict = d.PolicyStrict

	// Flip the flavor + script catalogues to live-first when the
	// agent client is wired. The mem seed stays as the fallback
	// path inside each wrapper, so Unimplemented agents still get
	// the dev rows for reads. Writes (Set/Delete) on scripts go
	// straight through to the agent — masking a write failure
	// behind a mem pretend-accept would lie to the dashboard.
	if live != nil {
		flavorsCatalogue = newLiveFlavorCatalogue(live)
		scriptsCatalogue = newLiveScriptCatalogue(live)
	} else {
		flavorsCatalogue = newMemFlavorCatalogue()
		scriptsCatalogue = newMemScriptCatalogue()
	}
	mux := http.NewServeMux()

	// Typed REST API : huma-generated handlers + OpenAPI 3.1 at
	// /api/openapi + interactive docs at /api/docs. Mounted on the
	// same mux so the existing middleware chain (security, logging,
	// metrics, request-id, panic recovery, auth) wraps it unchanged.
	// One huma.API per listener — scope drives which operations get
	// registered, so the user listener never even acknowledges
	// admin-only endpoints (404 instead of 403). /api/healthz +
	// /api/readyz live inside the huma surface too — see api_misc.go.
	mountAPI(mux, scope)

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
	// /api/me moved to huma (api_tenants.go) since the role flags
	// depend on the tenant store which sits in this layer.
	// Server-Sent Events stream bridging the agent's WatchEvents RPC
	// to the SPA's EventSource subscription. Open per browser tab,
	// auto-reconnects on disconnect. (Stays on stdlib — huma's
	// streaming story is heavier than what this needs.)
	mux.HandleFunc("GET /api/events", handleEvents)
	mux.HandleFunc("POST /api/session/scope", d.Auth.SetScopeHandler)

	// (/api/resources, /api/resources/{id}, /api/summary,
	// /api/registry/upload moved to huma — see api_misc.go.)

	// (Tenants, projects, quotas, /api/me all moved to huma — see
	// api_tenants.go. Cluster-admin / tenant-admin gating preserved
	// server-side ; scope-gated registration handles the user-listener
	// 404 for the cluster-admin endpoints.)

	// --- Resource lifecycle (live gRPC only) ---------------------
	// VM lifecycle + per-VM metadata routes all live in huma now.
	// See api_microvms.go (create/start/stop/delete/status/timings/
	// logs) and api_microvm_metadata.go (properties / UEFI vars /
	// sshkey assignments).
	// Volumes / Networks / SGs / Floating-IPs / Routers / LBs / DNS /
	// Scheduling-rules — all moved to huma (api_storage.go +
	// api_networking.go).

	// (Shares lifecycle moved to huma — see api_storage.go.)

	// (Flavors, scripts, ssh-keys catalogues moved to huma — see
	// api_flavors.go / api_scripts.go / api_sshkeys.go.)

	// (Object storage + share storage moved to huma — see api_storage.go.)

	// Network topology + quotas. Topology exposes host-placement info
	// for every node so it's admin-only ; the user listener returns
	// 404 so a stale SPA build never accidentally reveals which host
	// runs a VM.
	if scope != ScopeAdmin {
		// User listener returns 404 on /api/network-topology so a stale
		// SPA build never accidentally reveals host placement. The
		// admin listener gets the typed huma op in api_networking.go.
		mux.HandleFunc("GET /api/network-topology", notFound)
	}
	// (/api/quotas moved to huma — see api_tenants.go.)

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

	// Middleware chain : panic → log → metrics → request-id → http-req
	// → security-headers → json-defaults → auth → rate-limit → mux.
	// Outer-most wraps run first. Metrics sits outside auth so 401s
	// are counted too. Rate-limit sits BETWEEN auth and the mux so
	// the per-user key has the session identity to read.
	var h http.Handler = mux
	if d.RateLimit != nil {
		h = d.RateLimit.Middleware(h)
	}
	h = d.Auth.Wrap(h)
	h = withJSONDefaults(h)
	h = withSecurityHeaders(d.DevMode, h)
	h = withRequestID(h)
	h = withHTTPRequest(h)
	h = withMetrics(d.Metrics, persona, h)
	h = withLogging(d.Logger, h)
	h = withPanicRecovery(d.Logger, h)
	return h
}

// scopedResources returns only the registry entries visible to scope.
// (scopedResources / scopedResourceRows / scopedSummary moved to
// huma — see api_misc.go.)

// scopedRowCount returns the count of rows visible under the session
// scope (tenant + project). When both are empty the cluster-wide count
// is returned. For resources without a project column the global
// count stands — the static catalogue (flavors, security-rules,
// hosts) is the same in every scope.
func scopedRowCount(res *Resource, tenant, project string) int {
	if tenant == "" && project == "" {
		return rowCount(res)
	}
	switch res.ID {
	case "tenants":
		// Tenants is membership-filtered ; here we approximate by
		// "is THIS tenant visible?". One when the scope's tenant
		// is the row, zero otherwise. Cluster-wide stays at rowCount.
		if tenant != "" {
			if _, ok := tenantsDB.tenantDetail(tenant); ok {
				return 1
			}
			return 0
		}
		return rowCount(res)
	case "projects":
		return len(tenantsDB.listProjects("", tenant))
	case "shares":
		if project != "" {
			return len(sharesDB.list(project))
		}
		return len(sharesDB.listByTenant(tenant))
	case "scheduling-rules":
		return len(schedulingDB.list(project))
	}
	// Generic path : count rows whose `project` cell falls in the
	// scope. Rows with no project column count as-is — they're
	// either cluster-wide (hosts, flavors) or non-project-scoped
	// (security-rules grouped by SG name).
	tenantProjects := map[string]struct{}{}
	if tenant != "" {
		for _, p := range tenantsDB.projectsInTenant(tenant) {
			tenantProjects[p] = struct{}{}
		}
	}
	n := 0
	for _, row := range res.Rows {
		rp, ok := row["project"].(string)
		if !ok || rp == "" {
			n++
			continue
		}
		if project != "" {
			if rp == project {
				n++
			}
			continue
		}
		if _, in := tenantProjects[rp]; in {
			n++
		}
	}
	return n
}

// (handleHealthz / handleReadyz moved to huma — see api_misc.go.)

// resourceMeta is the registry entry minus the row data.
type resourceMeta struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Section string   `json:"section"`
	Columns []Column `json:"columns"`
	Count   int      `json:"count"`
}

// liveServe runs a live-mode paginated list callback and writes the
// result through writePagedThrough — the daemon already paginated, the
// JSON layer just relays its cursor. Any gRPC error becomes a 502 with
// the message.
func liveServe(w http.ResponseWriter, _ *http.Request, fn func() ([]map[string]any, string, error)) {
	rows, next, err := fn()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "live: " + err.Error()})
		return
	}
	writePagedThrough(w, rows, next)
}

// rowCount returns the live count for a resource (cluster-wide).
// Store-backed resources delegate to their store ; everything else
// counts inline rows. Used by /api/resources metadata + the no-scope
// summary path.
func rowCount(res *Resource) int {
	switch res.ID {
	case "registries":
		return registryCount()
	case "dns":
		// Unified DNS entry — badge surfaces zone count.
		if zones, ok := resourceByID["dns-zones"]; ok {
			return len(zones.Rows)
		}
		return 0
	case "flavors":
		fl, _ := flavorsCatalogue.List(context.Background())
		return len(fl)
	case "scripts":
		ss, _ := scriptsCatalogue.List(context.Background())
		return len(ss)
	case "ssh-keys":
		ks, _ := sshKeysCatalogue.List(context.Background())
		return len(ks)
	case "buckets":
		return bucketsCount()
	case "topology":
		return len(resourceByID["networks"].Rows)
	case "tenants":
		return len(tenantsDB.listTenants(""))
	case "projects":
		return len(tenantsDB.listProjects("", ""))
	case "users":
		return len(tenantsDB.listUsers())
	case "groups":
		return len(tenantsDB.listGroups())
	case "shares":
		return len(sharesDB.list(""))
	case "scheduling-rules":
		return len(schedulingDB.list(""))
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
	case "registries":
		writePage(w, r, registryList())
		return
	case "flavors":
		// Same source as /api/flavors — see flavors.go for the etcd
		// migration plan that makes this branch a thin proxy.
		writePage(w, r, flavorRows(r.Context()))
		return
	case "scripts":
		// Same indirection as flavors. See scripts.go for the etcd
		// migration plan.
		writePage(w, r, scriptRows(r.Context()))
		return
	case "ssh-keys":
		writePage(w, r, sshKeyRows(r.Context()))
		return
	case "buckets":
		writePage(w, r, bucketSummaries())
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
				writePage(w, r, rows)
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
		writePage(w, r, tenantsDB.listTenants(filter))
		return
	case "groups":
		// Store-only (weft-agent has no ListGroups yet).
		writePage(w, r, tenantsDB.listGroups())
		return
	case "scheduling-rules":
		// Live-first via weft-network ; fall back to the in-memory
		// store on Unimplemented / no controller wired.
		_, project := scopeFromRequest(r)
		if liveNet != nil {
			rows, next, err := liveNet.ListSchedulingRules(r.Context(), project, pageOptsFromRequest(r))
			if err == nil {
				writePagedThrough(w, rows, next)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "net: " + err.Error()})
				return
			}
		}
		filter := ""
		if u := auth.UserFromContext(r.Context()); u != nil && !isClusterAdmin(u) {
			filter = project
		}
		writePage(w, r, schedulingDB.list(filter))
		return
	case "routers":
		// Live-first via weft-network ; otherwise fall through to the
		// inline mock rows in the registry.
		_, project := scopeFromRequest(r)
		if liveNet != nil {
			rows, next, err := liveNet.ListRouters(r.Context(), project, pageOptsFromRequest(r))
			if err == nil {
				writePagedThrough(w, rows, next)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "net: " + err.Error()})
				return
			}
		}
	case "loadbalancers":
		_, project := scopeFromRequest(r)
		if liveNet != nil {
			rows, next, err := liveNet.ListLoadBalancers(r.Context(), project, pageOptsFromRequest(r))
			if err == nil {
				writePagedThrough(w, rows, next)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "net: " + err.Error()})
				return
			}
		}
	case "dns-zones":
		_, project := scopeFromRequest(r)
		if liveNet != nil {
			rows, next, err := liveNet.ListDNSZones(r.Context(), project, pageOptsFromRequest(r))
			if err == nil {
				writePagedThrough(w, rows, next)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "net: " + err.Error()})
				return
			}
		}
	case "dns-records":
		// Records query is zone-scoped at the wire ; the UI's "filter
		// by zone" picks up via the existing front-end dropdown.
		// Pass empty = every zone (the controller does the filtering).
		if liveNet != nil {
			rows, next, err := liveNet.ListDNSRecords(r.Context(), "", pageOptsFromRequest(r))
			if err == nil {
				writePagedThrough(w, rows, next)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "net: " + err.Error()})
				return
			}
		}
	case "shares":
		// Live-first ; fall back to the mock store on Unimplemented.
		// Scope filtering applies on both paths.
		tenant, project := scopeFromRequest(r)
		if live != nil {
			rows, next, err := live.ListShares(r.Context(), project, pageOptsFromRequest(r))
			if err == nil {
				if project == "" && tenant != "" {
					// Re-filter to the tenant's projects since the live
					// list returned everything we can see. The cursor
					// stays valid : the daemon paginated unfiltered, this
					// shaves rows after the fact ; "Load more" still walks
					// the unfiltered cursor and the same filter runs again.
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
				writePagedThrough(w, rows, next)
				return
			}
			if !wclient.IsUnimplemented(err) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "live: " + err.Error()})
				return
			}
		}
		if project != "" {
			writePage(w, r, sharesDB.list(project))
			return
		}
		if tenant != "" {
			writePage(w, r, sharesDB.listByTenant(tenant))
			return
		}
		writePage(w, r, sharesDB.list(""))
		return
	case "projects":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, string, error) {
				return live.ListProjects(r.Context(), pageOptsFromRequest(r))
			})
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
		writePage(w, r, tenantsDB.listProjects(filter, tenant))
		return
	case "microvms":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, string, error) {
				return live.ListVMs(r.Context(), projectFromRequest(r), pageOptsFromRequest(r))
			})
			return
		}
	case "networks":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, string, error) {
				return live.ListNetworks(r.Context(), projectFromRequest(r), pageOptsFromRequest(r))
			})
			return
		}
	case "hosts":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, string, error) {
				return live.ListHosts(r.Context(), "", pageOptsFromRequest(r))
			})
			return
		}
	case "volumes":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, string, error) {
				return live.ListVolumes(r.Context(), projectFromRequest(r), pageOptsFromRequest(r))
			})
			return
		}
	case "users":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, string, error) {
				return live.ListUsers(r.Context(), pageOptsFromRequest(r))
			})
			return
		}
		// Mock path : memberships column comes from the store.
		writePage(w, r, tenantsDB.listUsers())
		return
	case "security-groups":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, string, error) {
				return live.ListSecurityGroups(r.Context(), projectFromRequest(r), pageOptsFromRequest(r))
			})
			return
		}
	case "floating-ips":
		// Live-first with Unimplemented fallback to the registry's
		// inline mock rows (the table still surfaces something useful
		// while the agent catches up with AllocateFloatingIP).
		if live != nil {
			rows, next, err := live.ListFloatingIPs(r.Context(), projectFromRequest(r), pageOptsFromRequest(r))
			if err == nil {
				writePagedThrough(w, rows, next)
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
	writePage(w, r, applyScopeFilter(rows, r))
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

// pageOptsFromRequest reads ?limit / ?page_token off a list request and
// hands them to wclient as ListOpts. Limit clamped to [0, 1000] (0 =
// daemon default). Used by the live branches so the gRPC source owns
// the cursor when it implements pagination itself.
func pageOptsFromRequest(r *http.Request) wclient.ListOpts {
	o := wclient.ListOpts{}
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 1000 {
			o.Limit = int32(n)
		}
	}
	o.PageToken = r.URL.Query().Get("page_token")
	return o
}

// writePagedThrough is the live-mode sibling of writePage : it emits
// {rows, next} as-is from an upstream that already paginated, without
// re-slicing on this side. total is -1 (sentinel for "unknown") so the
// SPA hides the parenthetical "of N" — the daemon doesn't tell us the
// global count.
func writePagedThrough(w http.ResponseWriter, rows []map[string]any, next string) {
	if rows == nil {
		rows = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":  rows,
		"next":  next,
		"total": -1,
	})
}

// writePage emits the {rows, next} envelope every /api/resources/:id and
// related list endpoints share. ?limit=N (1..1000 ; default 50) caps the
// page. ?page_token is opaque to the caller — today's value is a base64
// offset since the mock store is in-memory ; once the gRPC source paginates,
// this becomes a real keyset cursor without any caller-visible change.
//
// rows == nil is normalised to `[]` so the SPA never has to guard against
// "rows might be missing".
func writePage(w http.ResponseWriter, r *http.Request, rows []map[string]any) {
	if rows == nil {
		rows = []map[string]any{}
	}
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	offset := 0
	if t := r.URL.Query().Get("page_token"); t != "" {
		if b, err := base64.RawURLEncoding.DecodeString(t); err == nil {
			if n, err := strconv.Atoi(string(b)); err == nil && n >= 0 {
				offset = n
			}
		}
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	end := offset + limit
	next := ""
	if end < len(rows) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	} else {
		end = len(rows)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":  rows[offset:end],
		"next":  next,
		"total": len(rows),
	})
}
