// lifecycle.go — row-action handlers (Start/Stop/Delete) wired
// straight to vzd. These exist so the SPA's ResourceTable dropdown
// can do something real beyond viewing a row.
//
// All mutators require a live gRPC client : without --weft-socket the
// handlers return 503 (no daemon). The webui never simulates state
// changes on its mock data — that path would diverge from production
// silently.
//
// Auth model :
//   - GET / list paths already filter by the session's bearer (vzd
//     enforces RBAC).
//   - Mutations here trust vzd : if the daemon refuses, we proxy the
//     gRPC status code through as a 4xx.
package server

import (
	"net/http"

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

// handleCreateVM : POST /api/microvms  {name, image, cpu, mem_mb,
// disk_gb, ssh_pub}. Project comes from the session scope.
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
		Name, Image, SSHPub string
		CPU                 uint32
		MemMB, DiskGB       uint64
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Name == "" || body.Image == "" {
		writeErr(w, errBadReq("name and image are required"))
		return
	}
	if cerr := live.CreateVM(r.Context(), wclient.CreateVMOpts{
		Name: body.Name, Image: body.Image, Project: project,
		SSHPubKey: body.SSHPub,
		CPU:       body.CPU, MemMB: body.MemMB, DiskGB: body.DiskGB,
	}); cerr != nil {
		writeErr(w, &httpErr{http.StatusBadGateway, "live: " + cerr.Error()})
		return
	}
	userAction(r, "microvm.create")
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name, "project": project})
}

// --- Volume / Network mutators -------------------------------------

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
