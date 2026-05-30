// Package server wires the weft-webui HTTP surface : a small JSON API the
// SvelteJS dashboard consumes, plus the embedded single-page app itself.
//
// The API is intentionally thin and currently serves mock data from the
// resource registry (see resources.go). Wiring it to the real control
// plane means replacing the handler bodies with calls through
// weft-client / weft-proto — the routes and JSON shapes stay the same.
package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
)

// New returns an http.Handler with the API + SPA routes mounted. static is
// the built SvelteJS app (web/dist), embedded by the caller.
func New(logger *slog.Logger, static fs.FS) http.Handler {
	mux := http.NewServeMux()

	// --- API ---
	mux.HandleFunc("GET /api/healthz", handleHealthz)
	mux.HandleFunc("GET /api/resources", handleResources)         // metadata (sidebar + columns)
	mux.HandleFunc("GET /api/resources/{id}", handleResourceRows) // rows for one type
	mux.HandleFunc("GET /api/summary", handleSummary)             // counts for the overview
	mux.HandleFunc("POST /api/images/upload", handleImageUpload) // push a container / raw multi-arch image

	// --- Object storage (CubeFS S3) ---
	mux.HandleFunc("POST /api/buckets", handleCreateBucket)
	mux.HandleFunc("DELETE /api/buckets/{name}", handleDeleteBucket)
	mux.HandleFunc("GET /api/buckets/{name}/objects", handleListObjects)
	mux.HandleFunc("POST /api/buckets/{name}/objects", handleUploadObject)
	mux.HandleFunc("GET /api/buckets/{name}/object", handleGetObject)

	// --- Shares (CubeFS POSIX filesystems) ---
	mux.HandleFunc("GET /api/shares/{name}/objects", handleListShareObjects)
	mux.HandleFunc("POST /api/shares/{name}/objects", handleUploadShareObject)
	mux.HandleFunc("GET /api/shares/{name}/object", handleGetShareObject)

	// --- Network topology (mesh map) ---
	mux.HandleFunc("GET /api/network-topology", handleNetworkTopology)

	// --- Quotas (overview) ---
	mux.HandleFunc("GET /api/quotas", handleQuotas)

	// --- SPA (everything else) ---
	mux.Handle("/", spaHandler(static))

	return withLogging(logger, withJSONDefaults(mux))
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().UTC()})
}

// resourceMeta is the registry entry minus the row data.
type resourceMeta struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Section string   `json:"section"`
	Columns []Column `json:"columns"`
	Count   int      `json:"count"`
}

// rowCount returns the live count for a resource. The "images" type is backed
// by the mutable images store (uploads), everything else by static rows.
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

func handleResources(w http.ResponseWriter, r *http.Request) {
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
	}
	rows := res.Rows
	if rows == nil {
		rows = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleSummary returns one card per section for the overview dashboard.
func handleSummary(w http.ResponseWriter, r *http.Request) {
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

// withJSONDefaults sets a no-store policy on API responses so the dashboard
// always reflects current state.
func withJSONDefaults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		logger.Info("http",
			"method", r.Method, "path", r.URL.Path,
			"status", sr.status, "dur", time.Since(start).Round(time.Microsecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
