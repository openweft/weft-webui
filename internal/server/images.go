package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// OCI registry images live in their own little store rather than the static
// registry, because the dashboard can push to it (upload). Seeded with mock
// artifacts ; wiring to the real registry (zot) means replacing imagesAdd /
// imagesList with oras/containerd calls — the HTTP shapes stay the same.
var (
	imagesMu sync.Mutex
	images   = []map[string]any{
		row("repository", "library/alpine", "tag", "3.21", "type", "container",
			"arch", "amd64, arm64", "registry", "zot.dc-a", "size", "7.8 MiB", "pushed", "3d ago"),
		row("repository", "team-alpha/web", "tag", "v1.4.2", "type", "container",
			"arch", "amd64, arm64", "registry", "zot.dc-a", "size", "52 MiB", "pushed", "5h ago"),
		row("repository", "weft/cloud-boot", "tag", "uefi", "type", "raw",
			"arch", "amd64, arm64, riscv64, loongarch64", "registry", "zot.dc-a", "size", "18 MiB", "pushed", "1d ago"),
		row("repository", "research/jupyter", "tag", "latest", "type", "container",
			"arch", "amd64, arm64", "registry", "zot.dc-c", "size", "1.1 GiB", "pushed", "2d ago"),
		row("repository", "images/debian-12", "tag", "raw", "type", "raw",
			"arch", "amd64, arm64", "registry", "zot.dc-b", "size", "1.9 GiB", "pushed", "1w ago"),
	}
)

func imagesList() []map[string]any {
	imagesMu.Lock()
	defer imagesMu.Unlock()
	out := make([]map[string]any, len(images))
	copy(out, images)
	return out
}

func imagesCount() int {
	imagesMu.Lock()
	defer imagesMu.Unlock()
	return len(images)
}

// imagesAdd prepends so the newest upload shows first.
func imagesAdd(r map[string]any) {
	imagesMu.Lock()
	defer imagesMu.Unlock()
	images = append([]map[string]any{r}, images...)
}

// handleImageUpload accepts a container or a raw multi-arch image. It does not
// (yet) push to a registry — it records the artifact so the UI round-trips.
func handleImageUpload(w http.ResponseWriter, r *http.Request) {
	// Cap the in-memory parse buffer ; large files spill to temp files.
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart form: " + err.Error()})
		return
	}

	typ := strings.TrimSpace(r.FormValue("type"))
	repo := strings.TrimSpace(r.FormValue("repository"))
	tag := strings.TrimSpace(r.FormValue("tag"))
	registry := strings.TrimSpace(r.FormValue("registry"))
	arches := r.Form["arch"]

	switch {
	case typ != "container" && typ != "raw":
		badUpload(w, "type must be 'container' or 'raw'")
		return
	case repo == "":
		badUpload(w, "repository is required")
		return
	case tag == "":
		badUpload(w, "tag is required")
		return
	case len(arches) == 0:
		badUpload(w, "select at least one architecture")
		return
	}
	if registry == "" {
		registry = "zot.dc-a"
	}

	var total int64
	if r.MultipartForm != nil {
		for _, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				total += fh.Size
			}
		}
	}

	newRow := row(
		"repository", repo,
		"tag", tag,
		"type", typ,
		"arch", strings.Join(arches, ", "),
		"registry", registry,
		"size", humanSize(total),
		"pushed", "just now",
	)
	imagesAdd(newRow)
	writeJSON(w, http.StatusCreated, newRow)
}

func badUpload(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func humanSize(n int64) string {
	if n <= 0 {
		return "—"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
