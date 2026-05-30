// lifecycle.go — row-action handlers (Start/Stop/Delete) wired
// straight to weft-agent. These exist so the SPA's ResourceTable dropdown
// can do something real beyond viewing a row.
//
// All mutators require a live gRPC client : without --weft-socket the
// handlers return 503 (no daemon). The webui never simulates state
// changes on its mock data — that path would diverge from production
// silently.
//
// Auth model :
//   - GET / list paths already filter by the session's bearer (weft-agent
//     enforces RBAC).
//   - Mutations here trust weft-agent : if the daemon refuses, we proxy the
//     gRPC status code through as a 4xx.
package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
)

// requireLive writes a 503 when the daemon isn't wired. Returns false
// if the request should not proceed.
func requireLive(w http.ResponseWriter) bool {
	if live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "no live weft daemon configured ; start the webui with --weft-socket",
		})
		return false
	}
	return true
}

// resolveProject returns the project to use for a VM mutation : the
// session's selected project, falling back to a query param. Errors
// out when neither is available — a VM mutation needs a project to
// disambiguate the name.
func resolveVMProject(r *http.Request) (string, error) {
	if p := projectFromRequest(r); p != "" {
		return p, nil
	}
	return "", errBadReq("project is required (set scope via /api/session/scope or pass ?project=...)")
}

// userAction logs a per-user action counter so the admin telemetry
// dashboard sees who triggered which mutation. Called from every
// mutator below ; no-op when telemetry is off.
func userAction(r *http.Request, action string) {
	if metrics == nil {
		return
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		metrics.UserAction(u.Subject, action)
	}
}

// (VM lifecycle + inspect handlers moved to huma — see api_microvms.go.)

// flavorSpec is the resolved view of a flavor row : the three numbers
// weft-agent's CreateVMRequest still takes by hand (until the proto
// gains a Flavor field). nil = unknown flavor name.
type flavorSpec struct {
	CPU    uint32
	MemMB  uint64
	DiskGB uint64
}

// resolveFlavor looks up a flavor by name via flavorsCatalogue (see
// flavors.go) and returns its CPU / RAM / ephemeral-disk envelope.
// The catalogue stores RAM as "<n>Gi" or "<n>Mi" ; parsed here at the
// wire boundary so weft-agent gets megabytes regardless of how the
// operator wrote it.
func resolveFlavor(ctx context.Context, name string) (flavorSpec, bool) {
	f, ok := flavorsCatalogue.Get(ctx, name)
	if !ok {
		return flavorSpec{}, false
	}
	return flavorSpec{
		CPU:    uint32(f.VCPU),
		MemMB:  parseRAMtoMB(f.RAM),
		DiskGB: uint64(f.EphemeralGB),
	}, true
}

// parseRAMtoMB turns "4Gi" / "256Mi" / "4096" into megabytes. Unrecognised
// strings come back as 0 — the handler treats that as an invalid flavor
// row, not a 0-MB VM.
func parseRAMtoMB(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "Gi"):
		mult = 1024
		s = strings.TrimSuffix(s, "Gi")
	case strings.HasSuffix(s, "G"):
		mult = 1024
		s = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "Mi"):
		s = strings.TrimSuffix(s, "Mi")
	case strings.HasSuffix(s, "M"):
		s = strings.TrimSuffix(s, "M")
	}
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}

// (handleCreateVM moved to huma — see api_microvms.go. The wire-model
// doc-block lives on createVMInput / the create-vm Operation description.)

// writeBootProperty stamps one reserved weft.boot/* property on the
// newly-created VM. Always guest-readable — the in-guest weft-microvm-agent
// is the consumer ; host-only would defeat the purpose. Goes through
// the same per-VM Properties store as the operator-set ones (see
// vm_metadata.go) so the drawer's Properties tab shows them too.
func writeBootProperty(vmName, key, value string) {
	vmPropsMu.Lock()
	defer vmPropsMu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	props := vmProps[vmName]
	for i, p := range props {
		if p.Key == key {
			props[i] = VMProperty{Key: key, Value: value, GuestReadable: true, UpdatedAt: now}
			vmProps[vmName] = props
			return
		}
	}
	vmProps[vmName] = append(props, VMProperty{
		Key: key, Value: value, GuestReadable: true, UpdatedAt: now,
	})
}

// (Volume handlers moved to huma — see api_storage.go.)

// parsePortRange turns "80", "8000-8100", or "" into (min, max). Kept
// here (not in api_networking.go) because the legacy security-rules
// mock fallback uses the same helper to translate the mock's
// "port_range" string column into the typed SecurityRule shape.
func parsePortRange(v any) (int, int) {
	s, ok := v.(string)
	if !ok || s == "" {
		return 0, 0
	}
	var lo, hi int
	if _, err := fmt.Sscanf(s, "%d-%d", &lo, &hi); err == nil {
		return lo, hi
	}
	if _, err := fmt.Sscanf(s, "%d", &lo); err == nil {
		return lo, lo
	}
	return 0, 0
}

// (handleCreateNetwork, FIP / SG / Network-Delete handlers moved to
// huma — see api_networking.go.)
