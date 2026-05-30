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
	"strings"
	"sync"

	"github.com/openweft/weft-webui/internal/wclient"
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

// liveScriptCatalogue wraps wclient.{List,Get,Set,Delete}Script with
// a transparent fallback to the in-memory seed on Unimplemented.
// Same shape as liveFlavorCatalogue, with two differences :
//
//   - Set / Delete are real writes (vs flavor's read-only catalogue) ;
//     they go straight to the agent and don't fall back to mem (a
//     pretend-success on an Unimplemented agent would be a lie that
//     bites later when the operator's edit silently vanishes).
//   - Get falls back to the cached List output then to the seed —
//     same logic as the flavor wrapper so the CreateVMModal picker
//     stays usable when the agent's slow / older / Unimplemented.
type liveScriptCatalogue struct {
	live *wclient.Client
	mem  *memScriptCatalogue

	mu     sync.Mutex
	cached []Script
}

func newLiveScriptCatalogue(live *wclient.Client) *liveScriptCatalogue {
	return &liveScriptCatalogue{
		live: live,
		mem:  newMemScriptCatalogue(),
	}
}

func (l *liveScriptCatalogue) List(ctx context.Context) ([]Script, error) {
	rows, _, err := l.live.ListScripts(ctx, wclient.ListOpts{})
	if err != nil {
		if wclient.IsUnimplemented(err) {
			return l.mem.List(ctx)
		}
		return nil, err
	}
	out := make([]Script, 0, len(rows))
	for _, r := range rows {
		out = append(out, scriptFromRow(r))
	}
	l.mu.Lock()
	l.cached = append(l.cached[:0], out...)
	l.mu.Unlock()
	dup := make([]Script, len(out))
	copy(dup, out)
	return dup, nil
}

func (l *liveScriptCatalogue) Get(ctx context.Context, name string) (Script, bool) {
	row, err := l.live.GetScript(ctx, name)
	if err == nil {
		return scriptFromRow(row), true
	}
	if wclient.IsUnimplemented(err) {
		l.mu.Lock()
		cached := append([]Script(nil), l.cached...)
		l.mu.Unlock()
		for _, s := range cached {
			if s.Name == name {
				return s, true
			}
		}
		return l.mem.Get(ctx, name)
	}
	return Script{}, false
}

// Set goes straight to the agent ; an Unimplemented response is
// surfaced as a real error rather than swallowed. A silent
// in-memory accept would be a lie : the operator would see "saved"
// in the dashboard, then notice nothing took effect, and the bug
// hunt is brutal. Better to break loudly.
func (l *liveScriptCatalogue) Set(ctx context.Context, s Script) error {
	return l.live.SetScript(ctx, s.Name, s.Description, s.Body)
}

func (l *liveScriptCatalogue) Delete(ctx context.Context, name string) error {
	return l.live.DeleteScript(ctx, name)
}

// scriptFromRow lifts the wclient row-shape map back to the typed
// Script we expose. Defensive on type assertions — bogus rows just
// yield zeroed fields rather than panicking the catalogue.
func scriptFromRow(r map[string]any) Script {
	s := Script{}
	if v, ok := r["name"].(string); ok {
		s.Name = v
	}
	if v, ok := r["description"].(string); ok {
		s.Description = v
	}
	if v, ok := r["body"].(string); ok {
		s.Body = v
	}
	if v, ok := r["updated_at"].(string); ok {
		s.UpdatedAt = v
	}
	if v, ok := r["updated_by"].(string); ok {
		s.UpdatedBy = v
	}
	return s
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

// (Scripts handlers moved to huma — see api_scripts.go.)
