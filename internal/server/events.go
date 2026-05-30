// events.go — Server-Sent Events bridge that proxies the agent's
// WatchEvents gRPC stream to the SPA. The browser's EventSource API
// reconnects automatically when the TCP connection drops ; we honour
// that by treating each GET /api/events as a fresh stream.
//
// Query params :
//   kind=microvm.   one or more `kind=` repetitions become the
//                   kindPrefix filter on the gRPC side. Same any-match
//                   semantics — no kind= = no filter.
//   project=<n>     restrict to a project (name OR uuid)
//   subject=<s>     restrict to a subject (typically a VM name)
//
// In mock mode the endpoint synthesises a tiny demo heartbeat so the
// SPA's toast bar still proves the wiring works.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
)

func handleEvents(w http.ResponseWriter, r *http.Request) {
	// Mandatory SSE headers. http.Flusher lets us push each event
	// straight to the wire without buffering.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if present
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Filter parameters.
	prefixes := r.URL.Query()["kind"]
	project := r.URL.Query().Get("project")
	subject := r.URL.Query().Get("subject")
	// Default project to the session scope so the SPA doesn't have to
	// pass it on every reconnect.
	if project == "" {
		_, project = scopeFromRequest(r)
	}

	ctx := r.Context()
	if live != nil {
		streamLive(ctx, w, flusher, prefixes, project, subject)
		return
	}
	streamMock(ctx, w, flusher, prefixes)
}

// streamLive bridges WatchEvents → SSE.
func streamLive(ctx context.Context, w http.ResponseWriter, flusher http.Flusher,
	prefixes []string, project, subject string,
) {
	stream, err := live.WatchEvents(ctx, prefixes, project, subject)
	if err != nil {
		sseError(w, flusher, "live: "+err.Error())
		return
	}
	defer stream.Close()
	sseComment(w, flusher, "ready · live · prefixes="+strings.Join(prefixes, ","))

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			// EventSource silently drops idle connections after ~30s
			// on some proxies ; a comment line is a free keepalive.
			sseComment(w, flusher, "ping")
		case ev, ok := <-stream.Events:
			if !ok {
				return
			}
			sseData(w, flusher, ev)
			userActionFromEvent(ctx, ev)
		case err := <-stream.Errors:
			if err != nil && ctx.Err() == nil {
				sseError(w, flusher, "live: "+err.Error())
			}
			return
		}
	}
}

// streamMock emits a short demo heartbeat so the SPA's toast bar
// proves the wiring works in dev. Cycles through a few event kinds
// every 6 seconds. Each event also carries a rotating mock `actor`
// (the OIDC `sub` that triggered the action) in meta so the Activity
// page's "by user" filter has something to bite on.
func streamMock(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, prefixes []string) {
	sseComment(w, flusher, "ready · mock · 6s heartbeat")
	demo := []map[string]any{
		{"kind": "vm.state.running", "subject": "web-1", "project": "team-alpha"},
		{"kind": "volume.attached", "subject": "pg-data", "project": "team-alpha"},
		{"kind": "scheduling-rule.compliant", "subject": "nats-quorum", "project": "platform"},
		{"kind": "dns.record.upserted", "subject": "web-1.team-alpha.acme.weft.internal", "project": "team-alpha"},
		{"kind": "lb.backends.changed", "subject": "web-prod", "project": "team-alpha"},
	}
	// Mock actors : the agent will carry the real OIDC `sub` in meta on
	// every mutation-derived event ; the heartbeat synthesises one so
	// the SPA's filter chips/dropdown have variety even pre-wiring.
	actors := []string{
		"alice@acme.example",
		"bob@acme.example",
		"carol@platform.example",
		"system",
	}
	t := time.NewTicker(6 * time.Second)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ev := demo[i%len(demo)]
			actor := actors[i%len(actors)]
			i++
			if !kindMatches(prefixes, ev["kind"].(string)) {
				continue
			}
			// Fresh copy : the demo map is shared across iterations,
			// and we don't want a previous tick's meta leaking forward.
			out := map[string]any{
				"ts":      time.Now().UTC().Format(time.RFC3339Nano),
				"kind":    ev["kind"],
				"subject": ev["subject"],
				"project": ev["project"],
				"meta":    map[string]string{"actor": actor},
			}
			sseData(w, flusher, out)
		}
	}
}

func kindMatches(prefixes []string, kind string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if p == "" || strings.HasPrefix(kind, p) {
			return true
		}
	}
	return false
}

// --- SSE framing helpers --------------------------------------------

func sseData(w http.ResponseWriter, flusher http.Flusher, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func sseError(w http.ResponseWriter, flusher http.Flusher, msg string) {
	fmt.Fprintf(w, "event: error\ndata: %s\n\n", msg)
	flusher.Flush()
}

func sseComment(w http.ResponseWriter, flusher http.Flusher, msg string) {
	fmt.Fprintf(w, ": %s\n\n", msg)
	flusher.Flush()
}

// userActionFromEvent records a per-user counter for kinds that map
// to a tracked action verb. Cheap : skip when the event doesn't.
func userActionFromEvent(ctx context.Context, ev map[string]any) {
	if metrics == nil {
		return
	}
	u := auth.UserFromContext(ctx)
	if u == nil || u.Subject == "" {
		return
	}
	// e.g. kind=vm.state.running → action="vm.state.running"
	if k, ok := ev["kind"].(string); ok && k != "" {
		metrics.UserAction(u.Subject, "event:"+k)
	}
}
