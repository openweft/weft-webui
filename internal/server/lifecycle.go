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
	"github.com/openweft/weft-webui/internal/wclient"
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
// newly-created VM. Always guest-readable — the in-guest weft-vm-agent
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

// --- Volume / Network mutators -------------------------------------

// handleAttachVolume : POST /api/volumes/{uuid}/attach  {VMUUID}
//
// Attaches a volume to a VM identified by UUID. The caller looks up
// the VM UUID from VMStatus or ListVMs before calling — weft-agent
// keys on UUID, not name.
func handleAttachVolume(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	uuid := r.PathValue("uuid")
	var body struct{ VMUUID string }
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.VMUUID == "" {
		writeErr(w, errBadReq("vm_uuid is required"))
		return
	}
	if err := live.AttachVolume(r.Context(), uuid, body.VMUUID); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "volume.attach")
	writeJSON(w, http.StatusOK, map[string]string{"volume": uuid, "vm": body.VMUUID})
}

// handleDetachVolume : POST /api/volumes/{uuid}/detach
func handleDetachVolume(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	if err := live.DetachVolume(r.Context(), r.PathValue("uuid")); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "volume.detach")
	w.WriteHeader(http.StatusNoContent)
}

func handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	uuid := r.PathValue("uuid")
	if uuid == "" {
		writeErr(w, errBadReq("uuid is required"))
		return
	}
	if err := live.DeleteVolume(r.Context(), uuid); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "volume.delete")
	w.WriteHeader(http.StatusNoContent)
}

func handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Name, Format string
		SizeGiB      int64
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" || body.SizeGiB <= 0 {
		writeErr(w, errBadReq("name and a positive size_gib are required"))
		return
	}
	if cerr := live.CreateVolume(r.Context(), project, body.Name, body.SizeGiB, body.Format); cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	userAction(r, "volume.create")
	writeJSON(w, http.StatusCreated, map[string]any{"name": body.Name, "project": project, "size_gib": body.SizeGiB})
}

// handleCreateNetwork : POST /api/networks  {Name, CIDR, Gateway,
// Type, DNSServers[]}. Project comes from the session scope.
func handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Name, CIDR, Gateway, Type string
		DNSServers                []string
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" || body.CIDR == "" {
		writeErr(w, errBadReq("name and cidr are required"))
		return
	}
	if cerr := live.CreateNetwork(r.Context(), wclient.CreateNetworkOpts{
		Project: project, Name: body.Name, CIDR: body.CIDR,
		Gateway: body.Gateway, Type: body.Type, DNSServers: body.DNSServers,
	}); cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	userAction(r, "network.create")
	writeJSON(w, http.StatusCreated, map[string]any{"name": body.Name, "project": project, "cidr": body.CIDR})
}

// --- Floating IPs --------------------------------------------------

// handleAllocateFloatingIP : POST /api/floating-ips {Network}
// Project from session scope ; 503 in mock mode (no fallback — the
// store path would mint a fake address that wouldn't route).
func handleAllocateFloatingIP(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct{ Network string }
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Network == "" {
		writeErr(w, errBadReq("network is required"))
		return
	}
	uuid, addr, cerr := live.AllocateFloatingIP(r.Context(), project, body.Network)
	if cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	userAction(r, "floating-ip.allocate")
	writeJSON(w, http.StatusCreated, map[string]any{
		"uuid": uuid, "address": addr, "network": body.Network, "project": project,
	})
}

// handleReleaseFloatingIP : DELETE /api/floating-ips/{uuid}
func handleReleaseFloatingIP(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	if err := live.ReleaseFloatingIP(r.Context(), r.PathValue("uuid")); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "floating-ip.release")
	w.WriteHeader(http.StatusNoContent)
}

// handleMapFloatingIP : POST /api/floating-ips/{uuid}/map  {TargetKind, TargetName}
func handleMapFloatingIP(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	var body struct{ TargetKind, TargetName string }
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.TargetKind != "vm" && body.TargetKind != "lb" {
		writeErr(w, errBadReq("target_kind must be 'vm' or 'lb'"))
		return
	}
	if body.TargetName == "" {
		writeErr(w, errBadReq("target_name is required"))
		return
	}
	if err := live.MapFloatingIP(r.Context(), r.PathValue("uuid"), body.TargetKind, body.TargetName); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "floating-ip.map")
	writeJSON(w, http.StatusOK, map[string]string{"uuid": r.PathValue("uuid"), "target": body.TargetName})
}

