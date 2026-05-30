package server

import (
	"fmt"
	"sync"
)

// OCI registry artifacts live in their own little store rather than the
// static registry, because the dashboard can push to it (upload). Seeded
// with mock artifacts ; wiring to the real registry (zot) means replacing
// registryAdd / registryList with oras/containerd calls — the HTTP shapes
// stay the same.
//
// "artifact" rather than "image" : container images, raw multi-arch
// disks, Helm charts, model weights — all OCI-wrapped blobs.
var (
	registryMu        sync.Mutex
	registryArtifacts = []map[string]any{
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

func registryList() []map[string]any {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]map[string]any, len(registryArtifacts))
	copy(out, registryArtifacts)
	return out
}

func registryCount() int {
	registryMu.Lock()
	defer registryMu.Unlock()
	return len(registryArtifacts)
}

// registryAdd prepends so the newest upload shows first.
func registryAdd(r map[string]any) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registryArtifacts = append([]map[string]any{r}, registryArtifacts...)
}

// (handleRegistryUpload moved to huma — see api_misc.go. The badUpload
// helper is gone too ; huma errors replace the ad-hoc 400 envelope.)

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
