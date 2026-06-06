package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves the built single-page app. Real files (JS, CSS,
// assets) are served as-is ; any other path falls back to index.html
// so the client-side router can take over (deep links, refresh).
//
// Two-FS layout, three-portal split :
//
//   - static : per-portal subtree (dist/<portal>/) — owns index.html.
//   - shared : the dist/ root that backs the shared /assets/* pool
//              Vite emits across portals (entry chunks + dynamic
//              chunks + CSS). Nil collapses to static (legacy flat
//              layout — pre-split builds, dev mode).
func spaHandler(static, shared fs.FS) http.Handler {
	if shared == nil {
		shared = static
	}
	staticFS := http.FileServer(http.FS(static))
	sharedFS := http.FileServer(http.FS(shared))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		// 1) Per-portal first : the index.html for / lives here.
		if f, err := static.Open(name); err == nil {
			_ = f.Close()
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			staticFS.ServeHTTP(w, r)
			return
		}
		// 2) Shared assets : Vite's hash-named JS / CSS / fonts that
		// every portal's index.html references via /assets/<hash>.*.
		if strings.HasPrefix(name, "assets/") {
			if f, err := shared.Open(name); err == nil {
				_ = f.Close()
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				sharedFS.ServeHTTP(w, r)
				return
			}
		}
		// Unknown /api/* paths are real 404s — never falling through
		// to index.html, which would give a misleading 200 + HTML body
		// to a misrouted client. The SPA fallback below is for
		// deep-linked routes the client-side router resolves.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}

		// SPA fallback (deep-linked client-side routes). Serve the
		// per-portal index.html so the SPA loads its own portal bundle.
		idx, err := fs.ReadFile(static, "index.html")
		if err != nil {
			http.Error(w, "frontend not built — run `task build-web` (see README)", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(idx)
	})
}
