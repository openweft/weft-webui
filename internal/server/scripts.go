// scripts.go — read/write catalogue of reusable provisioning scripts.
//
// Operators name + version their first-boot scripts here rather than
// pasting raw sh into the CreateVMModal textarea each time. The modal
// picks by name ; the server still stamps the script body verbatim on
// the VM's weft.boot/script property (so the in-guest weft-vm-agent's
// behaviour is unchanged — see 2c098e7).
//
// Same shape as flavors.go : interface + in-memory seed today,
// etcd-backed swap in front of weft-agent later. The seam is the
// scriptCatalogue interface ; a liveScriptCatalogue wrapping
// wclient.ListScripts lands when the proto extension does.
//
// Target topology (cross-repo, not implemented here) :
//
//	etcd                /weft/catalogue/scripts/<name>     →  JSON
//	weft-agent          watch the prefix, cache, serve via
//	                    ListScripts / SetScript / DeleteScript RPCs.
//	                    Same embedded-etcd dev story as flavors
//	                    ([[openweft-etcd-embedded]]).
//	weft-cli            `weft script {create,update,delete,list}`
//	weft-webui          drops the in-memory seed, becomes a plain
//	                    consumer of ListScripts.
//
// Scope is ScopeAdmin : like flavors, the catalogue is a cluster-wide
// artefact only the superadmin (or tenant admins of a future per-
// tenant variant) defines. The user UI still needs to READ the
// catalogue inside CreateVMModal — same parallel /api/scripts endpoint
// exposed on both listeners.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
)

// Script is the wire shape both /api/scripts and /api/resources/scripts
// emit. Body is the literal sh source ; the operator's editor saves
// what they typed, we don't reformat.
type Script struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	UpdatedAt   string `json:"updated_at"`
	UpdatedBy   string `json:"updated_by"`
}

// scriptCatalogue is the contract every consumer goes through.
// Write methods exist (unlike flavorCatalogue today) because
// operators manage scripts from the dashboard's Scripts page.
type scriptCatalogue interface {
	List(ctx context.Context) ([]Script, error)
	Get(ctx context.Context, name string) (Script, bool)
	Set(ctx context.Context, s Script) error
	Delete(ctx context.Context, name string) error
}

type memScriptCatalogue struct {
	mu      sync.Mutex
	scripts []Script
}

func newMemScriptCatalogue() *memScriptCatalogue {
	return &memScriptCatalogue{scripts: seedScripts()}
}

// seedScripts — two demonstrative scripts so the Scripts page + the
// CreateVMModal picker aren't empty on first open. Realistic content,
// not just "echo hi" placeholders.
func seedScripts() []Script {
	now := "2026-05-20T12:00:00Z"
	return []Script{
		{
			Name:        "nginx-from-source",
			Description: "Pulls + installs the project's static site from a git checkout.",
			Body: `#!/bin/sh
set -eu
# Payload is in $PWD (weft-vm-agent cd's into the cloned repo).
apk add --no-cache nginx
mkdir -p /var/www/html
cp -r ./public/* /var/www/html/
cat > /etc/nginx/http.d/site.conf <<'EOF'
server { listen 80 ; root /var/www/html ; index index.html ; }
EOF
rc-update add nginx
rc-service nginx start
`,
			UpdatedAt: now,
			UpdatedBy: "dev@weft.local",
		},
		{
			Name:        "compose-up",
			Description: "Brings up a docker-compose project from the payload.",
			Body: `#!/bin/sh
set -eu
apk add --no-cache docker docker-cli-compose
rc-update add docker
rc-service docker start
sleep 2
docker compose up -d
`,
			UpdatedAt: now,
			UpdatedBy: "dev@weft.local",
		},
	}
}

func (m *memScriptCatalogue) List(ctx context.Context) ([]Script, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Script, len(m.scripts))
	copy(out, m.scripts)
	return out, nil
}

func (m *memScriptCatalogue) Get(ctx context.Context, name string) (Script, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.scripts {
		if s.Name == name {
			return s, true
		}
	}
	return Script{}, false
}

func (m *memScriptCatalogue) Set(ctx context.Context, s Script) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.scripts {
		if x.Name == s.Name {
			m.scripts[i] = s
			return nil
		}
	}
	m.scripts = append(m.scripts, s)
	return nil
}

func (m *memScriptCatalogue) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.scripts {
		if s.Name == name {
			m.scripts = append(m.scripts[:i], m.scripts[i+1:]...)
			return nil
		}
	}
	return nil // idempotent : missing key is not an error
}

// scriptsCatalogue is the process-wide singleton. Today always the in-
// memory impl ; the live wrapper lands when weft-agent ships
// ListScripts / SetScript RPCs.
var scriptsCatalogue scriptCatalogue = newMemScriptCatalogue()

// scriptRows projects the catalogue to the map[string]any shape the
// generic /api/resources/{id} path expects. Same indirection as
// flavorRows() — the registry's "scripts" entry stays declarative.
func scriptRows(ctx context.Context) []map[string]any {
	ss, err := scriptsCatalogue.List(ctx)
	if err != nil || len(ss) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(ss))
	for _, s := range ss {
		// "lines" gives an at-a-glance complexity hint without dumping
		// the whole body into the table.
		lines := 1 + strings.Count(s.Body, "\n")
		out = append(out, map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"lines":       lines,
			"updated_at":  s.UpdatedAt,
			"updated_by":  s.UpdatedBy,
		})
	}
	return out
}

// ---- handlers ----

// handleListScripts — GET /api/scripts (both ports).
func handleListScripts(w http.ResponseWriter, r *http.Request) {
	ss, err := scriptsCatalogue.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if ss == nil {
		ss = []Script{}
	}
	writeJSON(w, http.StatusOK, ss)
}

// handleGetScript — GET /api/scripts/{name}. The body is the heavy
// payload ; the modal calls this only when the operator picks a
// script (lazy load to avoid sending every script's body in the
// /api/scripts listing).
func handleGetScript(w http.ResponseWriter, r *http.Request) {
	s, ok := scriptsCatalogue.Get(r.Context(), r.PathValue("name"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such script"})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// handleSetScript — POST /api/scripts (create or update).
// Admin-gated by the registry's ScopeAdmin handling ; the route is
// only mounted on the admin listener.
//
// The handler stamps UpdatedAt + UpdatedBy server-side so the client
// can't lie about provenance.
func handleSetScript(w http.ResponseWriter, r *http.Request) {
	var body Script
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	body.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if u := auth.UserFromContext(r.Context()); u != nil {
		body.UpdatedBy = u.Email
		if body.UpdatedBy == "" {
			body.UpdatedBy = u.Subject
		}
	}
	if err := scriptsCatalogue.Set(r.Context(), body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// handleDeleteScript — DELETE /api/scripts/{name}. Idempotent : a
// missing script is 200, not 404, so a retried client doesn't see a
// confusing inconsistency.
func handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := scriptsCatalogue.Delete(r.Context(), name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}