// handleUnmapFloatingIP : POST /api/floating-ips/{uuid}/unmap
func handleUnmapFloatingIP(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	if err := live.UnmapFloatingIP(r.Context(), r.PathValue("uuid")); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "floating-ip.unmap")
	w.WriteHeader(http.StatusNoContent)
}

// --- Security groups -----------------------------------------------

// handleCreateSecurityGroup : POST /api/security-groups
//
// Body : { Name, Description, Rules:[{direction,protocol,port_min,
//          port_max,remote_cidr,remote_group_uuid}] }
// Project comes from the session scope.
func handleCreateSecurityGroup(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Name, Description string
		Rules             []wclient.SecurityRule
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" {
		writeErr(w, errBadReq("name is required"))
		return
	}
	uuid, cerr := live.CreateSecurityGroup(r.Context(), wclient.CreateSecurityGroupOpts{
		Project: project, Name: body.Name, Description: body.Description, Rules: body.Rules,
	})
	if cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	userAction(r, "security-group.create")
	writeJSON(w, http.StatusCreated, map[string]any{
		"name": body.Name, "project": project, "uuid": uuid, "rules": len(body.Rules),
	})
}

// handleGetSecurityGroupRules : GET /api/security-groups/{uuid}/rules
//
// Returns the rule list for one SG. Live-first ; on Unimplemented OR
// no daemon, falls back to reading the "security-rules" mock resource
// and filtering by group name. (The mock SG rows don't carry rules
// themselves ; the rules live in a sibling resource, which mirrors
// vzd's SecurityGroupInfo.rules embedding.)
func handleGetSecurityGroupRules(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if live != nil {
		rules, err := live.GetSecurityGroup(r.Context(), uuid)
		if err == nil {
			writeJSON(w, http.StatusOK, rules)
			return
		}
		if !wclient.IsUnimplemented(err) {
			writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
			return
		}
	}
	// Mock fallback : look up the SG name from the security-groups
	// resource, then filter the security-rules resource by `group`.
	var groupName string
	for _, row := range resourceByID["security-groups"].Rows {
		if row["uuid"] == uuid {
			groupName, _ = row["name"].(string)
			break
		}
	}
	out := make([]map[string]any, 0)
	if groupName != "" {
		for _, row := range resourceByID["security-rules"].Rows {
			if row["group"] == groupName {
				// Translate the security-rules columns to the
				// SecurityRule shape the drawer expects.
				portMin, portMax := parsePortRange(row["port_range"])
				out = append(out, map[string]any{
					"direction":         row["direction"],
					"protocol":          row["protocol"],
					"port_min":          portMin,
					"port_max":          portMax,
					"remote_cidr":       row["remote"],
					"remote_group_uuid": "",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// parsePortRange turns "80", "8000-8100", or "" into (min, max).
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

// handleSetSecurityGroupRules : PUT /api/security-groups/{uuid}/rules
// Body : []SecurityRule. Replaces the SG's rule list atomically.
func handleSetSecurityGroupRules(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	var body []wclient.SecurityRule
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if err := live.SetSecurityGroupRules(r.Context(), r.PathValue("uuid"), body); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "security-group.set-rules")
	writeJSON(w, http.StatusOK, map[string]any{"uuid": r.PathValue("uuid"), "rules": len(body)})
}

// handleDeleteSecurityGroup : DELETE /api/security-groups/{uuid}
func handleDeleteSecurityGroup(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	uuid := r.PathValue("uuid")
	if uuid == "" {
		writeErr(w, errBadReq("uuid is required"))
		return
	}
	if err := live.DeleteSecurityGroup(r.Context(), uuid); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "security-group.delete")
	w.WriteHeader(http.StatusNoContent)
}

func handleDeleteNetwork(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	uuid := r.PathValue("uuid")
	if uuid == "" {
		writeErr(w, errBadReq("uuid is required"))
		return
	}
	if err := live.DeleteNetwork(r.Context(), uuid); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "network.delete")
	w.WriteHeader(http.StatusNoContent)
}
