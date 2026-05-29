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

// --- VM lifecycle ---------------------------------------------------

func handleStartVM(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	name := r.PathValue("name")
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := live.StartVM(r.Context(), name, project); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "microvm.start")
	writeJSON(w, http.StatusAccepted, map[string]string{"name": name, "state": "starting"})
}

func handleStopVM(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	name := r.PathValue("name")
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := live.StopVM(r.Context(), name, project); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "microvm.stop")
	writeJSON(w, http.StatusAccepted, map[string]string{"name": name, "state": "stopping"})
}

func handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	name := r.PathValue("name")
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := live.DeleteVM(r.Context(), name, project); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + err.Error()})
		return
	}
	userAction(r, "microvm.delete")
	w.WriteHeader(http.StatusNoContent)
}

// --- VM inspect (status / timings / logs) ---------------------------
//
// Read-only endpoints the SPA's microVM drawer hits when the operator
// opens a row. Like the lifecycle mutators they need a project, so
// they go through resolveVMProject ; mock mode returns 503 so the
// drawer surfaces the same "no daemon" message instead of hanging.

func handleVMStatus(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	name := r.PathValue("name")
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	info, cerr := live.VMStatus(r.Context(), name, project)
	if cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func handleVMTimings(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	name := r.PathValue("name")
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	events, cerr := live.VMTimings(r.Context(), name, project)
	if cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// handleVMLogs supports ?tail=<bytes>. Defaults to 65536 (64 KiB) so a
// VM with a giant console.log doesn't blow up the SPA on first open.
func handleVMLogs(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	name := r.PathValue("name")
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var tail int64 = 65536
	if t := r.URL.Query().Get("tail"); t != "" {
		var n int64
		_, _ = fmt.Sscanf(t, "%d", &n)
		if n >= 0 {
			tail = n
		}
	}
	out, cerr := live.VMLogs(r.Context(), name, project, tail)
	if cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

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

// handleCreateVM : POST /api/microvms
//
//	{name, image, flavor, scheduling_rule, network,
//	 ingress_kind, ingress_floating_ip, ingress_load_balancer,
//	 provisioning}
//
// A microVM is the combination of a flavor (sizing envelope), an
// image, and a scheduling policy ; cpu/ram/disk aren't independent
// fields — they're resolved from the flavor catalogue on the server.
// SSH keys are pushed at runtime via /api/microvms/{name}/keys.
//
// Wire model — DECIDED : pull / reconcile (not push). When the proto
// extension lands, scheduling_rule and network ride on
// CreateVMRequest as plain string labels ; weft-agent persists them
// on the VMRecord and does nothing else. weft-network's reconcile
// loop (which already powers the scheduling-rule.compliant /
// drifting event stream) watches VM events, sees the labels, and
// applies AttachVM / BindRule on its own. Same pattern as
// weft-vm-agent's guest-side Subscriber+ApplyFunc — pull, never push,
// at every layer. No agent→network gRPC dependency in the hot path.
//
// Wire status of the optional fields, today :
//   - scheduling_rule : captured ; ignored by the wclient call
//     (proto extension pending). Selector-based rules in
//     weft-network still match by labels, so this is forward-
//     compat without breaking anything.
//   - network         : captured ; ignored by the wclient call
//     (proto extension pending). Same forward-compat story.
//   - ingress         : best-effort orchestration post-create using
//     existing endpoints — MapFloatingIP for kind=floating_ip,
//     SetLoadBalancerBackends for kind=loadbalancer. This is the
//     transitional push path ; once weft-network reconciles ingress
//     too, drop these calls — the labels on the VMRecord drive it.
//   - provisioning    : stamps weft.boot/* properties (guest-readable).
//     The in-guest weft-vm-agent reads them on first boot — pull
//     side again. See lifecycle.go writeBootProperty + 2c098e7.
func handleCreateVM(w http.ResponseWriter, r *http.Request) {
	if !requireLive(w) {
		return
	}
	project, err := resolveVMProject(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Name, Image, Flavor                    string
		SchedulingRule, Network                string
		IngressKind                            string // "none" | "floating_ip" | "loadbalancer"
		IngressFloatingIP, IngressLoadBalancer string
		Provisioning                           *struct {
			SourceKind string `json:"source_kind"` // "none" | "git" | "oci"
			SourceURL  string `json:"source_url"`
			SourceRef  string `json:"source_ref"`
			Script     string `json:"script"`
		}
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" || body.Image == "" {
		writeErr(w, errBadReq("name and image are required"))
		return
	}
	if body.Flavor == "" {
		writeErr(w, errBadReq("flavor is required (microVM = flavor + image + scheduling)"))
		return
	}
	spec, ok := resolveFlavor(r.Context(), body.Flavor)
	if !ok {
		writeErr(w, errBadReq("unknown flavor: "+body.Flavor))
		return
	}
	if spec.CPU == 0 || spec.MemMB == 0 {
		writeErr(w, errBadReq("flavor "+body.Flavor+" has incomplete spec (cpu/ram)"))
		return
	}
	if cerr := live.CreateVM(r.Context(), wclient.CreateVMOpts{
		Name: body.Name, Image: body.Image, Project: project,
		CPU: spec.CPU, MemMB: spec.MemMB, DiskGB: spec.DiskGB,
	}); cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	userAction(r, "microvm.create")

	// Best-effort ingress orchestration. Errors are warnings on the
	// response, not failures : the VM is already up.
	var warnings []string
	switch body.IngressKind {
	case "", "none":
		// nothing to do
	case "floating_ip":
		if body.IngressFloatingIP == "" {
			warnings = append(warnings, "ingress=floating_ip but no IngressFloatingIP UUID — allocation flow not wired here ; allocate then Map from the Floating IPs page")
			break
		}
		if err := live.MapFloatingIP(r.Context(), body.IngressFloatingIP, "vm", body.Name); err != nil {
			warnings = append(warnings, "MapFloatingIP: "+err.Error())
		}
	case "loadbalancer":
		if body.IngressLoadBalancer == "" {
			warnings = append(warnings, "ingress=loadbalancer but no IngressLoadBalancer UUID")
			break
		}
		if liveNet == nil {
			warnings = append(warnings, "ingress=loadbalancer requires --weft-network-socket")
			break
		}
		// No incremental "add backend" RPC yet ; read the LB's current
		// backends, append, re-set. Tolerate the LB not being in the
		// first page — pages don't fan out at the LB count this
		// installation realistically sees.
		rows, _, lerr := liveNet.ListLoadBalancers(r.Context(), project, wclient.ListOpts{Limit: 1000})
		if lerr != nil {
			warnings = append(warnings, "list LBs: "+lerr.Error())
			break
		}
		backends := []string{body.Name}
		for _, row := range rows {
			if u, _ := row["uuid"].(string); u != body.IngressLoadBalancer {
				continue
			}
			if cur, _ := row["backends"].(string); cur != "" {
				for _, b := range strings.Split(cur, ",") {
					b = strings.TrimSpace(b)
					if b != "" && b != body.Name {
						backends = append([]string{b}, backends...)
					}
				}
			}
			break
		}
		if err := liveNet.SetLoadBalancerBackends(r.Context(), body.IngressLoadBalancer, backends); err != nil {
			warnings = append(warnings, "SetLoadBalancerBackends: "+err.Error())
		}
	default:
		warnings = append(warnings, "unknown ingress kind: "+body.IngressKind)
	}

	// First-boot provisioning : write the requested source + script as
	// guest-readable properties on the new VM. The in-guest weft-vm-agent
	// reads weft.boot/* on its first boot, pulls (git clone / oras pull
	// + extract), and runs the script via mvdan.cc/sh/v3 in the payload's
	// CWD. The "weft.boot/" prefix is reserved — operators set their
	// own provisioning out-of-band by editing the VM's Properties tab
	// directly if they want to override.
	if body.Provisioning != nil {
		p := body.Provisioning
		switch p.SourceKind {
		case "", "none":
			// Only a script ; still legitimate (no payload, just run the lines).
			if strings.TrimSpace(p.Script) != "" {
				writeBootProperty(body.Name, "weft.boot/script", p.Script)
			}
		case "git", "oci":
			writeBootProperty(body.Name, "weft.boot/source.kind", p.SourceKind)
			writeBootProperty(body.Name, "weft.boot/source.url", p.SourceURL)
			if p.SourceRef != "" {
				writeBootProperty(body.Name, "weft.boot/source.ref", p.SourceRef)
			}
			if strings.TrimSpace(p.Script) != "" {
				writeBootProperty(body.Name, "weft.boot/script", p.Script)
			}
		default:
			warnings = append(warnings, "unknown provisioning source kind: "+p.SourceKind)
		}
	}

	out := map[string]any{"name": body.Name, "project": project}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	writeJSON(w, http.StatusCreated, out)
}

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
