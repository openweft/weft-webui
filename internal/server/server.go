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
	"github.com/openweft/weft-webui/internal/wclient"
)

// live is the optional gRPC client to the weft daemon, set by New
// when the server is launched with a configured socket. Handlers
// switch on `live != nil` to choose between real and mock data.
var live *wclient.Client

// Deps carries everything the HTTP layer needs at construction time.
// Treat it as immutable post-New() ; concurrent reads only.
type Deps struct {
	Logger *slog.Logger
	Static fs.FS
	Live   *wclient.Client

	// Auth is the request-context provider for /api/*. Must be non-nil ;
	// in dev-mode it's configured with ModeNone + a synthetic user.
	Auth *auth.Middleware

	// OIDC is non-nil only when AuthMode=oidc — it owns /api/auth/*.
	OIDC *auth.OIDC

	// DevMode relaxes the CSP for Vite HMR + skips a few warnings.
	DevMode bool
}

// New returns an http.Handler with API + auth + SPA routes mounted
// and all middleware applied.
func New(d Deps) http.Handler {
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

	mux.HandleFunc("GET /api/resources", handleResources)         // metadata (sidebar + columns)
	mux.HandleFunc("GET /api/resources/{id}", handleResourceRows) // rows for one type
	mux.HandleFunc("GET /api/summary", handleSummary)             // counts for the overview
	mux.HandleFunc("POST /api/images/upload", handleImageUpload)

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

	// Network topology + quotas
	mux.HandleFunc("GET /api/network-topology", handleNetworkTopology)
	mux.HandleFunc("GET /api/quotas", handleQuotas)

	// SPA (everything else)
	mux.Handle("/", spaHandler(d.Static))

	// Middleware chain : panic → log → request-id → security-headers →
	// json-defaults → auth → mux. Outer-most wraps run first.
	var h http.Handler = mux
	h = d.Auth.Wrap(h)
	h = withJSONDefaults(h)
	h = withSecurityHeaders(d.DevMode, h)
	h = withRequestID(h)
	h = withLogging(d.Logger, h)
	h = withPanicRecovery(d.Logger, h)
	return h
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
	case "images":
		return imagesCount()
	case "buckets":
		return bucketsCount()
	case "topology":
		return len(resourceByID["networks"].Rows)
	}
	return len(res.Rows)
}

func handleResources(w http.ResponseWriter, _ *http.Request) {
	out := make([]resourceMeta, 0, len(registry))
	for i := range registry {
		res := &registry[i]
		out = append(out, resourceMeta{
			ID: res.ID, Label: res.Label, Section: res.Section,
			Columns: res.Columns, Count: rowCount(res),
		})
	}
	writeJSON(w, http.StatusOK, out)
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
	case "images":
		writeJSON(w, http.StatusOK, imagesList())
		return
	case "buckets":
		writeJSON(w, http.StatusOK, bucketSummaries())
		return
	case "projects":
		if live != nil {
			liveServe(w, r, func() ([]map[string]any, error) { return live.ListProjects(r.Context()) })
			return
		}
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

// handleSummary returns one card per section for the overview dashboard.
func handleSummary(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	out := make([]item, 0, len(registry))
	for i := range registry {
		res := &registry[i]
		out = append(out, item{ID: res.ID, Label: res.Label, Count: rowCount(res)})
	}
	writeJSON(w, http.StatusOK, out)
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
