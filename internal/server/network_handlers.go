// network_handlers.go — Create / Delete handlers for the resources
// owned by the weft-network controller (Routers, Load Balancers,
// DNS Zones, DNS Records, Scheduling Rules).
//
// Live-first against `liveNet` ; on Unimplemented (or no controller
// wired), routers / LBs / DNS surface a 503 so the operator knows the
// controller isn't ready, while scheduling-rules fall back to the
// existing in-memory store (the operator wrote the rule via the
// dashboard before weft-network landed — keep the affordance).
package server

import (
	"net/http"

	"github.com/openweft/weft-webui/internal/wclient"
)

// requireLiveNet writes a 503 when the controller isn't wired. Used
// for resources where there's no sane mock-mode mutation path.
func requireLiveNet(w http.ResponseWriter) bool {
	if liveNet == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "no live weft-network controller configured ; start the webui with --weft-network-socket",
		})
		return false
	}
	return true
}

// --- Routers -------------------------------------------------------

func handleCreateRouter(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	_, project := scopeFromRequest(r)
	if project == "" {
		project = "platform" // edge routers live there
	}
	var body struct {
		Name, Kind, Backend, External string
		Networks                      []string
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" || body.Kind == "" {
		writeErr(w, errBadReq("name and kind are required"))
		return
	}
	uuid, err := liveNet.CreateRouter(r.Context(), wclient.CreateRouterOpts{
		Project: project, Name: body.Name, Kind: body.Kind, Backend: body.Backend,
		Networks: body.Networks, External: body.External,
	})
	if err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "router.create")
	writeJSON(w, http.StatusCreated, map[string]any{"name": body.Name, "uuid": uuid})
}

func handleDeleteRouter(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	if err := liveNet.DeleteRouter(r.Context(), r.PathValue("uuid")); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "router.delete")
	w.WriteHeader(http.StatusNoContent)
}

// --- Load Balancers ------------------------------------------------

func handleCreateLoadBalancer(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	project, perr := resolveVMProject(r)
	if perr != nil {
		writeErr(w, perr)
		return
	}
	var body struct {
		Name, Mode, AZ string
		Port           uint32
		Backends       []string
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" || body.Port == 0 {
		writeErr(w, errBadReq("name and port are required"))
		return
	}
	uuid, err := liveNet.CreateLoadBalancer(r.Context(), wclient.CreateLoadBalancerOpts{
		Project: project, Name: body.Name, Mode: body.Mode, Port: body.Port,
		Backends: body.Backends, AZ: body.AZ,
	})
	if err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "lb.create")
	writeJSON(w, http.StatusCreated, map[string]any{"name": body.Name, "uuid": uuid})
}

func handleDeleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	if err := liveNet.DeleteLoadBalancer(r.Context(), r.PathValue("uuid")); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "lb.delete")
	w.WriteHeader(http.StatusNoContent)
}

// handleSetLoadBalancerBackends : PUT /api/loadbalancers/{uuid}/backends
// Body : []string. Replaces the backend list atomically.
func handleSetLoadBalancerBackends(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	var body []string
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if err := liveNet.SetLoadBalancerBackends(r.Context(), r.PathValue("uuid"), body); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "lb.set-backends")
	writeJSON(w, http.StatusOK, map[string]int{"backends": len(body)})
}

// --- DNS Zones -----------------------------------------------------

func handleCreateDNSZone(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	_, project := scopeFromRequest(r)
	if project == "" {
		project = "platform"
	}
	var body struct {
		Name, Role, PushTarget string
		TTLDefault             int32
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" {
		writeErr(w, errBadReq("name is required"))
		return
	}
	uuid, err := liveNet.CreateDNSZone(r.Context(), wclient.CreateDNSZoneOpts{
		Project: project, Name: body.Name, Role: body.Role,
		TTLDefault: body.TTLDefault, PushTarget: body.PushTarget,
	})
	if err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "dns-zone.create")
	writeJSON(w, http.StatusCreated, map[string]any{"name": body.Name, "uuid": uuid})
}

func handleDeleteDNSZone(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	if err := liveNet.DeleteDNSZone(r.Context(), r.PathValue("uuid")); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "dns-zone.delete")
	w.WriteHeader(http.StatusNoContent)
}

// --- DNS Records ---------------------------------------------------

func handleCreateDNSRecord(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	var body struct {
		ZoneUUID, Name, Type, Value string
		TTL                         int32
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.ZoneUUID == "" || body.Type == "" || body.Value == "" {
		writeErr(w, errBadReq("zone_uuid, type, value are required"))
		return
	}
	uuid, err := liveNet.CreateDNSRecord(r.Context(), wclient.CreateDNSRecordOpts{
		ZoneUUID: body.ZoneUUID, Name: body.Name, Type: body.Type,
		Value: body.Value, TTL: body.TTL,
	})
	if err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "dns-record.create")
	writeJSON(w, http.StatusCreated, map[string]any{"uuid": uuid, "type": body.Type, "name": body.Name})
}

func handleDeleteDNSRecord(w http.ResponseWriter, r *http.Request) {
	if !requireLiveNet(w) {
		return
	}
	if err := liveNet.DeleteDNSRecord(r.Context(), r.PathValue("uuid")); err != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "net: " + err.Error()})
		return
	}
	userAction(r, "dns-record.delete")
	w.WriteHeader(http.StatusNoContent)
}
