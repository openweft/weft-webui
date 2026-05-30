package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves the built single-page app. Real files (JS, CSS, assets)
// are served as-is ; any other path falls back to index.html so the
// client-side router can take over (deep links, refresh).
func spaHandler(static fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := static.Open(name); err == nil {
			_ = f.Close()
			// Hash the immutable Vite assets aggressively, leave the rest fresh.
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback.
		idx, err := fs.ReadFile(static, "index.html")
		if err != nil {
			http.Error(w, "frontend not built — run `task build-web` (see README)", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(idx)
	})
}
